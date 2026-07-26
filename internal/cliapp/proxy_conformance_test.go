package cliapp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/iml885203/tunlease/internal/gatewayd"
	"github.com/iml885203/tunlease/internal/registry"
	"github.com/iml885203/tunlease/pkg/tunnelclient"
)

type observedRequest struct {
	Method     string              `json:"method"`
	Host       string              `json:"host"`
	RequestURI string              `json:"request_uri"`
	Headers    map[string][]string `json:"headers"`
	BodySHA256 string              `json:"body_sha256"`
	BodySize   int                 `json:"body_size"`
	Trailers   map[string][]string `json:"trailers"`
}

type observedExchange struct {
	Request          observedRequest
	Status           int
	Headers          map[string][]string
	Body             string
	ResponseTrailers map[string][]string
}

func TestProxyConformanceDirectFailOpenAndTunnel(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read origin request: %v", err)
			http.Error(w, "read request", http.StatusInternalServerError)
			return
		}
		observation := observedRequest{
			Method:     r.Method,
			Host:       r.Host,
			RequestURI: r.RequestURI,
			Headers:    conformanceHeaders(r.Header),
			BodySHA256: fmt.Sprintf("%x", sha256.Sum256(body)),
			BodySize:   len(body),
			Trailers:   cloneHeader(r.Trailer),
		}
		w.Header().Add("X-Origin-Value", "one")
		w.Header().Add("X-Origin-Value", "two")
		w.Header().Add("Set-Cookie", "first=1; Path=/")
		w.Header().Add("Set-Cookie", "second=2; Path=/")
		w.Header().Set("Trailer", "X-Origin-Trailer")
		w.WriteHeader(http.StatusMultiStatus)
		if err = json.NewEncoder(w).Encode(observation); err != nil {
			t.Errorf("write origin response: %v", err)
			return
		}
		w.Header().Set("X-Origin-Trailer", "complete")
	}))
	defer origin.Close()

	failOpen, err := buildOriginProxy(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := registry.NewMemory(registry.Options{
		MaxClaims: 64,
		Allowed:   []string{"/conformance/"},
	}, logger)
	tunnel := gatewayd.NewTunnel(store, nil)
	gateway := httptest.NewServer((&gatewayd.Server{
		Store: store, Tunnel: tunnel, FailOpen: failOpen,
	}).Handler())
	defer gateway.Close()

	client, err := tunnelclient.New(tunnelclient.Config{
		Gateway: gateway.URL, HTTPClient: gateway.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Start(context.Background(), []string{"/conformance/claimed"}, testServerPort(t, origin.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	direct := runConformanceExchange(t, origin.Client(), origin.URL+"/conformance/direct")
	unclaimed := runConformanceExchange(t, gateway.Client(), gateway.URL+"/conformance/unclaimed")
	claimed := runConformanceExchange(t, gateway.Client(), gateway.URL+"/conformance/claimed")

	assertProxyManagedHeaders(t, "fail-open", direct, unclaimed)
	assertProxyManagedHeaders(t, "claimed tunnel", direct, claimed)
	assertEquivalentExchange(t, "fail-open", direct, unclaimed, "/conformance/direct", "/conformance/unclaimed")
	assertEquivalentExchange(t, "claimed tunnel", direct, claimed, "/conformance/direct", "/conformance/claimed")
}

func TestFailOpenAndTunnelStreamWithoutBuffering(t *testing.T) {
	release := make(chan struct{})
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "first\n")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "second\n")
	}))
	defer origin.Close()

	failOpen, err := buildOriginProxy(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := registry.NewMemory(registry.Options{MaxClaims: 64, Allowed: []string{"/stream/"}}, logger)
	tunnel := gatewayd.NewTunnel(store, nil)
	gateway := httptest.NewServer((&gatewayd.Server{
		Store: store, Tunnel: tunnel, FailOpen: failOpen,
	}).Handler())
	defer gateway.Close()

	client, err := tunnelclient.New(tunnelclient.Config{
		Gateway: gateway.URL, HTTPClient: gateway.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Start(context.Background(), []string{"/stream/claimed"}, testServerPort(t, origin.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	type pendingResponse struct {
		body *bufio.Reader
		done func()
	}
	start := func(path string) pendingResponse {
		t.Helper()
		// The returned cleanup closes the streaming body after both chunks are
		// asserted by the caller.
		response, requestErr := gateway.Client().Get(gateway.URL + path) //nolint:bodyclose
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		return pendingResponse{
			body: bufio.NewReader(response.Body),
			done: func() { _ = response.Body.Close() },
		}
	}

	unclaimed := start("/stream/unclaimed")
	defer unclaimed.done()
	claimed := start("/stream/claimed")
	defer claimed.done()
	for name, response := range map[string]pendingResponse{
		"fail-open":      unclaimed,
		"claimed tunnel": claimed,
	} {
		first := make(chan string, 1)
		go func() {
			line, _ := response.body.ReadString('\n')
			first <- line
		}()
		select {
		case line := <-first:
			if line != "first\n" {
				t.Fatalf("%s first streamed chunk = %q", name, line)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s buffered the first response chunk", name)
		}
	}
	close(release)
	for name, response := range map[string]pendingResponse{
		"fail-open":      unclaimed,
		"claimed tunnel": claimed,
	} {
		if line, readErr := response.body.ReadString('\n'); readErr != nil || line != "second\n" {
			t.Fatalf("%s second streamed chunk = %q, %v", name, line, readErr)
		}
	}
}

func TestFailOpenAndTunnelStreamRequestBodyWithoutBuffering(t *testing.T) {
	firstChunk := make(chan string, 2)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader := bufio.NewReader(r.Body)
		line, err := reader.ReadString('\n')
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		firstChunk <- r.URL.Path + ":" + line
		rest, err := io.ReadAll(reader)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = w.Write(rest)
	}))
	defer origin.Close()

	failOpen, err := buildOriginProxy(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := registry.NewMemory(registry.Options{MaxClaims: 64, Allowed: []string{"/upload/"}}, logger)
	tunnel := gatewayd.NewTunnel(store, nil)
	gateway := httptest.NewServer((&gatewayd.Server{
		Store: store, Tunnel: tunnel, FailOpen: failOpen,
	}).Handler())
	defer gateway.Close()

	client, err := tunnelclient.New(tunnelclient.Config{
		Gateway: gateway.URL, HTTPClient: gateway.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Start(context.Background(), []string{"/upload/claimed"}, testServerPort(t, origin.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	for name, path := range map[string]string{
		"fail-open":      "/upload/unclaimed",
		"claimed tunnel": "/upload/claimed",
	} {
		t.Run(name, func(t *testing.T) {
			reader, writer := io.Pipe()
			request, requestErr := http.NewRequest(http.MethodPost, gateway.URL+path, reader)
			if requestErr != nil {
				t.Fatal(requestErr)
			}
			result := make(chan string, 1)
			go func() {
				response, doErr := gateway.Client().Do(request)
				if doErr != nil {
					result <- "error: " + doErr.Error()
					return
				}
				body, readErr := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if readErr != nil {
					result <- "error: " + readErr.Error()
					return
				}
				result <- string(body)
			}()
			if _, writeErr := io.WriteString(writer, "first\n"); writeErr != nil {
				t.Fatal(writeErr)
			}
			select {
			case got := <-firstChunk:
				if want := path + ":first\n"; got != want {
					t.Fatalf("first target chunk = %q, want %q", got, want)
				}
			case <-time.After(time.Second):
				t.Fatal("proxy buffered the request body before dispatch")
			}
			if _, writeErr := io.WriteString(writer, "second\n"); writeErr != nil {
				t.Fatal(writeErr)
			}
			if closeErr := writer.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			select {
			case got := <-result:
				if got != "second\n" {
					t.Fatalf("response body = %q", got)
				}
			case <-time.After(time.Second):
				t.Fatal("streaming request did not complete")
			}
		})
	}
}

func TestFailOpenAndTunnelPropagateClientCancellation(t *testing.T) {
	entered := make(chan string, 2)
	canceled := make(chan string, 2)
	origin := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		entered <- r.URL.Path
		<-r.Context().Done()
		canceled <- r.URL.Path
	}))
	defer origin.Close()

	failOpen, err := buildOriginProxy(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := registry.NewMemory(registry.Options{MaxClaims: 64, Allowed: []string{"/cancel/"}}, logger)
	tunnel := gatewayd.NewTunnel(store, nil)
	gateway := httptest.NewServer((&gatewayd.Server{
		Store: store, Tunnel: tunnel, FailOpen: failOpen,
	}).Handler())
	defer gateway.Close()

	client, err := tunnelclient.New(tunnelclient.Config{
		Gateway: gateway.URL, HTTPClient: gateway.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Start(context.Background(), []string{"/cancel/claimed"}, testServerPort(t, origin.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	for name, path := range map[string]string{
		"fail-open":      "/cancel/unclaimed",
		"claimed tunnel": "/cancel/claimed",
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			request, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, gateway.URL+path, bytes.NewBufferString("body"))
			if requestErr != nil {
				t.Fatal(requestErr)
			}
			result := make(chan error, 1)
			go func() {
				response, doErr := gateway.Client().Do(request)
				if response != nil {
					_ = response.Body.Close()
				}
				result <- doErr
			}()
			select {
			case got := <-entered:
				if got != path {
					t.Fatalf("origin path = %q, want %q", got, path)
				}
			case <-time.After(time.Second):
				t.Fatal("request did not reach target")
			}
			cancel()
			select {
			case doErr := <-result:
				if doErr == nil {
					t.Fatal("canceled client request returned no error")
				}
			case <-time.After(time.Second):
				t.Fatal("canceled client request did not return")
			}
			select {
			case got := <-canceled:
				if got != path {
					t.Fatalf("canceled origin path = %q, want %q", got, path)
				}
			case <-time.After(time.Second):
				t.Fatal("target did not observe client cancellation")
			}
		})
	}
}

func runConformanceExchange(t *testing.T, client *http.Client, target string) observedExchange {
	t.Helper()
	payload := bytes.Repeat([]byte{0x00, 0xff, 't', 'u', 'l'}, 200_000)
	body := bytes.NewReader(payload)
	request, err := http.NewRequest(http.MethodPost, target+"?signature=a%2Fb&item=one&item=two", body)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "callbacks.staging.example.com"
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Add("X-Provider-Value", "one")
	request.Header.Add("X-Provider-Value", "two")
	request.Header.Add("Cookie", "first=1")
	request.Header.Add("Cookie", "second=2")
	request.Header.Set("Connection", "X-Remove-Me")
	request.Header.Set("X-Remove-Me", "hop-by-hop")

	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	responseBody, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	var observation observedRequest
	if err = json.Unmarshal(responseBody, &observation); err != nil {
		t.Fatalf("decode observed request: %v; body=%q", err, responseBody)
	}
	return observedExchange{
		Request:          observation,
		Status:           response.StatusCode,
		Headers:          conformanceHeaders(response.Header),
		Body:             string(responseBody),
		ResponseTrailers: cloneHeader(response.Trailer),
	}
}

func assertEquivalentExchange(
	t *testing.T,
	name string,
	direct, proxied observedExchange,
	directPath, proxiedPath string,
) {
	t.Helper()
	direct.Request.RequestURI = replaceRequestPath(direct.Request.RequestURI, directPath, proxiedPath)
	direct.Body = replaceRequestPath(direct.Body, directPath, proxiedPath)
	direct.Request.Headers = withoutForwardingHeaders(direct.Request.Headers)
	proxied.Request.Headers = withoutForwardingHeaders(proxied.Request.Headers)
	// The response body is the JSON encoding of Request, which is compared in
	// structured form above. Ignore its incidental forwarding header encoding.
	direct.Body = ""
	proxied.Body = ""
	if !reflect.DeepEqual(proxied, direct) {
		t.Fatalf("%s exchange differs\n got: %#v\nwant: %#v", name, proxied, direct)
	}
}

func assertProxyManagedHeaders(t *testing.T, name string, direct, proxied observedExchange) {
	t.Helper()
	if direct.Request.Headers["X-Remove-Me"][0] != "hop-by-hop" {
		t.Fatal("direct test request lost its hop-by-hop sentinel")
	}
	if _, ok := proxied.Request.Headers["X-Remove-Me"]; ok {
		t.Fatalf("%s forwarded a Connection-nominated hop-by-hop header", name)
	}
	if values := proxied.Request.Headers["X-Forwarded-For"]; len(values) == 0 || values[0] == "" {
		t.Fatalf("%s did not record X-Forwarded-For", name)
	}
}

func replaceRequestPath(value, old, new string) string {
	return strings.ReplaceAll(value, old, new)
}

func conformanceHeaders(header http.Header) map[string][]string {
	out := cloneHeader(header)
	delete(out, "Content-Length")
	delete(out, "Date")
	return out
}

func withoutForwardingHeaders(header map[string][]string) map[string][]string {
	out := cloneHeader(http.Header(header))
	delete(out, "X-Forwarded-For")
	delete(out, "Connection")
	delete(out, "X-Remove-Me")
	return out
}

func cloneHeader(header http.Header) map[string][]string {
	out := make(map[string][]string, len(header))
	for name, values := range header {
		out[name] = append([]string(nil), values...)
	}
	return out
}

func testServerPort(t *testing.T, rawURL string) int {
	t.Helper()
	_, rawPort, err := net.SplitHostPort(rawURL[len("http://"):])
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

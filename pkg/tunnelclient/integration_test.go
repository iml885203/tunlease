package tunnelclient_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/iml885203/tunlease/internal/gatewayd"
	"github.com/iml885203/tunlease/internal/registry"
	"github.com/iml885203/tunlease/pkg/tunnelclient"
)

type testGateway struct {
	http   *httptest.Server
	store  *registry.Memory
	tunnel *gatewayd.Tunnel
	origin *atomic.Int64
}

func newTestGateway(t *testing.T, allowed []string) *testGateway {
	return newTestGatewayOptions(t, registry.Options{MaxClaims: 64, Allowed: allowed})
}

func newTestGatewayOptions(t *testing.T, options registry.Options) *testGateway {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := registry.NewMemory(options, logger)
	tunnel := gatewayd.NewTunnel(store, nil)
	originCalls := &atomic.Int64{}
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		fmt.Fprintf(w, "origin: %s", r.URL.Path)
	})
	server := httptest.NewServer((&gatewayd.Server{
		Store: store, Tunnel: tunnel, FailOpen: origin,
	}).Handler())
	t.Cleanup(server.Close)
	return &testGateway{http: server, store: store, tunnel: tunnel, origin: originCalls}
}

func TestClaimExpiresAtGatewayLimit(t *testing.T) {
	gateway := newTestGatewayOptions(t, registry.Options{
		MaxClaims:        64,
		MaxClaimDuration: 150 * time.Millisecond,
		Allowed:          []string{"/test/"},
	})
	client, err := tunnelclient.New(tunnelclient.Config{
		Gateway: gateway.http.URL, HTTPClient: gateway.http.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Start(context.Background(), []string{"/test/expiring/*"}, 8080)
	if err != nil {
		t.Fatal(err)
	}
	if session.Claim().ExpiresAt == nil {
		t.Fatal("claim did not include its expiration")
	}
	select {
	case <-session.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("claim did not expire")
	}
	var apiErr *tunnelclient.APIError
	if !errors.As(session.Err(), &apiErr) || apiErr.Code != "claim_expired" {
		t.Fatalf("session error = %v", session.Err())
	}
	eventually(t, func() bool { return len(gateway.store.List()) == 0 }, "expired claim release")
}

func TestSessionDataPathFallbackAndClose(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, "local: %s", r.URL.Path)
	}))
	defer local.Close()
	localPort := mustPort(t, local.URL)
	gateway := newTestGateway(t, []string{"/test/"})

	client, err := tunnelclient.New(tunnelclient.Config{
		Gateway: gateway.http.URL, HTTPClient: gateway.http.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Start(context.Background(), []string{"/test/anonymous/*"}, localPort)
	if err != nil {
		t.Fatal(err)
	}
	if session.Claim().Owner != "anonymous" {
		t.Fatalf("owner = %q", session.Claim().Owner)
	}
	if body := get(t, gateway.http, "/test/anonymous/hook?secret=do-not-log"); body != "local: /test/anonymous/hook" {
		t.Fatalf("tunnelled body = %q", body)
	}
	select {
	case event := <-session.Events():
		if event.Type != tunnelclient.EventRequestActivity {
			t.Fatalf("event type = %q", event.Type)
		}
		if event.Method != http.MethodGet || event.Path != "/test/anonymous/hook" || event.Status != http.StatusAccepted {
			t.Fatalf("activity event = %#v", event)
		}
		if strings.Contains(event.Path, "secret") || event.Duration < 0 {
			t.Fatalf("unsafe or invalid activity event = %#v", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request activity event was not emitted")
	}
	if body := get(t, gateway.http, "/unclaimed"); body != "origin: /unclaimed" {
		t.Fatalf("origin body = %q", body)
	}

	if err = session.Close(); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return len(gateway.store.List()) == 0 }, "claim release")
	if body := get(t, gateway.http, "/test/anonymous/hook"); body != "origin: /test/anonymous/hook" {
		t.Fatalf("post-close body = %q", body)
	}
}

func TestTunnelTransparentlyForwardsRequestAndResponse(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		w.Header().Set("X-Local-Response", "forwarded")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, "%s %s?%s %s %s", r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Provider-Signature"), body)
	}))
	defer local.Close()

	gateway := newTestGateway(t, []string{"/test/"})
	client, err := tunnelclient.New(tunnelclient.Config{
		Gateway: gateway.http.URL, HTTPClient: gateway.http.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Start(context.Background(), []string{"/test/forward/*"}, mustPort(t, local.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	request, err := http.NewRequest(http.MethodPost, gateway.http.URL+"/test/forward/hook?delivery=42", strings.NewReader(`{"ok":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Provider-Signature", "signed")
	response, err := gateway.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusCreated ||
		response.Header.Get("X-Local-Response") != "forwarded" ||
		string(body) != `POST /test/forward/hook?delivery=42 signed {"ok":true}` {
		t.Fatalf("response: status=%d header=%q body=%q", response.StatusCode, response.Header.Get("X-Local-Response"), body)
	}
	if gateway.origin.Load() != 0 {
		t.Fatalf("claimed request was also sent to origin %d time(s)", gateway.origin.Load())
	}
}

func TestTunnelMultiplexesConcurrentRequests(t *testing.T) {
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		arrived <- struct{}{}
		<-release
		_, _ = io.WriteString(w, "local")
	}))
	defer local.Close()

	gateway := newTestGateway(t, []string{"/test/"})
	client, err := tunnelclient.New(tunnelclient.Config{
		Gateway: gateway.http.URL, HTTPClient: gateway.http.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Start(context.Background(), []string{"/test/concurrent/*"}, mustPort(t, local.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	results := make(chan error, 2)
	for _, path := range []string{"one", "two"} {
		go func() {
			response, requestErr := gateway.http.Client().Get(gateway.http.URL + "/test/concurrent/" + path)
			if requestErr != nil {
				results <- requestErr
				return
			}
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr != nil {
				results <- readErr
				return
			}
			if string(body) != "local" {
				results <- fmt.Errorf("body = %q", body)
				return
			}
			results <- nil
		}()
	}
	for range 2 {
		select {
		case <-arrived:
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent requests did not both reach localhost")
		}
	}
	close(release)
	released = true
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent request did not complete")
		}
	}
	if gateway.origin.Load() != 0 {
		t.Fatalf("concurrent claimed requests reached origin %d time(s)", gateway.origin.Load())
	}
}

func TestPartialLocalResponseIsNeverReplayedToOrigin(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, "partial")
		w.(http.Flusher).Flush()
		connection, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = connection.Close()
		}
	}))
	defer local.Close()

	gateway := newTestGateway(t, []string{"/test/"})
	client, err := tunnelclient.New(tunnelclient.Config{
		Gateway: gateway.http.URL, HTTPClient: gateway.http.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Start(context.Background(), []string{"/test/partial/*"}, mustPort(t, local.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	response, err := gateway.http.Client().Get(gateway.http.URL + "/test/partial/hook")
	if err == nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		t.Fatal("partial local response unexpectedly completed")
	}
	if !strings.Contains(err.Error(), "EOF") {
		t.Fatalf("partial response error = %v", err)
	}
	if gateway.origin.Load() != 0 {
		t.Fatalf("partial dispatched response was replayed to origin %d time(s)", gateway.origin.Load())
	}
}

func TestUnavailableLocalTargetReturnsDescriptiveBadGatewayAndEvent(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}

	gateway := newTestGateway(t, []string{"/test/"})
	client, err := tunnelclient.New(tunnelclient.Config{
		Gateway: gateway.http.URL, HTTPClient: gateway.http.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Start(context.Background(), []string{"/test/unavailable/*"}, port)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	response, err := gateway.http.Client().Get(gateway.http.URL + "/test/unavailable/hook")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %q", response.StatusCode, body)
	}
	wantBody := "This path is claimed, but its local service is unavailable.\n\n" +
		"If this is your tunnel, check the terminal running tul.\n"
	if string(body) != wantBody {
		t.Fatalf("body = %q", body)
	}
	if gateway.origin.Load() != 0 {
		t.Fatalf("failed dispatched request was replayed to origin %d time(s)", gateway.origin.Load())
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-session.Events():
			if event.Type != tunnelclient.EventLocalTargetError {
				continue
			}
			if event.Err == nil || !strings.Contains(event.Err.Error(), "127.0.0.1") {
				t.Fatalf("event error = %v", event.Err)
			}
			return
		case <-deadline:
			t.Fatal("local target failure event was not emitted")
		}
	}
}

func TestIdleLocalTargetReturnsDescriptiveGatewayTimeout(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/slow") {
			time.Sleep(time.Second)
			_, _ = io.WriteString(w, "too late")
			return
		}
		_, _ = io.WriteString(w, "still connected")
	}))
	defer local.Close()

	gateway := newTestGateway(t, []string{"/test/"})
	gateway.tunnel.SetIdleTimeout(50 * time.Millisecond)
	client, err := tunnelclient.New(tunnelclient.Config{
		Gateway: gateway.http.URL, HTTPClient: gateway.http.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Start(context.Background(), []string{"/test/idle/*"}, mustPort(t, local.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	response, err := gateway.http.Client().Get(gateway.http.URL + "/test/idle/slow")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, body = %q", response.StatusCode, body)
	}
	if !strings.Contains(string(body), "tunneled request was idle for too long") {
		t.Fatalf("body = %q", body)
	}
	if gateway.origin.Load() != 0 {
		t.Fatalf("timed-out dispatched request was replayed to origin %d time(s)", gateway.origin.Load())
	}
	if body := get(t, gateway.http, "/test/idle/fast"); body != "still connected" {
		t.Fatalf("request after stream timeout = %q", body)
	}
}

func TestSessionReconnectReplacesClaim(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "local")
	}))
	defer local.Close()
	gateway := newTestGateway(t, []string{"/test/"})
	client, err := tunnelclient.New(tunnelclient.Config{
		Gateway: gateway.http.URL, HTTPClient: gateway.http.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Start(context.Background(), []string{"/test/reconnect/*"}, mustPort(t, local.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	initial := session.Claim().ID
	gateway.tunnel.CloseClaim(initial)
	for _, want := range []tunnelclient.EventType{
		tunnelclient.EventTunnelDisconnected,
		tunnelclient.EventTunnelReconnected,
	} {
		select {
		case event := <-session.Events():
			if event.Type != want {
				t.Fatalf("reconnect event = %q, want %q", event.Type, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %q", want)
		}
	}
	eventually(t, func() bool { return session.Claim().ID != initial }, "new claim after reconnect")
	if len(gateway.store.List()) != 1 {
		t.Fatalf("active claims = %#v", gateway.store.List())
	}
	if body := get(t, gateway.http, "/test/reconnect/hook"); body != "local" {
		t.Fatalf("reconnected body = %q", body)
	}
}

func TestExplicitReleaseStopsReconnect(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "local")
	}))
	defer local.Close()
	gateway := newTestGateway(t, []string{"/test/"})
	client, err := tunnelclient.New(tunnelclient.Config{
		Gateway: gateway.http.URL, HTTPClient: gateway.http.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Start(context.Background(), []string{"/test/release"}, mustPort(t, local.URL))
	if err != nil {
		t.Fatal(err)
	}

	if err = client.Release(context.Background(), session.Claim().ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("released session did not stop")
	}
	var apiErr *tunnelclient.APIError
	if !errors.As(session.Err(), &apiErr) || apiErr.Code != "claim_released" {
		t.Fatalf("session error = %v", session.Err())
	}
	time.Sleep(1200 * time.Millisecond)
	if claims := gateway.store.List(); len(claims) != 0 {
		t.Fatalf("released session reconnected: %#v", claims)
	}
	if body := get(t, gateway.http, "/test/release/hook"); body != "origin: /test/release/hook" {
		t.Fatalf("post-release body = %q", body)
	}
}

func TestRepeatedExplicitReleaseRetainsNoGatewayState(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "local")
	}))
	defer local.Close()
	gateway := newTestGateway(t, []string{"/test/"})
	client, err := tunnelclient.New(tunnelclient.Config{
		Gateway: gateway.http.URL, HTTPClient: gateway.http.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 10; attempt++ {
		session, startErr := client.Start(context.Background(), []string{fmt.Sprintf("/test/release-%d", attempt)}, mustPort(t, local.URL))
		if startErr != nil {
			t.Fatal(startErr)
		}
		if releaseErr := client.Release(context.Background(), session.Claim().ID); releaseErr != nil {
			t.Fatal(releaseErr)
		}
		select {
		case <-session.Done():
		case <-time.After(5 * time.Second):
			t.Fatalf("release %d did not stop session", attempt)
		}
	}
	eventually(t, func() bool {
		return len(gateway.store.List()) == 0 && gateway.tunnel.ActiveSessions() == 0
	}, "all repeated release state to clear")
}

// Wildcard semantics decide which requests a developer actually receives, so
// they are asserted over a live tunnel: the response body reports whether the
// gateway routed to the local server or fell open to the original app.
func TestClaimedWildcardsRouteRequestsToTheLocalServer(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "local: %s", r.URL.Path)
	}))
	defer local.Close()
	gateway := newTestGateway(t, []string{"/test/"})
	client, err := tunnelclient.New(tunnelclient.Config{
		Gateway: gateway.http.URL, HTTPClient: gateway.http.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Start(context.Background(),
		[]string{"/test/exact", "/test/events/*", "/test/tree/**"}, mustPort(t, local.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	for _, tt := range []struct {
		name     string
		request  string
		reaching string
	}{
		{"exact claim", "/test/exact", "local"},
		{"exact claim does not take a descendant", "/test/exact/child", "origin"},

		{"single-level wildcard takes a child", "/test/events/one", "local"},
		{"single-level wildcard does not take its base", "/test/events", "origin"},
		{"single-level wildcard does not take a grandchild", "/test/events/one/two", "origin"},

		{"recursive wildcard takes its base", "/test/tree", "local"},
		{"recursive wildcard takes a child", "/test/tree/one", "local"},
		{"recursive wildcard takes a grandchild", "/test/tree/one/two", "local"},

		{"unclaimed path", "/test/other", "origin"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			want := tt.reaching + ": " + tt.request
			if body := get(t, gateway.http, tt.request); body != want {
				t.Errorf("GET %s reached %q, want %q", tt.request, body, want)
			}
		})
	}
}

func get(t *testing.T, gateway *httptest.Server, path string) string {
	t.Helper()
	response, err := gateway.Client().Get(gateway.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func eventually(t *testing.T, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func mustPort(t *testing.T, rawURL string) int {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(rawURL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

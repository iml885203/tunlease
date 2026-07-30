package tunnelclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

func TestNewValidatesSingleTopology(t *testing.T) {
	for _, config := range []Config{
		{},
		{Gateway: "ftp://example.test"},
		{Gateway: "https://example.test/base"},
	} {
		if _, err := New(config); err == nil {
			t.Fatalf("New(%+v) succeeded", config)
		}
	}
	client, err := New(Config{Gateway: "example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if client.Gateway() != "https://example.test/_tunlease" {
		t.Fatalf("gateway = %q", client.Gateway())
	}
}

func TestClientOmitsAuthorizationWithoutToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"claims": []Claim{}})
	}))
	defer server.Close()

	client, err := New(Config{Gateway: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.List(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizePath(t *testing.T) {
	for input, want := range map[string]string{
		"/callback":    "/callback",
		"/callback/":   "/callback",
		"/callback/*":  "/callback/*",
		"/callback/**": "/callback/**",
	} {
		got, err := NormalizePath(input)
		if err != nil || got != want {
			t.Fatalf("NormalizePath(%q) = %q, %v", input, got, err)
		}
	}
	for _, input := range []string{"callback", "/", "/x/*/y", "/x/**/y", "/" + strings.Repeat("a", MaxPathLength)} {
		if _, err := NormalizePath(input); err == nil {
			t.Fatalf("NormalizePath(%q) succeeded", input)
		}
	}
}

func TestStartRejectsTooManyPathsBeforeDial(t *testing.T) {
	client, err := New(Config{Gateway: "example.test"})
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, MaxPathsPerClaim+1)
	for i := range paths {
		paths[i] = "/callback"
	}
	if _, err = client.Start(context.Background(), paths, 8080); err == nil {
		t.Fatal("too many paths were accepted")
	}
}

func TestClientListAndRelease(t *testing.T) {
	var released string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/_tunlease/api/v1/claims":
			_ = json.NewEncoder(w).Encode(map[string]any{"claims": []Claim{{
				ID: "claim-1", Owner: "dev", Paths: []string{"/callback/*"}, StartedAt: time.Now(),
			}}})
		case r.Method == http.MethodDelete && r.URL.Path == "/_tunlease/api/v1/claims/claim-1":
			released = "claim-1"
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := New(Config{Gateway: server.URL, Token: "secret", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := client.List(context.Background())
	if err != nil || len(claims) != 1 || claims[0].ID != "claim-1" {
		t.Fatalf("List() = %#v, %v", claims, err)
	}
	if err = client.Release(context.Background(), "claim-1"); err != nil {
		t.Fatal(err)
	}
	if released != "claim-1" {
		t.Fatalf("released = %q", released)
	}
}

func TestStartClaimsThroughTunnelHandshake(t *testing.T) {
	var pathsHeader string
	var protocolHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pathsHeader = r.Header.Get(headerPaths)
		protocolHeader = r.Header.Get(headerProtocol)
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "path_claimed", "detail": "overlap", "claimed_by": "alice",
		})
	}))
	defer server.Close()

	client, err := New(Config{Gateway: server.URL, Token: "secret", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Start(context.Background(), []string{"/callback"}, 8080)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "path_claimed" {
		t.Fatalf("Start error = %#v", err)
	}
	if pathsHeader != `["/callback"]` {
		t.Fatalf("paths header = %q", pathsHeader)
	}
	if protocolHeader != "1" {
		t.Fatalf("protocol header = %q", protocolHeader)
	}
}

func TestStartReportsProtocolUpgrade(t *testing.T) {
	for _, test := range []struct {
		code   string
		detail string
	}{
		{
			code:   "client_upgrade_required",
			detail: "gateway protocol 2 requires a newer Tunlease client",
		},
		{
			code:   "gateway_upgrade_required",
			detail: "client protocol 2 requires a newer Tunlease gateway",
		},
	} {
		t.Run(test.code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUpgradeRequired)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": test.code, "detail": test.detail,
				})
			}))
			defer server.Close()

			client, err := New(Config{Gateway: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Start(context.Background(), []string{"/callback"}, 8080)
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Code != test.code || apiErr.Detail != test.detail {
				t.Fatalf("Start error = %#v", err)
			}
		})
	}
}

func TestStartAcceptsLegacyV1GatewayWithoutProtocolHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerClaim, "legacy-claim")
		w.Header().Set(headerOwner, "legacy-owner")
		w.Header().Set(headerStarted, time.Now().UTC().Format(time.RFC3339Nano))
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		conn := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
		session, err := yamux.Server(conn, yamuxConfig())
		if err != nil {
			t.Errorf("start yamux: %v", err)
			return
		}
		defer func() { _ = session.Close() }()
		stream, err := session.Open()
		if err != nil {
			t.Errorf("open readiness stream: %v", err)
			return
		}
		defer func() { _ = stream.Close() }()
		if _, err = io.WriteString(stream, "ready"); err != nil {
			t.Errorf("write ready: %v", err)
			return
		}
		var acknowledgement [3]byte
		if _, err = io.ReadFull(stream, acknowledgement[:]); err != nil || string(acknowledgement[:]) != "ack" {
			t.Errorf("read acknowledgement: %q, %v", acknowledgement, err)
			return
		}
		if _, err = io.WriteString(stream, "ok"); err != nil {
			t.Errorf("write confirmation: %v", err)
			return
		}
		<-session.CloseChan()
	}))
	defer server.Close()

	client, err := New(Config{Gateway: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Start(context.Background(), []string{"/callback"}, 8080)
	if err != nil {
		t.Fatal(err)
	}
	if session.Claim().ID != "legacy-claim" {
		t.Fatalf("claim = %#v", session.Claim())
	}
	if err = session.Close(); err != nil {
		t.Fatal(err)
	}
}

// The control plane lives at a fixed path, so a gateway host must receive control
// requests under /_tunlease no matter how the client was configured.
func TestControlRequestsGoToTheFixedControlPath(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"claims":[]}`))
	}))
	defer server.Close()

	client, err := New(Config{Gateway: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := "/_tunlease/api/v1/claims"; got != want {
		t.Errorf("list requested %q, want %q", got, want)
	}
	if want := server.URL + "/_tunlease"; client.Gateway() != want {
		t.Errorf("Gateway() = %q, want %q", client.Gateway(), want)
	}
}

func TestWaitRetryStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	if waitRetry(ctx, time.Minute) {
		t.Fatal("canceled retry reported ready")
	}
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("canceled retry did not stop promptly")
	}
}

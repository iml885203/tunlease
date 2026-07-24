package tunnelclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewValidatesConfig(t *testing.T) {
	for _, cfg := range []Config{{}, {Gateway: "ftp://example.test", Token: "x"}} {
		if _, err := New(cfg); err == nil {
			t.Fatalf("New(%+v) succeeded", cfg)
		}
	}
	if _, err := New(Config{Gateway: "https://example.test/base"}); err != nil {
		t.Fatal(err)
	}
}

func TestClientOmitsAuthorizationWithoutToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"claims": []Claim{}})
	}))
	defer srv.Close()
	c, err := New(Config{Gateway: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.List(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizePath(t *testing.T) {
	for input, want := range map[string]string{
		"/callback":   "/callback/*",
		"/callback/":  "/callback/*",
		"/callback/*": "/callback/*",
	} {
		got, err := NormalizePath(input)
		if err != nil || got != want {
			t.Fatalf("NormalizePath(%q) = %q, %v", input, got, err)
		}
	}
	for _, input := range []string{"callback", "/", "/x/*/y"} {
		if _, err := NormalizePath(input); err == nil {
			t.Fatalf("NormalizePath(%q) succeeded", input)
		}
	}
}

func TestClientListAndRelease(t *testing.T) {
	var released string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/base/api/v1/claims":
			_ = json.NewEncoder(w).Encode(map[string]any{"claims": []Claim{{ID: "claim-1", Owner: "dev", Paths: []string{"/callback/*"}, ExpiresAt: time.Now()}}})
		case r.Method == http.MethodDelete && r.URL.Path == "/base/api/v1/claims/claim-1":
			released = "claim-1"
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := New(Config{Gateway: srv.URL + "/base", Token: "secret", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := c.List(context.Background())
	if err != nil || len(claims) != 1 || claims[0].ID != "claim-1" {
		t.Fatalf("List() = %#v, %v", claims, err)
	}
	if err := c.Release(context.Background(), "claim-1"); err != nil {
		t.Fatal(err)
	}
	if released != "claim-1" {
		t.Fatalf("released = %q", released)
	}
}

func TestStartNormalizesPathsAndReleasesWhenTunnelFails(t *testing.T) {
	var createdPath, released string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/claims":
			body, _ := io.ReadAll(r.Body)
			var in struct {
				Paths []string `json:"paths"`
			}
			_ = json.Unmarshal(body, &in)
			if len(in.Paths) == 1 {
				createdPath = in.Paths[0]
			}
			_ = json.NewEncoder(w).Encode(Claim{ID: "claim-1", Paths: in.Paths, RemotePort: 20000, Fingerprint: "invalid"})
		case r.Method == http.MethodGet && r.URL.Path == "/tunnel":
			http.Error(w, "not a websocket", http.StatusBadGateway)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/claims/claim-1":
			released = "claim-1"
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, _ := New(Config{Gateway: srv.URL, Token: "secret", HTTPClient: srv.Client()})
	if _, err := c.Start(context.Background(), []string{"/callback"}, 8080); err == nil {
		t.Fatal("Start succeeded without a WebSocket tunnel")
	}
	if createdPath != "/callback/*" {
		t.Fatalf("created path = %q", createdPath)
	}
	if released != "claim-1" {
		t.Fatalf("failed tunnel left claim %q active", released)
	}
}

func TestAPIErrorIsTyped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "path_claimed", "claimed_by": "alice", "expires_at": time.Now()})
	}))
	defer srv.Close()
	c, _ := New(Config{Gateway: srv.URL, Token: "secret", HTTPClient: srv.Client()})
	_, err := c.List(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict || apiErr.Code != "path_claimed" {
		t.Fatalf("error = %#v", err)
	}
}

func TestGatewayBasePathURLs(t *testing.T) {
	for _, tt := range []struct{ base, api, tunnel string }{
		{"https://tunlease.example.com/tunlease", "https://tunlease.example.com/tunlease/api/v1/claims", "wss://tunlease.example.com/tunlease/tunnel"},
		{"http://localhost:8300", "http://localhost:8300/api/v1/claims", "ws://localhost:8300/tunnel"},
	} {
		got, err := joinURL(tt.base, "/api/v1/claims")
		if err != nil || got != tt.api {
			t.Fatalf("API got %q %v", got, err)
		}
		got, err = tunnelURL(tt.base)
		if err != nil || got != tt.tunnel {
			t.Fatalf("tunnel got %q %v", got, err)
		}
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

package tunnelclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pathsHeader = r.Header.Get(headerPaths)
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
}

func TestFixedControlURLs(t *testing.T) {
	base := "https://callbacks.example.com/_tunlease"
	api, err := joinURL(base, "/api/v1/claims")
	if err != nil || api != "https://callbacks.example.com/_tunlease/api/v1/claims" {
		t.Fatalf("API URL = %q, %v", api, err)
	}
	tunnel, err := tunnelURL(base)
	if err != nil || tunnel != "wss://callbacks.example.com/_tunlease/tunnel" {
		t.Fatalf("tunnel URL = %q, %v", tunnel, err)
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

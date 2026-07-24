package sidecar

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLongestPrefixAndFallback(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "app:"+r.URL.Path) }))
	defer app.Close()
	tunnel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-DevProxy-Claim") != "deep" {
			t.Errorf("missing claim header")
		}
		_, _ = io.WriteString(w, "tunnel")
	}))
	defer tunnel.Close()
	p, _ := New(Config{AppURL: app.URL, RoutesURL: "http://unused"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.table.Store(&routeTable{routes: []Route{{PathPrefix: "/a/b/*", TunnelAddr: strings.TrimPrefix(tunnel.URL, "http://"), ClaimID: "deep"}, {PathPrefix: "/a/*", TunnelAddr: "127.0.0.1:1", ClaimID: "shallow"}}, updatedAt: time.Now()})
	r := httptest.NewRequest("POST", "/a/b/c", strings.NewReader("x"))
	w := httptest.NewRecorder()
	p.Handler().ServeHTTP(w, r)
	if w.Body.String() != "tunnel" {
		t.Fatalf("got %q", w.Body.String())
	}
	p.table.Store(&routeTable{routes: []Route{{PathPrefix: "/a/*", TunnelAddr: "127.0.0.1:1", ClaimID: "dead"}}, updatedAt: time.Now()})
	r = httptest.NewRequest("POST", "/a/c", strings.NewReader("body"))
	w = httptest.NewRecorder()
	p.Handler().ServeHTTP(w, r)
	if w.Body.String() != "app:/a/c" {
		t.Fatalf("fallback got %q", w.Body.String())
	}
}

func TestStaleRoutesClear(t *testing.T) {
	p, _ := New(Config{AppURL: "http://127.0.0.1:1", RoutesURL: "http://127.0.0.1:1", MaxStale: time.Millisecond}, nil)
	p.table.Store(&routeTable{routes: []Route{{PathPrefix: "/a/*"}}, updatedAt: time.Now().Add(-time.Second)})
	p.fetch(context.Background())
	if _, ok := p.Match("/a/x"); ok {
		t.Fatal("stale route retained")
	}
}

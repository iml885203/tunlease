package tunnelclient_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/iml885203/tunlease/internal/gatewayd"
	"github.com/iml885203/tunlease/internal/registry"
	"github.com/iml885203/tunlease/pkg/tunnelclient"
)

func TestAnonymousSessionDataPathAndClose(t *testing.T) {
	// Local HTTP server the claimed path should reach through the tunnel.
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "received: %s", r.URL.Path)
	}))
	defer local.Close()
	localPort := mustPort(t, local.URL)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := registry.NewMemory(64, []string{"/test/"}, time.Minute, nil, logger)
	tunnel, err := gatewayd.NewTunnel(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := &gatewayd.Server{
		Store:             store,
		Tokens:            nil,
		TTL:               time.Minute,
		Heartbeat:         time.Second,
		Tunnel:            tunnel,
		TunnelFingerprint: tunnel.Fingerprint(),
		OnChange:          tunnel.Sync,
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	client, err := tunnelclient.New(tunnelclient.Config{Gateway: httpServer.URL, HTTPClient: httpServer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	session, err := client.Start(ctx, []string{"/test/anonymous/*"}, localPort)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if got := session.Claim().Owner; got != "anonymous" {
		t.Fatalf("owner = %q", got)
	}

	// Third-party traffic enters as HTTP on the gateway; the claimed path must
	// reach the local server through the tunnel.
	body := getThroughTunnel(t, httpServer, "/test/anonymous/hook")
	if body != "received: /test/anonymous/hook" {
		t.Fatalf("tunnelled body = %q", body)
	}

	cancel()
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if len(store.List()) != 0 {
		t.Fatalf("claim remains after Close: %#v", store.List())
	}
}

func getThroughTunnel(t *testing.T, gateway *httptest.Server, path string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := gateway.Client().Get(gateway.URL + path)
		if err == nil && resp.StatusCode == 200 {
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			return string(b)
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("path %s never reached the tunnel: err=%v", path, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func mustPort(t *testing.T, rawURL string) int {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(rawURL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSessionReclaimsExpiredLeaseWithoutEventConsumer(t *testing.T) {
	local, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = local.Close() }()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := registry.NewMemory(64, []string{"/test/"}, 100*time.Millisecond, nil, logger)
	tunnel, err := gatewayd.NewTunnel(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := &gatewayd.Server{
		Store:             store,
		TTL:               100 * time.Millisecond,
		Heartbeat:         time.Second,
		Tunnel:            tunnel,
		TunnelFingerprint: tunnel.Fingerprint(),
		OnChange:          tunnel.Sync,
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	client, _ := tunnelclient.New(tunnelclient.Config{Gateway: httpServer.URL, HTTPClient: httpServer.Client()})
	ctx, cancel := context.WithCancel(context.Background())
	session, err := client.Start(ctx, []string{"/test/reclaim"}, local.Addr().(*net.TCPAddr).Port)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	initialID := session.Claim().ID
	deadline := time.Now().Add(4 * time.Second)
	for session.Claim().ID == initialID && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if current := session.Claim().ID; current == initialID {
		cancel()
		_ = session.Close()
		t.Fatalf("claim was not renewed: %s", current)
	}
	cancel()
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}

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
}

func newTestGateway(t *testing.T, allowed []string) *testGateway {
	return newTestGatewayOptions(t, registry.Options{MaxClaims: 64, Allowed: allowed})
}

func newTestGatewayOptions(t *testing.T, options registry.Options) *testGateway {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := registry.NewMemory(options, logger)
	tunnel := gatewayd.NewTunnel(store, nil)
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "origin: %s", r.URL.Path)
	})
	server := httptest.NewServer((&gatewayd.Server{
		Store: store, Tunnel: tunnel, FailOpen: origin,
	}).Handler())
	t.Cleanup(server.Close)
	return &testGateway{http: server, store: store, tunnel: tunnel}
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
	if body := get(t, gateway.http, "/test/anonymous/hook"); body != "local: /test/anonymous/hook" {
		t.Fatalf("tunnelled body = %q", body)
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
	session, err := client.Start(context.Background(), []string{"/test/unavailable"}, port)
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

	select {
	case event := <-session.Events():
		if event.Type != tunnelclient.EventLocalTargetError {
			t.Fatalf("event type = %q", event.Type)
		}
		if event.Err == nil || !strings.Contains(event.Err.Error(), "127.0.0.1") {
			t.Fatalf("event error = %v", event.Err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("local target failure event was not emitted")
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
	session, err := client.Start(context.Background(), []string{"/test/reconnect"}, mustPort(t, local.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	initial := session.Claim().ID
	gateway.tunnel.CloseClaim(initial)
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

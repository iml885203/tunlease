package gatewayd

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/iml885203/tunlease/internal/registry"
)

func TestTunnelAuthenticationModes(t *testing.T) {
	request := httptest.NewRequest("GET", "/tunnel", nil)
	principal, ok := authenticate(nil, false, request)
	if !ok || principal.Owner != "anonymous" {
		t.Fatalf("anonymous principal = %#v, %v", principal, ok)
	}

	tokens := map[string]Token{"secret": {Owner: "alice"}}
	if _, ok := authenticate(tokens, false, request); ok {
		t.Fatal("authenticated mode accepted a missing token")
	}
	request.Header.Set("Authorization", "Bearer secret")
	principal, ok = authenticate(tokens, false, request)
	if !ok || principal.Owner != "alice" {
		t.Fatalf("token principal = %#v, %v", principal, ok)
	}

	request.Header.Set("Authorization", "Bearer generated-client-secret")
	first, ok := authenticate(nil, true, request)
	if !ok || first.Owner == "" || first.Owner == "anonymous" {
		t.Fatalf("dynamic principal = %#v, %v", first, ok)
	}
	second, ok := authenticate(nil, true, request)
	if !ok || second.Owner != first.Owner {
		t.Fatalf("dynamic principal changed: %#v != %#v", first, second)
	}
	request.Header.Del("Authorization")
	if _, ok := authenticate(nil, true, request); ok {
		t.Fatal("dynamic identity accepted a missing bearer secret")
	}
}

func TestIdleConnExtendsDeadlineWhenDataKeepsMoving(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	go func() {
		for _, value := range []byte("abc") {
			time.Sleep(30 * time.Millisecond)
			_, _ = server.Write([]byte{value})
		}
	}()

	connection := &idleConn{Conn: client, timeout: 50 * time.Millisecond}
	var received [3]byte
	if _, err := io.ReadFull(connection, received[:]); err != nil {
		t.Fatal(err)
	}
	if string(received[:]) != "abc" {
		t.Fatalf("received %q", received)
	}
}

func TestIncompleteTunnelHandshakeReleasesClaim(t *testing.T) {
	store := registry.NewMemory(registry.Options{MaxClaims: 64}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tunnel := NewTunnel(store, nil)
	tunnel.setup = 50 * time.Millisecond
	origin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "origin")
	})
	server := httptest.NewServer((&Server{Store: store, Tunnel: tunnel, FailOpen: origin}).Handler())
	defer server.Close()

	headers := http.Header{}
	headers.Set(headerPaths, `["/callback/*"]`)
	headers.Set(headerLocal, "localhost:8080")
	ws, response, err := websocket.Dial(context.Background(), "ws"+server.URL[len("http"):]+ControlPrefix+"/tunnel", &websocket.DialOptions{
		HTTPHeader: headers,
	})
	if response != nil && response.Body != nil {
		defer func() { _ = response.Body.Close() }()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ws.CloseNow() }()

	response, err = server.Client().Get(server.URL + "/callback/request-during-setup")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(body) != "origin" {
		t.Fatalf("request during setup = %q, %v", body, err)
	}

	deadline := time.Now().Add(time.Second)
	for len(store.List()) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if claims := store.List(); len(claims) != 0 {
		t.Fatalf("incomplete handshake retained claim: %#v", claims)
	}
}

func TestMatchClaimDistinguishesExactAndWildcardPaths(t *testing.T) {
	store := registry.NewMemory(registry.Options{MaxClaims: 64}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	exact, err := store.Create("alice", []string{"/callback"}, "")
	if err != nil {
		t.Fatal(err)
	}
	wildcard, err := store.Create("bob", []string{"/events/*"}, "")
	if err != nil {
		t.Fatal(err)
	}
	recursive, err := store.Create("carol", []string{"/tree/**"}, "")
	if err != nil {
		t.Fatal(err)
	}
	tunnel := NewTunnel(store, nil)

	for _, path := range []string{"/callback", "/callback/"} {
		if got, ok := tunnel.matchClaim(path); !ok || got != exact.ID {
			t.Errorf("matchClaim(%q) = %q, %v", path, got, ok)
		}
	}
	if got, ok := tunnel.matchClaim("/callback/child"); ok {
		t.Errorf("exact claim matched descendant as %q", got)
	}
	for _, path := range []string{"/events/one", "/events/two/"} {
		if got, ok := tunnel.matchClaim(path); !ok || got != wildcard.ID {
			t.Errorf("matchClaim(%q) = %q, %v", path, got, ok)
		}
	}
	for _, path := range []string{"/events", "/events/", "/events/one/two"} {
		if got, ok := tunnel.matchClaim(path); ok {
			t.Errorf("single-level wildcard matched %q as %q", path, got)
		}
	}
	for _, path := range []string{"/tree", "/tree/", "/tree/one", "/tree/one/two"} {
		if got, ok := tunnel.matchClaim(path); !ok || got != recursive.ID {
			t.Errorf("matchClaim(%q) = %q, %v", path, got, ok)
		}
	}
}

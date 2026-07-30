package gatewayd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/iml885203/tunlease/internal/registry"
)

func TestTunnelProtocolCompatibility(t *testing.T) {
	for _, test := range []struct {
		name            string
		clientProtocol  string
		gatewayProtocol int
		compatible      bool
		status          int
		code            string
	}{
		{name: "legacy v1 client", gatewayProtocol: 1, compatible: true},
		{name: "same major", clientProtocol: "1", gatewayProtocol: 1, compatible: true},
		{
			name: "client upgrade required", clientProtocol: "1", gatewayProtocol: 2,
			status: http.StatusUpgradeRequired, code: "client_upgrade_required",
		},
		{
			name: "gateway upgrade required", clientProtocol: "2", gatewayProtocol: 1,
			status: http.StatusUpgradeRequired, code: "gateway_upgrade_required",
		},
		{
			name: "invalid protocol", clientProtocol: "future", gatewayProtocol: 1,
			status: http.StatusBadRequest, code: "invalid_request",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := registry.NewMemory(registry.Options{MaxClaims: 64}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			tunnel := NewTunnel(store, nil)
			tunnel.protocol = test.gatewayProtocol

			request := httptest.NewRequest(http.MethodGet, ControlPrefix+"/tunnel", nil)
			if test.clientProtocol != "" {
				request.Header.Set(headerProtocol, test.clientProtocol)
			}
			request.Header.Set(headerPaths, `["/callback"]`)
			request.Header.Set(headerLocal, "localhost:8080")
			recorder := httptest.NewRecorder()
			tunnel.ServeHTTP(recorder, request)

			var body struct {
				Code string `json:"error"`
			}
			_ = json.NewDecoder(recorder.Body).Decode(&body)

			if test.compatible {
				// Negotiation passed, so the gateway advertises its version and the
				// handshake continues to the WebSocket upgrade, which httptest cannot
				// perform. Only the negotiation error codes prove a rejection.
				if got := recorder.Header().Get(headerProtocol); got != "1" {
					t.Errorf("gateway protocol header = %q, want 1", got)
				}
				if body.Code != "" {
					t.Fatalf("compatible protocol was rejected as %q", body.Code)
				}
				return
			}
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d", recorder.Code, test.status)
			}
			if body.Code != test.code {
				t.Fatalf("error code = %q, want %q", body.Code, test.code)
			}
		})
	}
}

// Who a claim belongs to is decided during the handshake, so each authentication
// mode is asserted by the claim the gateway records for a connecting client.
func TestTunnelAuthenticationModes(t *testing.T) {
	for _, tt := range []struct {
		name      string
		tokens    map[string]Token
		dynamic   bool
		authptr   string
		wantOwner string
		wantCode  int
	}{
		{name: "anonymous when no tokens are configured", wantOwner: "anonymous"},
		{
			name:   "a configured token names its owner",
			tokens: map[string]Token{"secret": {Owner: "alice"}}, authptr: "secret", wantOwner: "alice",
		},
		{
			name:   "a missing token is rejected",
			tokens: map[string]Token{"secret": {Owner: "alice"}}, wantCode: http.StatusUnauthorized,
		},
		{
			name:   "an unknown token is rejected",
			tokens: map[string]Token{"secret": {Owner: "alice"}}, authptr: "wrong", wantCode: http.StatusUnauthorized,
		},
		{name: "a dynamic identity derives an owner", dynamic: true, authptr: "generated-client-secret"},
		{name: "a dynamic identity requires a secret", dynamic: true, wantCode: http.StatusUnauthorized},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := registry.NewMemory(registry.Options{MaxClaims: 64}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			tunnel := NewTunnel(store, tt.tokens)
			tunnel.DynamicClientIdentity = tt.dynamic

			request := httptest.NewRequest(http.MethodGet, ControlPrefix+"/tunnel", nil)
			if tt.authptr != "" {
				request.Header.Set("Authorization", "Bearer "+tt.authptr)
			}
			request.Header.Set(headerPaths, `["/callback"]`)
			request.Header.Set(headerLocal, "localhost:8080")
			if tt.wantCode != 0 {
				recorder := httptest.NewRecorder()
				tunnel.ServeHTTP(recorder, request)
				if recorder.Code != tt.wantCode {
					t.Fatalf("status = %d, want %d (%q)", recorder.Code, tt.wantCode, recorder.Body)
				}
				if claims := store.List(); len(claims) != 0 {
					t.Errorf("rejected handshake recorded a claim: %#v", claims)
				}
				return
			}

			// The claim is recorded once the tunnel is established, so an accepted
			// handshake needs a real WebSocket to reach it.
			claims := connectTunnel(t, tunnel, store, request.Header)
			if len(claims) != 1 {
				t.Fatalf("claims = %#v", claims)
			}
			switch {
			case tt.wantOwner != "":
				if claims[0].Owner != tt.wantOwner {
					t.Errorf("owner = %q, want %q", claims[0].Owner, tt.wantOwner)
				}
			default:
				if claims[0].Owner == "" || claims[0].Owner == "anonymous" {
					t.Errorf("dynamic identity produced owner %q", claims[0].Owner)
				}
			}
		})
	}
}

// connectTunnel completes a tunnel handshake against tunnel and returns the
// claims the gateway recorded for it.
func connectTunnel(t *testing.T, tunnel *Tunnel, store *registry.Memory, headers http.Header) []registry.Claim {
	t.Helper()
	server := httptest.NewServer((&Server{
		Store: store, Tunnel: tunnel, Tokens: tunnel.tokens,
		FailOpen: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
	}).Handler())
	defer server.Close()

	ws, response, err := websocket.Dial(context.Background(),
		"ws"+strings.TrimPrefix(server.URL, "http")+ControlPrefix+"/tunnel",
		&websocket.DialOptions{HTTPHeader: headers})
	if response != nil && response.Body != nil {
		defer func() { _ = response.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("tunnel handshake: %v", err)
	}
	defer func() { _ = ws.CloseNow() }()

	deadline := time.Now().Add(2 * time.Second)
	for len(store.List()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	return store.List()
}

// A dynamic identity has to name the same owner every time a client reconnects
// with its stored secret, otherwise release and per-owner limits would not follow
// the client across sessions.
func TestDynamicIdentityIsStableForTheSameSecret(t *testing.T) {
	owners := make([]string, 0, 2)
	for _, path := range []string{`["/first"]`, `["/second"]`} {
		store := registry.NewMemory(registry.Options{MaxClaims: 64}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		tunnel := NewTunnel(store, nil)
		tunnel.DynamicClientIdentity = true

		headers := http.Header{}
		headers.Set("Authorization", "Bearer generated-client-secret")
		headers.Set(headerPaths, path)
		headers.Set(headerLocal, "localhost:8080")

		claims := connectTunnel(t, tunnel, store, headers)
		if len(claims) != 1 {
			t.Fatalf("claims for %s = %#v", path, claims)
		}
		owners = append(owners, claims[0].Owner)
	}
	if owners[0] != owners[1] {
		t.Errorf("same secret produced owners %q and %q", owners[0], owners[1])
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

func TestTunnelHandshakeValidatesAuthenticationAndHeaders(t *testing.T) {
	store := registry.NewMemory(registry.Options{MaxClaims: 64}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tunnel := NewTunnel(store, map[string]Token{"secret": {Owner: "alice"}})

	for _, test := range []struct {
		name  string
		token string
		paths string
		local string
		code  int
	}{
		{name: "authentication", paths: `["/callback"]`, local: "localhost:8080", code: http.StatusUnauthorized},
		{name: "paths", token: "secret", paths: `not-json`, local: "localhost:8080", code: http.StatusBadRequest},
		{name: "local target", token: "secret", paths: `["/callback"]`, code: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, ControlPrefix+"/tunnel", nil)
			if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			request.Header.Set(headerPaths, test.paths)
			request.Header.Set(headerLocal, test.local)
			recorder := httptest.NewRecorder()
			tunnel.ServeHTTP(recorder, request)
			if recorder.Code != test.code {
				t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body)
			}
		})
	}
}

func TestTunnelHandshakeReportsClaimPolicyErrors(t *testing.T) {
	for _, test := range []struct {
		name     string
		options  registry.Options
		existing []string
		paths    string
		code     int
	}{
		{
			name:    "invalid path",
			options: registry.Options{MaxClaims: 64},
			paths:   `["callback"]`,
			code:    http.StatusBadRequest,
		},
		{
			name:    "outside allowlist",
			options: registry.Options{MaxClaims: 64, Allowed: []string{"/allowed/"}},
			paths:   `["/other/callback"]`,
			code:    http.StatusForbidden,
		},
		{
			name:     "overlapping claim",
			options:  registry.Options{MaxClaims: 64},
			existing: []string{"/callback/**"},
			paths:    `["/callback/*"]`,
			code:     http.StatusConflict,
		},
		{
			name:     "global claim limit",
			options:  registry.Options{MaxClaims: 1},
			existing: []string{"/first"},
			paths:    `["/second"]`,
			code:     http.StatusServiceUnavailable,
		},
		{
			name:     "owner claim limit",
			options:  registry.Options{MaxClaims: 64, MaxClaimsPerOwner: 1},
			existing: []string{"/first"},
			paths:    `["/second"]`,
			code:     http.StatusTooManyRequests,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := registry.NewMemory(test.options, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if len(test.existing) > 0 {
				if _, err := store.Create("anonymous", test.existing, "localhost:8080"); err != nil {
					t.Fatal(err)
				}
			}
			tunnel := NewTunnel(store, nil)
			request := httptest.NewRequest(http.MethodGet, ControlPrefix+"/tunnel", nil)
			request.Header.Set(headerPaths, test.paths)
			request.Header.Set(headerLocal, "localhost:8080")
			recorder := httptest.NewRecorder()
			tunnel.ServeHTTP(recorder, request)
			if recorder.Code != test.code {
				t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body)
			}
		})
	}
}

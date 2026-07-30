package gatewayd

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/iml885203/tunlease/internal/registry"
)

func testServer(tokens map[string]Token) (*Server, *registry.Memory) {
	store := registry.NewMemory(registry.Options{MaxClaims: 64, Allowed: []string{"/ok/"}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tunnel := NewTunnel(store, tokens)
	origin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	return &Server{Store: store, Tokens: tokens, Tunnel: tunnel, FailOpen: origin}, store
}

func request(server *Server, method, path, token string) (int, map[string]any) {
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	var body map[string]any
	if recorder.Body.Len() > 0 {
		_ = json.Unmarshal(recorder.Body.Bytes(), &body)
	}
	return recorder.Code, body
}

func TestControlPlaneIsFixedAndClaimsAreTunnelOwned(t *testing.T) {
	server, store := testServer(nil)
	claim, err := store.Create("anonymous", []string{"/ok/a/*"}, "localhost:1")
	if err != nil {
		t.Fatal(err)
	}

	code, body := request(server, http.MethodGet, ControlPrefix+"/api/v1/claims", "")
	claims, _ := body["claims"].([]any)
	if code != http.StatusOK || len(claims) != 1 {
		t.Fatalf("list: code=%d body=%v", code, body)
	}
	if code, _ = request(server, http.MethodPost, ControlPrefix+"/api/v1/claims", ""); code != http.StatusMethodNotAllowed {
		t.Fatalf("legacy claim endpoint code=%d", code)
	}
	if code, _ = request(server, http.MethodDelete, ControlPrefix+"/api/v1/claims/"+claim.ID, ""); code != http.StatusNotFound {
		t.Fatalf("claim without live session release code=%d", code)
	}
}

func TestAuthentication(t *testing.T) {
	tokens := map[string]Token{"a": {Owner: "alice"}, "b": {Owner: "bob"}}
	server, store := testServer(tokens)
	claim, err := store.Create("alice", []string{"/ok/a/*"}, "localhost:1")
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := request(server, http.MethodGet, ControlPrefix+"/api/v1/claims", ""); code != http.StatusUnauthorized {
		t.Fatalf("missing token code=%d", code)
	}
	if code, _ := request(server, http.MethodDelete, ControlPrefix+"/api/v1/claims/"+claim.ID, "b"); code != http.StatusUnauthorized {
		t.Fatalf("other owner release code=%d", code)
	}
	if code, _ := request(server, http.MethodDelete, ControlPrefix+"/api/v1/claims/"+claim.ID, "a"); code != http.StatusNotFound {
		t.Fatalf("owner release without session code=%d", code)
	}
}

func TestMissingReleaseDoesNotCreateRevocation(t *testing.T) {
	server, _ := testServer(map[string]Token{"a": {Owner: "alice"}})
	if code, _ := request(server, http.MethodDelete, ControlPrefix+"/api/v1/claims/missing", "a"); code != http.StatusNotFound {
		t.Fatalf("missing release code=%d", code)
	}
}

func TestUnclaimedTrafficUsesOrigin(t *testing.T) {
	server, _ := testServer(nil)
	if code, _ := request(server, http.MethodGet, "/webhooks/unclaimed", ""); code != http.StatusNoContent {
		t.Fatalf("origin code=%d", code)
	}
}

func TestClaimListCanBeDisabledWithoutDisablingRelease(t *testing.T) {
	server, store := testServer(nil)
	server.DisableClaimList = true
	claim, err := store.Create("anonymous", []string{"/ok/a/*"}, "localhost:1")
	if err != nil {
		t.Fatal(err)
	}

	if code, _ := request(server, http.MethodGet, ControlPrefix+"/api/v1/claims", ""); code != http.StatusNotFound {
		t.Fatalf("list code=%d", code)
	}
	if code, _ := request(server, http.MethodDelete, ControlPrefix+"/api/v1/claims/"+claim.ID, ""); code != http.StatusNotFound {
		t.Fatalf("release without live session code=%d", code)
	}
}

// With dynamic identities each client secret is its own owner, so listing shows a
// client only what it claimed. Both claims are established through the tunnel
// handshake so the owners are the ones the gateway itself derived.
func TestDynamicClientIdentityListsOnlyItsClaims(t *testing.T) {
	server, store := testServer(nil)
	server.DynamicClientIdentity = true
	server.Tunnel.DynamicClientIdentity = true

	for secret, path := range map[string]string{
		"alice-generated-secret": `["/ok/alice/*"]`,
		"bob-generated-secret":   `["/ok/bob/*"]`,
	} {
		headers := http.Header{}
		headers.Set("Authorization", "Bearer "+secret)
		headers.Set(headerPaths, path)
		headers.Set(headerLocal, "localhost:1")
		if claims := connectTunnel(t, server.Tunnel, store, headers); len(claims) == 0 {
			t.Fatalf("handshake for %s recorded no claim", secret)
		}
	}
	if claims := store.List(); len(claims) != 2 {
		t.Fatalf("seeded claims = %#v", claims)
	}

	code, body := request(server, http.MethodGet, ControlPrefix+"/api/v1/claims", "alice-generated-secret")
	claims, _ := body["claims"].([]any)
	if code != http.StatusOK || len(claims) != 1 {
		t.Fatalf("alice list: code=%d body=%v", code, body)
	}
	claim, _ := claims[0].(map[string]any)
	if paths, _ := claim["paths"].([]any); len(paths) != 1 || paths[0] != "/ok/alice/*" {
		t.Errorf("alice saw paths %#v, want [/ok/alice/*]", claim["paths"])
	}
}

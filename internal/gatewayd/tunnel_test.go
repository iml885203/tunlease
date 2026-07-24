package gatewayd

import (
	"net/http/httptest"
	"testing"
)

func TestTunnelAuthenticationModes(t *testing.T) {
	request := httptest.NewRequest("GET", "/tunnel", nil)
	principal, ok := authenticate(nil, request)
	if !ok || principal.Owner != "anonymous" {
		t.Fatalf("anonymous principal = %#v, %v", principal, ok)
	}

	tokens := map[string]Token{"secret": {Owner: "alice"}}
	if _, ok := authenticate(tokens, request); ok {
		t.Fatal("authenticated mode accepted a missing token")
	}
	request.Header.Set("Authorization", "Bearer secret")
	principal, ok = authenticate(tokens, request)
	if !ok || principal.Owner != "alice" {
		t.Fatalf("token principal = %#v, %v", principal, ok)
	}
}

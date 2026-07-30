package cliapp

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/iml885203/tunlease/pkg/tunnelclient"
)

// Release is scoped by what the command names: releasing by port must not touch
// another port or another gateway, and every local record of a released path is
// discarded even when a reclaim left more than one behind.
func TestReleaseByPortIsScopedToThatGatewayAndPort(t *testing.T) {
	const named = "https://named"
	for _, tt := range []struct {
		name    string
		claims  []stateClaim
		wantIDs []string
	}{
		{
			name: "leaves other ports and gateways in place",
			claims: []stateClaim{
				{ClaimID: "match", Gateway: named, Paths: []string{"/a/*"}, To: 8080},
				{ClaimID: "other-port", Gateway: named, Paths: []string{"/a/*"}, To: 9090},
				{ClaimID: "other-gateway", Gateway: "https://other", Paths: []string{"/a/*"}, To: 8080},
			},
			wantIDs: []string{"other-port", "other-gateway"},
		},
		{
			name: "discards every record left by a reclaimed path",
			claims: []stateClaim{
				{ClaimID: "old", Gateway: named, Paths: []string{"/a/*"}, To: 8080},
				{ClaimID: "new", Gateway: named, Paths: []string{"/a/*"}, To: 8080},
			},
			wantIDs: nil,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// The gateway reports every claim as already gone, so the command
			// reaches its state cleanup for each claim it was asked to release.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"claim_not_found","detail":"claim no longer exists"}`))
			}))
			defer server.Close()
			client, err := tunnelclient.New(tunnelclient.Config{Gateway: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}

			t.Setenv("TUNLEASE_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))
			claims := make([]stateClaim, len(tt.claims))
			for i, claim := range tt.claims {
				claims[i] = claim
				if claim.Gateway == named {
					claims[i].Gateway = client.Gateway()
				}
			}
			saveState(state{Claims: claims})

			var out, stderr bytes.Buffer
			if err = runRelease(newConsole(&out, &stderr), context.Background(), client, nil, 8080); err != nil {
				t.Fatalf("runRelease() = %v", err)
			}

			remaining := loadState().Claims
			got := make([]string, 0, len(remaining))
			for _, claim := range remaining {
				got = append(got, claim.ClaimID)
			}
			if strings.Join(got, ",") != strings.Join(tt.wantIDs, ",") {
				t.Errorf("remaining claims = %v, want %v", got, tt.wantIDs)
			}
		})
	}
}

// Without a configured token the CLI authenticates with a generated identity.
// The gateway sees it as a bearer token, so that is where stability per gateway
// and separation between gateways are observable.
func TestGeneratedIdentityIsStablePerGateway(t *testing.T) {
	var mu sync.Mutex
	seen := map[string][]string{}
	record := func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.Host] = append(seen[r.Host], r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"claims":[]}`))
	}
	one := httptest.NewServer(http.HandlerFunc(record))
	defer one.Close()
	two := httptest.NewServer(http.HandlerFunc(record))
	defer two.Close()

	t.Setenv("TUNLEASE_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))
	t.Setenv("TUNLEASE_DEFAULT_SCHEME", "http")
	for _, gateway := range []string{one.URL, two.URL, one.URL} {
		command := NewCommandWithVersion("test", "now")
		command.SetOut(&bytes.Buffer{})
		command.SetErr(&bytes.Buffer{})
		command.SetArgs([]string{"list", "--gateway", strings.TrimPrefix(gateway, "http://")})
		if err := command.Execute(); err != nil {
			t.Fatalf("list against %s: %v", gateway, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	first, second := seen[hostOf(one.URL)], seen[hostOf(two.URL)]
	if len(first) != 2 || len(second) != 1 {
		t.Fatalf("requests seen = %#v", seen)
	}
	if first[0] == "" || first[0] == "Bearer " {
		t.Fatalf("no identity sent to the first gateway: %q", first[0])
	}
	if first[0] != first[1] {
		t.Errorf("identity changed between calls to the same gateway: %q then %q", first[0], first[1])
	}
	if first[0] == second[0] {
		t.Errorf("both gateways received the same identity: %q", first[0])
	}
}

func hostOf(rawURL string) string {
	return strings.TrimPrefix(rawURL, "http://")
}

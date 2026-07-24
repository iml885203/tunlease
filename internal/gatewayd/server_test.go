package gatewayd

import (
	"bytes"
	"encoding/json"
	"github.com/iml885203/tunlease/internal/registry"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (f *fakeClock) Now() time.Time { return f.now }
func testServer(clock registry.Clock) *Server {
	m := registry.NewMemory(64, []string{"/ok/"}, time.Minute, clock, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return &Server{Store: m, Tokens: map[string]Token{"a": {Owner: "alice"}, "b": {Owner: "bob"}}, TTL: time.Minute, Heartbeat: time.Second}
}
func request(s *Server, method, path, token string, body any) (int, map[string]any) {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var v map[string]any
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &v)
	}
	return w.Code, v
}
func post(s *Server, token, path string) (int, map[string]any) {
	return request(s, "POST", "/api/v1/claims", token, map[string]any{"paths": []string{path}, "local": "localhost:1"})
}
func TestAuthenticationIsOptionalWhenNoTokensConfigured(t *testing.T) {
	s := testServer(nil)
	s.Tokens = nil
	code, claim := post(s, "", "/ok/anonymous/*")
	if code != http.StatusCreated || claim["owner"] != "anonymous" {
		t.Fatalf("claim: code=%d body=%v", code, claim)
	}
	id := claim["claim_id"].(string)
	if code, _ := request(s, http.MethodPost, "/api/v1/claims/"+id+"/heartbeat", "", nil); code != http.StatusOK {
		t.Fatalf("heartbeat code=%d", code)
	}
	if code, _ := request(s, http.MethodDelete, "/api/v1/claims/"+id, "", nil); code != http.StatusNoContent {
		t.Fatalf("release code=%d", code)
	}
}

func TestAuthenticationIsRequiredWhenTokensConfigured(t *testing.T) {
	s := testServer(nil)
	if code, _ := post(s, "", "/ok/a/*"); code != http.StatusUnauthorized {
		t.Fatalf("missing token code=%d", code)
	}
	if code, _ := post(s, "wrong", "/ok/a/*"); code != http.StatusUnauthorized {
		t.Fatalf("invalid token code=%d", code)
	}
}
func TestConflictExistingContainsNew(t *testing.T) {
	s := testServer(nil)
	if code, _ := post(s, "a", "/ok/a/*"); code != 201 {
		t.Fatal(code)
	}
	code, v := post(s, "b", "/ok/a/b/*")
	assertConflict(t, code, v)
}
func TestConflictNewContainsExisting(t *testing.T) {
	s := testServer(nil)
	if code, _ := post(s, "a", "/ok/a/b/*"); code != 201 {
		t.Fatal(code)
	}
	code, v := post(s, "b", "/ok/a/*")
	assertConflict(t, code, v)
}
func assertConflict(t *testing.T, code int, v map[string]any) {
	t.Helper()
	if code != 409 || v["error"] != "path_claimed" || v["claimed_by"] != "alice" || v["expires_at"] == nil {
		t.Fatalf("code=%d body=%v", code, v)
	}
}
func TestPathValidation(t *testing.T) {
	for _, tt := range []struct {
		path string
		want int
	}{{"/ok/a/*", 201}, {"ok/a/*", 400}, {"/ok/a", 400}, {"/ok/*/a/*", 400}, {"/*", 400}} {
		t.Run(tt.path, func(t *testing.T) {
			code, v := post(testServer(nil), "a", tt.path)
			if code != tt.want {
				t.Fatalf("code=%d body=%v", code, v)
			}
			if code == 400 && v["error"] != "invalid_request" {
				t.Fatal(v)
			}
		})
	}
}
func TestHeartbeatExpiredAndMissing(t *testing.T) {
	f := &fakeClock{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := testServer(f)
	_, v := post(s, "a", "/ok/a/*")
	id := v["claim_id"].(string)
	f.now = f.now.Add(time.Minute)
	for _, id := range []string{id, "missing"} {
		code, v := request(s, http.MethodPost, "/api/v1/claims/"+id+"/heartbeat", "a", nil)
		if code != 404 || v["error"] != "claim_expired" {
			t.Fatalf("%s: %d %v", id, code, v)
		}
	}
}

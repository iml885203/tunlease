package registry

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time { return f.t }
func memory(f *fakeClock) *Memory {
	return NewMemory(2, []string{"/webhooks/"}, time.Minute, f, slog.New(slog.NewTextHandler(io.Discard, nil)))
}
func TestTTLExpiryFreesClaimSlot(t *testing.T) {
	f := &fakeClock{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	m := memory(f)
	if _, e := m.Create("alice", []string{"/webhooks/a/*"}, ""); e != nil {
		t.Fatal(e)
	}
	f.t = f.t.Add(time.Minute)
	if len(m.List()) != 0 {
		t.Fatal("expired claim remains")
	}
	if _, e := m.Create("bob", []string{"/webhooks/b/*"}, ""); e != nil {
		t.Fatalf("slot not freed: %v", e)
	}
}
func TestClaimLimit(t *testing.T) {
	m := NewMemory(1, []string{"/"}, time.Minute, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, e := m.Create("alice", []string{"/a/*"}, ""); e != nil {
		t.Fatal(e)
	}
	var tmc *TooManyClaims
	if _, e := m.Create("bob", []string{"/b/*"}, ""); !errors.As(e, &tmc) {
		t.Fatalf("expected TooManyClaims, got %v", e)
	}
}
func TestHeartbeatExpiredAndMissing(t *testing.T) {
	f := &fakeClock{time.Now()}
	m := memory(f)
	a, _ := m.Create("alice", []string{"/webhooks/a/*"}, "")
	f.t = f.t.Add(time.Minute)
	if _, e := m.Heartbeat("alice", a.ID); !errors.Is(e, ErrNotFound) {
		t.Fatalf("expired heartbeat=%v", e)
	}
	if _, e := m.Heartbeat("alice", "missing"); !errors.Is(e, ErrNotFound) {
		t.Fatalf("missing heartbeat=%v", e)
	}
}
func TestAllowlist(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// A configured allowlist rejects paths outside its prefixes.
	restricted := NewMemory(64, []string{"/webhooks/"}, time.Minute, nil, logger)
	if _, e := restricted.Create("alice", []string{"/webhooks/a/*"}, ""); e != nil {
		t.Fatalf("allowed prefix rejected: %v", e)
	}
	var na *NotAllowed
	if _, e := restricted.Create("alice", []string{"/admin/*"}, ""); !errors.As(e, &na) {
		t.Fatalf("path outside allowlist should be rejected, got %v", e)
	}

	// An empty allowlist is opt-in: every valid path is allowed.
	open := NewMemory(64, nil, time.Minute, nil, logger)
	if _, e := open.Create("alice", []string{"/admin/*"}, ""); e != nil {
		t.Fatalf("empty allowlist should allow any path, got %v", e)
	}
}
func TestValidPath(t *testing.T) {
	for _, tt := range []struct {
		p    string
		want bool
	}{{"/a/*", true}, {"a/*", false}, {"/a", false}, {"/a/*/b/*", false}, {"/*", false}} {
		if got := ValidPath(tt.p); got != tt.want {
			t.Errorf("ValidPath(%q)=%v", tt.p, got)
		}
	}
}

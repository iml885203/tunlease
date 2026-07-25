package registry

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func testMemory(max int, allowed []string) *Memory {
	return NewMemory(Options{MaxClaims: max, Allowed: allowed}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestPerOwnerLimitDurationAndMinimumPathSegments(t *testing.T) {
	store := NewMemory(Options{
		MaxClaims:            4,
		MaxClaimsPerOwner:    1,
		MinClaimPathSegments: 2,
		MaxClaimDuration:     time.Minute,
		Allowed:              []string{"/demo/"},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	var notAllowed *NotAllowed
	if _, err := store.Create("alice", []string{"/demo/*"}, ""); !errors.As(err, &notAllowed) {
		t.Fatalf("shallow path error = %v", err)
	}
	claim, err := store.Create("alice", []string{"/demo/testing/*"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if claim.ExpiresAt == nil || claim.ExpiresAt.Sub(claim.Started) != time.Minute {
		t.Fatalf("claim expiration = %#v", claim.ExpiresAt)
	}
	var tooMany *TooManyOwnerClaims
	if _, err = store.Create("alice", []string{"/demo/other/*"}, ""); !errors.As(err, &tooMany) {
		t.Fatalf("per-owner limit error = %v", err)
	}
	if _, err = store.Create("bob", []string{"/demo/other/*"}, ""); err != nil {
		t.Fatalf("second owner rejected: %v", err)
	}
	if got := store.ListOwner("alice"); len(got) != 1 || got[0].ID != claim.ID {
		t.Fatalf("owner claims = %#v", got)
	}
}

func TestClaimLifecycle(t *testing.T) {
	store := testMemory(2, []string{"/webhooks/"})
	claim, err := store.Create("alice", []string{"/webhooks/a/*"}, "localhost:8080")
	if err != nil {
		t.Fatal(err)
	}
	if len(store.List()) != 1 {
		t.Fatal("active claim missing")
	}
	if err = store.Release("bob", claim.ID); err == nil {
		t.Fatal("another owner released the claim")
	}
	if err = store.Release("alice", claim.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.Release("alice", claim.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second release error = %v", err)
	}
	if len(store.List()) != 0 {
		t.Fatal("released claim remains")
	}
}

func TestConflictAndClaimLimit(t *testing.T) {
	store := testMemory(1, nil)
	if _, err := store.Create("alice", []string{"/a/*"}, ""); err != nil {
		t.Fatal(err)
	}
	var conflict *Conflict
	if _, err := store.Create("bob", []string{"/a/b"}, ""); !errors.As(err, &conflict) {
		t.Fatalf("expected Conflict, got %v", err)
	}
	var tooMany *TooManyClaims
	if _, err := store.Create("bob", []string{"/b/*"}, ""); !errors.As(err, &tooMany) {
		t.Fatalf("expected TooManyClaims, got %v", err)
	}
}

func TestAllowlist(t *testing.T) {
	restricted := testMemory(64, []string{"/webhooks/"})
	if _, err := restricted.Create("alice", []string{"/webhooks/a/*"}, ""); err != nil {
		t.Fatalf("allowed prefix rejected: %v", err)
	}
	var notAllowed *NotAllowed
	if _, err := restricted.Create("alice", []string{"/admin/*"}, ""); !errors.As(err, &notAllowed) {
		t.Fatalf("path outside allowlist should be rejected, got %v", err)
	}

	open := testMemory(64, nil)
	if _, err := open.Create("alice", []string{"/admin/*"}, ""); err != nil {
		t.Fatalf("empty allowlist should allow any path, got %v", err)
	}
}

func TestValidPath(t *testing.T) {
	for _, tt := range []struct {
		path string
		want bool
	}{
		{"/a/*", true},
		{"/a/**", true},
		{"/a", true},
		{"a/*", false},
		{"/a/", false},
		{"/a/*/b/*", false},
		{"/a/**/b", false},
		{"/*", false},
		{"/**", false},
		{"/" + strings.Repeat("a", MaxPathLength) + "/*", false},
	} {
		if got := ValidPath(tt.path); got != tt.want {
			t.Errorf("ValidPath(%q)=%v", tt.path, got)
		}
	}
}

func TestExactAndWildcardConflicts(t *testing.T) {
	store := testMemory(64, nil)
	if _, err := store.Create("alice", []string{"/a"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("bob", []string{"/a/b"}, ""); err != nil {
		t.Fatalf("sibling exact path should not conflict: %v", err)
	}
	var conflict *Conflict
	if _, err := store.Create("bob", []string{"/a/*"}, ""); !errors.As(err, &conflict) {
		t.Fatalf("wildcard should conflict with exact descendants, got %v", err)
	}
}

func TestSingleLevelAndRecursiveWildcardConflicts(t *testing.T) {
	store := testMemory(64, nil)
	if _, err := store.Create("alice", []string{"/a/*"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("bob", []string{"/a/b/c"}, ""); err != nil {
		t.Fatalf("single-level wildcard should not conflict with a grandchild: %v", err)
	}
	separateLevels := testMemory(64, nil)
	if _, err := separateLevels.Create("alice", []string{"/a/*"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := separateLevels.Create("bob", []string{"/a/b/*"}, ""); err != nil {
		t.Fatalf("single-level wildcards at different depths should not conflict: %v", err)
	}
	var conflict *Conflict
	if _, err := separateLevels.Create("bob", []string{"/a/b/**"}, ""); !errors.As(err, &conflict) {
		t.Fatalf("recursive subtree should conflict at its root, got %v", err)
	}

	recursive := testMemory(64, nil)
	if _, err := recursive.Create("alice", []string{"/tree/**"}, ""); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/tree", "/tree/child", "/tree/child/*", "/tree/child/**"} {
		if _, err := recursive.Create("bob", []string{path}, ""); !errors.As(err, &conflict) {
			t.Errorf("%s should overlap recursive claim, got %v", path, err)
		}
	}
}

func TestClaimRejectsTooManyPaths(t *testing.T) {
	paths := make([]string, MaxPathsPerClaim+1)
	for i := range paths {
		paths[i] = "/a/*"
	}
	if _, err := testMemory(64, nil).Create("alice", paths, ""); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("too many paths error = %v", err)
	}
}

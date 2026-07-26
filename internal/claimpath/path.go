// Package claimpath owns validation and matching rules for claimed callback
// paths. Both the public client and gateway registry use these rules so a path
// accepted before dialing cannot be rejected by the gateway for syntax.
package claimpath

import (
	"errors"
	"fmt"
	"strings"
)

const (
	MaxPathsPerClaim = 8
	MaxPathLength    = 512
)

type kind uint8

const (
	exact kind = iota
	singleLevel
	recursive
)

// Normalize validates a callback path and removes a trailing slash.
// A trailing /* matches one child segment; /** matches the whole subtree.
func Normalize(path string) (string, error) {
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "/") {
		return "", errors.New("path must start with /")
	}
	wildcard := ""
	switch {
	case strings.HasSuffix(path, "/**"):
		wildcard = "/**"
	case strings.HasSuffix(path, "/*"):
		wildcard = "/*"
	}
	base := strings.TrimSuffix(path, wildcard)
	base = strings.TrimRight(base, "/")
	if base == "" {
		return "", errors.New("root path is not allowed")
	}
	if strings.Contains(base, "*") {
		return "", errors.New("wildcard is only allowed as trailing /* or /**")
	}
	if len(base)+len(wildcard) > MaxPathLength {
		return "", fmt.Errorf("path must be at most %d bytes", MaxPathLength)
	}
	return base + wildcard, nil
}

// Valid reports whether path is already in normalized claim-path form.
func Valid(path string) bool {
	normalized, err := Normalize(path)
	return err == nil && normalized == path
}

// Base returns the non-wildcard portion of a normalized claim path.
func Base(path string) string {
	base, _ := split(path)
	return base
}

// Segments returns the number of path segments in a normalized claim path.
func Segments(path string) int {
	trimmed := strings.TrimPrefix(Base(path), "/")
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "/"))
}

// Match reports whether a request path belongs to a claim pattern and returns
// the pattern specificity for narrowest-match selection.
func Match(pattern, requestPath string) (specificity int, ok bool) {
	requestPath = strings.TrimRight(requestPath, "/")
	if requestPath == "" {
		requestPath = "/"
	}
	base := Base(pattern)
	return len(base), matches(pattern, requestPath)
}

// Overlap reports whether two normalized claim patterns can match any common
// request path.
func Overlap(a, b string) bool {
	aBase, aKind := split(a)
	bBase, bKind := split(b)
	if aKind == exact {
		return matches(b, aBase)
	}
	if bKind == exact {
		return matches(a, bBase)
	}
	if aKind == recursive && bKind == recursive {
		return matches(a, bBase) || matches(b, aBase)
	}
	if aKind == singleLevel && bKind == singleLevel {
		return aBase == bBase
	}
	if aKind == recursive {
		return matches(a, bBase) || matches(b, aBase)
	}
	return matches(b, aBase) || matches(a, bBase)
}

func split(path string) (string, kind) {
	if strings.HasSuffix(path, "/**") {
		return strings.TrimSuffix(path, "/**"), recursive
	}
	if strings.HasSuffix(path, "/*") {
		return strings.TrimSuffix(path, "/*"), singleLevel
	}
	return path, exact
}

func matches(pattern, path string) bool {
	base, pathKind := split(pattern)
	switch pathKind {
	case recursive:
		return path == base || strings.HasPrefix(path, base+"/")
	case singleLevel:
		if !strings.HasPrefix(path, base+"/") {
			return false
		}
		child := strings.TrimPrefix(path, base+"/")
		return child != "" && !strings.Contains(child, "/")
	default:
		return path == base
	}
}

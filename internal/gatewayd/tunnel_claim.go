package gatewayd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/iml885203/tunlease/internal/registry"
)

var errClaimOwner = errors.New("claim belongs to another owner")

func (t *Tunnel) writeClaimError(w http.ResponseWriter, err error) {
	var conflict *registry.Conflict
	var notAllowed *registry.NotAllowed
	var tooMany *registry.TooManyClaims
	var tooManyOwner *registry.TooManyOwnerClaims
	switch {
	case errors.Is(err, registry.ErrInvalidPath):
		writeTunnelError(w, http.StatusBadRequest, "invalid_request", "each path must start with /; wildcards are only allowed as trailing /* or /**", nil)
	case errors.As(err, &conflict):
		writeTunnelError(w, http.StatusConflict, "path_claimed", "path overlaps an active tunnel", map[string]any{"claimed_by": conflict.Owner})
	case errors.As(err, &notAllowed):
		writeTunnelError(w, http.StatusForbidden, "path_not_allowed", "path is outside the allowlist", nil)
	case errors.As(err, &tooMany):
		writeTunnelError(w, http.StatusServiceUnavailable, "claim_limit_reached", err.Error(), nil)
	case errors.As(err, &tooManyOwner):
		writeTunnelError(w, http.StatusTooManyRequests, "owner_claim_limit_reached", err.Error(), nil)
	default:
		writeTunnelError(w, http.StatusInternalServerError, "internal_error", "could not create tunnel", nil)
	}
}

func writeTunnelError(w http.ResponseWriter, status int, code, detail string, extra map[string]any) {
	body := map[string]any{"error": code, "detail": detail}
	for key, value := range extra {
		body[key] = value
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// CloseClaim terminates an active tunnel. Its handler releases the claim.
func (t *Tunnel) CloseClaim(id string) {
	t.mu.Lock()
	session := t.sessions[id]
	t.mu.Unlock()
	if session != nil {
		go func() { _ = session.Close() }()
	}
}

// ReleaseClaim tells the owning client that shutdown is terminal, waits for
// its acknowledgement, then closes the tunnel. No server-side tombstone is
// needed because a successful return means the client has stopped reconnecting.
func (t *Tunnel) ReleaseClaim(id, owner string) error {
	return t.terminateClaim(id, owner, streamRelease)
}

func (t *Tunnel) expireClaim(id, owner string) {
	claim, found := t.claim(id)
	if !found || claim.Owner != owner {
		return
	}
	t.mu.Lock()
	session := t.sessions[id]
	delete(t.sessions, id)
	t.mu.Unlock()
	_ = t.store.Release(owner, id)
	if session == nil || session.IsClosed() {
		return
	}
	defer func() { _ = session.Close() }()
	stream, err := session.Open()
	if err != nil {
		return
	}
	defer func() { _ = stream.Close() }()
	_ = stream.SetDeadline(time.Now().Add(t.setup))
	if _, err = stream.Write([]byte{streamExpire}); err != nil {
		return
	}
	var acknowledgement [1]byte
	_, _ = io.ReadFull(stream, acknowledgement[:])
}

func (t *Tunnel) terminateClaim(id, owner string, kind byte) error {
	claim, found := t.claim(id)
	if !found {
		return registry.ErrNotFound
	}
	if claim.Owner != owner {
		return errClaimOwner
	}
	t.mu.Lock()
	session := t.sessions[id]
	t.mu.Unlock()
	if session == nil || session.IsClosed() {
		return registry.ErrNotFound
	}
	stream, err := session.Open()
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()
	_ = stream.SetDeadline(time.Now().Add(t.setup))
	if _, err = stream.Write([]byte{kind}); err != nil {
		return err
	}
	var acknowledgement [1]byte
	if _, err = io.ReadFull(stream, acknowledgement[:]); err != nil {
		return err
	}
	if acknowledgement[0] != streamAck {
		return errors.New("invalid release acknowledgement")
	}
	go func() { _ = session.Close() }()
	return nil
}

func (t *Tunnel) claim(id string) (registry.Claim, bool) {
	for _, claim := range t.store.List() {
		if claim.ID == id {
			return claim, true
		}
	}
	return registry.Claim{}, false
}

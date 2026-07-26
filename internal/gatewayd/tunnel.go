package gatewayd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"github.com/iml885203/tunlease/internal/registry"
)

// Tunnel owns the complete lifecycle of each active claim. A WebSocket
// handshake claims its paths, and closing that connection releases them. Each
// HTTP request opens one yamux stream to the owning client.
type Tunnel struct {
	store                 registry.Store
	tokens                map[string]Token
	mu                    sync.Mutex
	sessions              map[string]*yamux.Session
	setup                 time.Duration
	idle                  time.Duration
	DynamicClientIdentity bool
}

func NewTunnel(store registry.Store, tokens map[string]Token) *Tunnel {
	return &Tunnel{
		store:    store,
		tokens:   tokens,
		sessions: map[string]*yamux.Session{},
		setup:    10 * time.Second,
		idle:     4 * time.Hour,
	}
}

// SetIdleTimeout bounds how long a dispatched request stream may make no
// progress. Reads and writes each extend the deadline.
func (t *Tunnel) SetIdleTimeout(timeout time.Duration) {
	t.idle = timeout
}

// ActiveSessions returns the number of routable sessions.
func (t *Tunnel) ActiveSessions() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.sessions)
}

func (t *Tunnel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := authenticate(t.tokens, t.DynamicClientIdentity, r)
	if !ok {
		writeTunnelError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required", nil)
		return
	}

	var paths []string
	if err := json.Unmarshal([]byte(r.Header.Get(headerPaths)), &paths); err != nil || len(paths) == 0 {
		writeTunnelError(w, http.StatusBadRequest, "invalid_request", "tunnel paths are required", nil)
		return
	}
	local := r.Header.Get(headerLocal)
	if local == "" {
		writeTunnelError(w, http.StatusBadRequest, "invalid_request", "local target is required", nil)
		return
	}

	if replaced := r.Header.Get(headerReplaces); replaced != "" {
		if claim, found := t.claim(replaced); found && claim.Owner != principal.Owner {
			writeTunnelError(w, http.StatusUnauthorized, "unauthorized", "replacement claim is not owned by this token", nil)
			return
		} else if found {
			t.CloseClaim(replaced)
			_ = t.store.Release(principal.Owner, replaced)
		}
	}

	claim, err := t.store.Create(principal.Owner, paths, local)
	if err != nil {
		t.writeClaimError(w, err)
		return
	}

	w.Header().Set(headerClaim, claim.ID)
	w.Header().Set(headerOwner, claim.Owner)
	w.Header().Set(headerStarted, claim.Started.Format(time.RFC3339Nano))
	if claim.ExpiresAt != nil {
		w.Header().Set(headerExpires, claim.ExpiresAt.Format(time.RFC3339Nano))
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		_ = t.store.Release(principal.Owner, claim.ID)
		return
	}

	conn := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	_ = conn.SetDeadline(time.Now().Add(t.setup))
	session, err := yamux.Server(conn, yamuxConfig())
	if err != nil {
		_ = conn.Close()
		_ = t.store.Release(principal.Owner, claim.ID)
		return
	}

	defer func() {
		t.mu.Lock()
		if t.sessions[claim.ID] == session {
			delete(t.sessions, claim.ID)
		}
		t.mu.Unlock()
		_ = t.store.Release(principal.Owner, claim.ID)
		_ = session.Close()
	}()
	if err = t.completeSetup(session, func() {
		t.mu.Lock()
		t.sessions[claim.ID] = session
		t.mu.Unlock()
	}); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	var expiry *time.Timer
	if claim.ExpiresAt != nil {
		expiry = time.AfterFunc(time.Until(*claim.ExpiresAt), func() {
			t.expireClaim(claim.ID, principal.Owner)
		})
		defer expiry.Stop()
	}
	<-session.CloseChan()
}

func (t *Tunnel) completeSetup(session *yamux.Session, makeRoutable func()) error {
	result := make(chan error, 1)
	go func() {
		stream, err := session.Open()
		if err == nil {
			_, err = io.WriteString(stream, "ready")
		}
		var acknowledgement [3]byte
		if err == nil {
			_, err = io.ReadFull(stream, acknowledgement[:])
			if err == nil && string(acknowledgement[:]) != "ack" {
				err = errors.New("invalid tunnel readiness acknowledgement")
			}
		}
		if err == nil {
			makeRoutable()
			_, err = io.WriteString(stream, "ok")
		}
		if stream != nil {
			_ = stream.Close()
		}
		result <- err
	}()
	timer := time.NewTimer(t.setup)
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-timer.C:
		go func() { _ = session.Close() }()
		return errors.New("tunnel readiness timed out")
	}
}

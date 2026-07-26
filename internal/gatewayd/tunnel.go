package gatewayd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"github.com/iml885203/tunlease/internal/registry"
)

const (
	headerClaim    = "X-Tunlease-Claim"
	headerLocal    = "X-Tunlease-Local"
	headerOwner    = "X-Tunlease-Owner"
	headerPaths    = "X-Tunlease-Paths"
	headerReplaces = "X-Tunlease-Replaces"
	headerStarted  = "X-Tunlease-Started"
	headerExpires  = "X-Tunlease-Expires"
	streamRequest  = byte(1)
	streamRelease  = byte(2)
	streamAck      = byte(3)
	streamExpire   = byte(4)
	streamActivity = byte(5)

	targetUnavailableMessage = "This path is claimed, but its local service is unavailable.\n\n" +
		"If this is your tunnel, check the terminal running tul."
	targetIdleMessage = "This path is claimed, but the tunneled request was idle for too long.\n\n" +
		"If this is your tunnel, check whether the local service is stuck."
)

type activityMessage struct {
	Method     string `json:"method"`
	Path       string `json:"path"`
	Status     int    `json:"status"`
	DurationMS int64  `json:"duration_ms"`
}

type activityResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *activityResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *activityResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

func (w *activityResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

var errClaimOwner = errors.New("claim belongs to another owner")

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

// ProxyByPath returns false only when no connected tunnel can be selected
// before dispatch. Once a stream is opened, later errors are returned to the
// caller and are never replayed to the origin.
func (t *Tunnel) ProxyByPath(w http.ResponseWriter, r *http.Request) bool {
	claimID, ok := t.matchClaim(r.URL.Path)
	if !ok {
		return false
	}
	t.mu.Lock()
	session := t.sessions[claimID]
	t.mu.Unlock()
	if session == nil || session.IsClosed() {
		return false
	}
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = "tunlease"
		},
		Transport: &http.Transport{
			DialContext: func(context.Context, string, string) (net.Conn, error) {
				stream, err := session.Open()
				if err != nil {
					return nil, err
				}
				if _, err = stream.Write([]byte{streamRequest}); err != nil {
					_ = stream.Close()
					return nil, err
				}
				return &idleConn{Conn: stream, timeout: t.idle}, nil
			},
			MaxIdleConns:       1,
			IdleConnTimeout:    5 * time.Second,
			DisableCompression: true,
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			var networkError net.Error
			if errors.As(err, &networkError) && networkError.Timeout() {
				http.Error(w, targetIdleMessage, http.StatusGatewayTimeout)
				return
			}
			http.Error(w, targetUnavailableMessage, http.StatusBadGateway)
		},
	}
	started := time.Now()
	recorded := &activityResponseWriter{ResponseWriter: w}
	proxy.ServeHTTP(recorded, r)
	if recorded.status == 0 {
		recorded.status = http.StatusOK
	}
	activity := activityMessage{
		Method:     r.Method,
		Path:       r.URL.Path,
		Status:     recorded.status,
		DurationMS: time.Since(started).Milliseconds(),
	}
	go sendActivity(session, activity)
	return true
}

type idleConn struct {
	net.Conn
	timeout time.Duration
}

func (c *idleConn) Read(p []byte) (int, error) {
	if c.timeout > 0 {
		_ = c.SetReadDeadline(time.Now().Add(c.timeout))
	}
	return c.Conn.Read(p)
}

func (c *idleConn) Write(p []byte) (int, error) {
	if c.timeout > 0 {
		_ = c.SetWriteDeadline(time.Now().Add(c.timeout))
	}
	return c.Conn.Write(p)
}

func sendActivity(session *yamux.Session, activity activityMessage) {
	stream, err := session.Open()
	if err != nil {
		return
	}
	defer func() { _ = stream.Close() }()
	_ = stream.SetDeadline(time.Now().Add(time.Second))
	if _, err = stream.Write([]byte{streamActivity}); err != nil {
		return
	}
	_ = json.NewEncoder(stream).Encode(activity)
}

func (t *Tunnel) matchClaim(path string) (string, bool) {
	path = strings.TrimRight(path, "/")
	if path == "" {
		path = "/"
	}
	best, bestLen := "", -1
	for _, claim := range t.store.List() {
		for _, pattern := range claim.Paths {
			base := strings.TrimSuffix(strings.TrimSuffix(pattern, "/**"), "/*")
			matches := path == base && !strings.HasSuffix(pattern, "/*")
			if strings.HasSuffix(pattern, "/**") {
				matches = path == base || strings.HasPrefix(path, base+"/")
			} else if strings.HasSuffix(pattern, "/*") && strings.HasPrefix(path, base+"/") {
				child := strings.TrimPrefix(path, base+"/")
				matches = child != "" && !strings.Contains(child, "/")
			}
			if matches && len(base) > bestLen {
				best, bestLen = claim.ID, len(base)
			}
		}
	}
	return best, bestLen >= 0
}

func (t *Tunnel) claim(id string) (registry.Claim, bool) {
	for _, claim := range t.store.List() {
		if claim.ID == id {
			return claim, true
		}
	}
	return registry.Claim{}, false
}

func yamuxConfig() *yamux.Config {
	config := yamux.DefaultConfig()
	config.LogOutput = io.Discard
	config.EnableKeepAlive = true
	config.KeepAliveInterval = 25 * time.Second
	return config
}

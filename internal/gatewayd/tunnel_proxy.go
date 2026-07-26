package gatewayd

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/iml885203/tunlease/internal/registry"
)

const (
	targetUnavailableMessage = "This path is claimed, but its local service is unavailable.\n\n" +
		"If this is your tunnel, check the terminal running tul."
	targetIdleMessage = "This path is claimed, but the tunneled request was idle for too long.\n\n" +
		"If this is your tunnel, check whether the local service is stuck."
)

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
	best, bestLen := "", -1
	for _, claim := range t.store.List() {
		for _, pattern := range claim.Paths {
			specificity, matches := registry.MatchPath(pattern, path)
			if matches && specificity > bestLen {
				best, bestLen = claim.ID, specificity
			}
		}
	}
	return best, bestLen >= 0
}

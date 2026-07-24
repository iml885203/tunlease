package gatewayd

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/iml885203/tunlease/internal/registry"
)

type Token struct {
	Owner string `yaml:"owner"`
	Admin bool   `yaml:"admin"`
}
type Server struct {
	Store             registry.Store
	Tokens            map[string]Token
	TTL, Heartbeat    time.Duration
	TunnelHost        string
	Tunnel            *Tunnel
	TunnelFingerprint string
	OnChange          func()
	// ControlPrefix is the path prefix the control plane is mounted under (e.g.
	// "/_tunlease"). Empty means the control plane is at the root (a dedicated
	// host with no third-party paths to avoid).
	ControlPrefix string
	// FailOpen, if set, handles third-party requests that match no active
	// claim (single-host serve mode points this at the real application). When
	// nil, unmatched requests get a 404.
	FailOpen http.Handler
}

func (s *Server) changed() {
	if s.OnChange != nil {
		s.OnChange()
	}
}

type principal struct {
	owner string
	admin bool
}

func (s *Server) auth(r *http.Request) (principal, bool) {
	token, ok := authenticate(s.Tokens, r)
	return principal{token.Owner, token.Admin}, ok
}

func authenticate(tokens map[string]Token, r *http.Request) (Token, bool) {
	if len(tokens) == 0 {
		return Token{Owner: "anonymous"}, true
	}
	value := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	token, ok := tokens[value]
	return token, ok && value != ""
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}
func errj(w http.ResponseWriter, status int, code, detail string, extra map[string]any) {
	m := map[string]any{"error": code, "detail": detail}
	for k, v := range extra {
		m[k] = v
	}
	write(w, status, m)
}
func (s *Server) Handler() http.Handler {
	// Control plane lives under ControlPrefix (e.g. "/_tunlease"): claim API,
	// tunnel WebSocket, and healthz. It is registered on its own mux so it can
	// be mounted under the prefix with StripPrefix.
	control := http.NewServeMux()
	control.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	control.HandleFunc("POST /api/v1/claims", s.claim)
	control.HandleFunc("POST /api/v1/claims/{id}/heartbeat", s.heartbeat)
	control.HandleFunc("DELETE /api/v1/claims/{id}", s.release)
	control.HandleFunc("GET /api/v1/claims", s.list)
	if s.Tunnel != nil {
		control.Handle("/tunnel", s.Tunnel)
	}

	// Third-party traffic (everything outside the control prefix) is routed to a
	// claimed path's tunnel, else fail-open, else 404.
	thirdParty := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Tunnel != nil && s.Tunnel.ProxyByPath(w, r) {
			return
		}
		if s.FailOpen != nil {
			s.FailOpen.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})

	mux := http.NewServeMux()
	mux.Handle("/", thirdParty)
	if s.ControlPrefix != "" {
		// Same-domain: control plane lives under the prefix, out of the way of
		// the application's own paths.
		mux.Handle(s.ControlPrefix+"/", http.StripPrefix(s.ControlPrefix, control))
	} else {
		// No prefix: control plane at the root, third-party demux as fallback.
		// The control mux's explicit patterns win over "/" for their paths.
		mux.Handle("/api/v1/", control)
		mux.Handle("/tunnel", control)
		mux.Handle("/healthz", control)
	}
	return mux
}
func (s *Server) claim(w http.ResponseWriter, r *http.Request) {
	p, ok := s.auth(r)
	if !ok {
		errj(w, 401, "unauthorized", "valid bearer token required", nil)
		return
	}
	var in struct {
		Paths []string `json:"paths"`
		Local string   `json:"local"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Paths) == 0 {
		errj(w, 400, "invalid_request", "paths are required", nil)
		return
	}
	c, e := s.Store.Create(p.owner, in.Paths, in.Local)
	if e != nil {
		var x *registry.Conflict
		var n *registry.NotAllowed
		switch {
		case errors.Is(e, registry.ErrInvalidPath):
			errj(w, 400, "invalid_request", "each path must start with /, end with /*, and contain no other wildcard", nil)
		case errors.As(e, &x):
			errj(w, 409, "path_claimed", "path overlaps an active claim", map[string]any{"claimed_by": x.Owner, "expires_at": x.ExpiresAt})
		case errors.As(e, &n):
			errj(w, 403, "path_not_allowed", "path is outside the allowlist", nil)
		default:
			errj(w, 503, "claim_limit_reached", e.Error(), nil)
		}
		return
	}
	s.changed()
	write(w, 201, map[string]any{"claim_id": c.ID, "owner": c.Owner, "paths": c.Paths, "expires_at": c.ExpiresAt, "ttl_seconds": int(s.TTL.Seconds()), "heartbeat_seconds": int(s.Heartbeat.Seconds()), "tunnel_fingerprint": s.TunnelFingerprint})
}
func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	p, ok := s.auth(r)
	if !ok {
		errj(w, 401, "unauthorized", "valid bearer token required", nil)
		return
	}
	t, e := s.Store.Heartbeat(p.owner, r.PathValue("id"))
	if e != nil {
		errj(w, 404, "claim_expired", "claim no longer exists", nil)
		return
	}
	write(w, 200, map[string]any{"expires_at": t, "tunnel_fingerprint": s.TunnelFingerprint})
}
func (s *Server) release(w http.ResponseWriter, r *http.Request) {
	p, ok := s.auth(r)
	if !ok {
		errj(w, 401, "unauthorized", "valid bearer token required", nil)
		return
	}
	if e := s.Store.Release(p.owner, r.PathValue("id"), p.admin); e != nil {
		errj(w, 401, "unauthorized", "only owner or admin may release", nil)
		return
	}
	s.changed()
	w.WriteHeader(204)
}
func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	_, ok := s.auth(r)
	if !ok {
		errj(w, 401, "unauthorized", "valid bearer token required", nil)
		return
	}
	cs := s.Store.List()
	if unhealthy, ok := s.Store.(interface{ LastError() error }); ok && unhealthy.LastError() != nil {
		errj(w, 503, "registry_unavailable", "registry is temporarily unavailable", nil)
		return
	}
	type item struct {
		ID      string    `json:"claim_id"`
		Owner   string    `json:"owner"`
		Paths   []string  `json:"paths"`
		Expires time.Time `json:"expires_at"`
	}
	out := make([]item, 0, len(cs))
	for _, c := range cs {
		out = append(out, item{c.ID, c.Owner, c.Paths, c.ExpiresAt})
	}
	write(w, 200, map[string]any{"claims": out})
}

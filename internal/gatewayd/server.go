package gatewayd

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/iml885203/tunlease/internal/registry"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Token struct {
	Owner string `yaml:"owner"`
	Admin bool   `yaml:"admin"`
}
type Server struct {
	Store             registry.Store
	Tokens            map[string]Token
	SidecarToken      string
	TTL, Heartbeat    time.Duration
	TunnelHost        string
	Tunnel            *Tunnel
	TunnelFingerprint string
	OnChange          func()
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
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("POST /api/v1/claims", s.claim)
	mux.HandleFunc("POST /api/v1/claims/{id}/heartbeat", s.heartbeat)
	mux.HandleFunc("DELETE /api/v1/claims/{id}", s.release)
	mux.HandleFunc("GET /api/v1/claims", s.list)
	mux.HandleFunc("GET /api/v1/routes", s.routes)
	if s.Tunnel != nil {
		mux.Handle("/tunnel", s.Tunnel)
		// Everything else is third-party traffic: route a claimed path to its
		// CLI over the tunnel, or 404 so the caller can fall open to the app.
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if s.Tunnel.ProxyByPath(w, r) {
				return
			}
			if s.FailOpen != nil {
				s.FailOpen.ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
		})
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
			errj(w, 503, "ports_exhausted", e.Error(), nil)
		}
		return
	}
	s.changed()
	write(w, 201, map[string]any{"claim_id": c.ID, "owner": c.Owner, "paths": c.Paths, "remote_port": c.RemotePort, "expires_at": c.ExpiresAt, "ttl_seconds": int(s.TTL.Seconds()), "heartbeat_seconds": int(s.Heartbeat.Seconds()), "tunnel_fingerprint": s.TunnelFingerprint})
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
func (s *Server) routes(w http.ResponseWriter, r *http.Request) {
	if s.SidecarToken != "" && r.Header.Get("Authorization") != "Bearer "+s.SidecarToken {
		errj(w, 401, "unauthorized", "valid sidecar token required", nil)
		return
	}
	v := s.Store.Version()
	if unhealthy, ok := s.Store.(interface{ LastError() error }); ok && unhealthy.LastError() != nil {
		errj(w, 503, "registry_unavailable", "registry is temporarily unavailable", nil)
		return
	}
	etag := fmt.Sprintf("\"%d\"", v)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(304)
		return
	}
	w.Header().Set("ETag", etag)
	routes := []map[string]any{}
	for _, c := range s.Store.List() {
		host := s.TunnelHost
		if host == "" {
			host = "127.0.0.1"
		}
		if net.ParseIP(host) == nil && strings.Contains(host, ":") {
			host = strings.Split(host, ":")[0]
		}
		for _, path := range c.Paths {
			routes = append(routes, map[string]any{"path_prefix": path, "tunnel_addr": net.JoinHostPort(host, strconv.Itoa(c.RemotePort)), "claim_id": c.ID, "owner": c.Owner, "expires_at": c.ExpiresAt})
		}
	}
	write(w, 200, map[string]any{"version": v, "routes": routes})
}

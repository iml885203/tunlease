package gatewayd

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/iml885203/tunlease/internal/registry"
)

const ControlPrefix = "/_tunlease"

type Token struct {
	Owner string `yaml:"owner"`
}

type Server struct {
	Store            registry.Store
	Tokens           map[string]Token
	Tunnel           *Tunnel
	FailOpen         http.Handler
	DisableClaimList bool
}

type principal struct {
	owner string
}

func (s *Server) auth(r *http.Request) (principal, bool) {
	token, ok := authenticate(s.Tokens, r)
	return principal{owner: token.Owner}, ok
}

func authenticate(tokens map[string]Token, r *http.Request) (Token, bool) {
	if len(tokens) == 0 {
		return Token{Owner: "anonymous"}, true
	}
	value := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	token, ok := tokens[value]
	return token, ok && value != ""
}

func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func errj(w http.ResponseWriter, status int, code, detail string) {
	write(w, status, map[string]any{"error": code, "detail": detail})
}

func (s *Server) Handler() http.Handler {
	control := http.NewServeMux()
	control.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	control.HandleFunc("DELETE /api/v1/claims/{id}", s.release)
	control.HandleFunc("GET /api/v1/claims", s.list)
	control.Handle("/tunnel", s.Tunnel)

	thirdParty := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Tunnel.ProxyByPath(w, r) {
			return
		}
		s.FailOpen.ServeHTTP(w, r)
	})

	mux := http.NewServeMux()
	mux.Handle("/", thirdParty)
	mux.Handle(ControlPrefix+"/", http.StripPrefix(ControlPrefix, control))
	return mux
}

func (s *Server) release(w http.ResponseWriter, r *http.Request) {
	p, ok := s.auth(r)
	if !ok {
		errj(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
		return
	}
	id := r.PathValue("id")
	if err := s.Tunnel.ReleaseClaim(id, p.owner); errors.Is(err, registry.ErrNotFound) {
		errj(w, http.StatusNotFound, "claim_not_found", "claim no longer exists")
		return
	} else if errors.Is(err, errClaimOwner) {
		errj(w, http.StatusUnauthorized, "unauthorized", "only the owner may release")
		return
	} else if err != nil {
		errj(w, http.StatusConflict, "release_failed", "tunnel did not acknowledge release")
		return
	}
	_ = s.Store.Release(p.owner, id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	if s.DisableClaimList {
		http.NotFound(w, r)
		return
	}
	if _, ok := s.auth(r); !ok {
		errj(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
		return
	}
	write(w, http.StatusOK, map[string]any{"claims": s.Store.List()})
}

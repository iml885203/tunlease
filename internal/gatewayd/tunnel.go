package gatewayd

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"io"
	"math/big"
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

// Tunnel is a purpose-built reverse tunnel. Each authenticated claim may bind
// only its allocated port, and each stream is forwarded by the CLI to one
// fixed localhost target. It intentionally has no arbitrary remote or SOCKS mode.
type Tunnel struct {
	store       registry.Store
	tokens      map[string]Token
	tlsConfig   *tls.Config
	fingerprint string
	mu          sync.Mutex
	sessions    map[string]*yamux.Session
}

func NewTunnel(store registry.Store, tokens map[string]Token) (*Tunnel, error) {
	cert, fingerprint, err := tunnelCertificate()
	if err != nil {
		return nil, err
	}
	return &Tunnel{
		store:       store,
		tokens:      tokens,
		tlsConfig:   &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13},
		fingerprint: fingerprint,
		sessions:    map[string]*yamux.Session{},
	}, nil
}

func (t *Tunnel) Fingerprint() string { return t.fingerprint }

func (t *Tunnel) Sync() {
	live := map[string]bool{}
	for _, claim := range t.store.List() {
		live[claim.ID] = true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, session := range t.sessions {
		if !live[id] {
			_ = session.Close()
			delete(t.sessions, id)
		}
	}
}

// ServeHTTP handles the CLI's tunnel WebSocket upgrade. It establishes the
// inner TLS + yamux session and keeps it in the session map. Third-party
// traffic no longer arrives on a per-claim TCP port; it enters as HTTP on the
// gateway's public listener and is demuxed to the right session by ProxyByPath.
func (t *Tunnel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := authenticate(t.tokens, r)
	claimID := r.Header.Get("X-Tunlease-Claim")
	claim, found := t.claim(claimID)
	if !ok || !found || claim.Owner != principal.Owner {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	conn := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	secure := tls.Server(conn, t.tlsConfig)
	if err = secure.HandshakeContext(r.Context()); err != nil {
		_ = conn.Close()
		return
	}
	session, err := yamux.Server(secure, yamuxConfig())
	if err != nil {
		_ = conn.Close()
		return
	}

	t.mu.Lock()
	if old := t.sessions[claimID]; old != nil {
		_ = old.Close()
	}
	t.sessions[claimID] = session
	t.mu.Unlock()

	defer func() {
		_ = session.Close()
		t.mu.Lock()
		if t.sessions[claimID] == session {
			delete(t.sessions, claimID)
		}
		t.mu.Unlock()
	}()
	// Hold the request open for the session's lifetime; the CLI keeps the WS up
	// with yamux keepalives and reconnects if it drops.
	<-session.CloseChan()
}

// ProxyByPath routes an inbound third-party HTTP request to the CLI that owns
// the longest claimed path prefix matching the request path. Each request opens
// one yamux stream to that CLI, which forwards it to its local target. Requests
// that match no claim (or whose CLI is not connected) get a 404 so the caller
// can fall open to the real application.
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
		Director: func(req *http.Request) { req.URL.Scheme = "http"; req.URL.Host = "tunlease" },
		Transport: &http.Transport{
			DialContext:        func(context.Context, string, string) (net.Conn, error) { return session.Open() },
			MaxIdleConns:       1,
			IdleConnTimeout:    5 * time.Second,
			DisableCompression: true,
		},
	}
	proxy.ServeHTTP(w, r)
	return true
}

// matchClaim finds the claim whose longest path prefix contains the request
// path. Prefixes are stored with a trailing "/*"; a claim on "/webhooks/x/*"
// matches "/webhooks/x" and "/webhooks/x/anything".
func (t *Tunnel) matchClaim(path string) (string, bool) {
	best, bestLen := "", -1
	for _, claim := range t.store.List() {
		for _, p := range claim.Paths {
			base := strings.TrimSuffix(p, "/*")
			if (path == base || strings.HasPrefix(path, base+"/")) && len(base) > bestLen {
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
	c := yamux.DefaultConfig()
	c.LogOutput = io.Discard
	c.EnableKeepAlive = true
	c.KeepAliveInterval = 25 * time.Second
	return c
}

func tunnelCertificate() (tls.Certificate, string, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()),
		Subject:      pkix.Name{CommonName: "tunlease-tunnel"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	sum := sha256.Sum256(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}, hex.EncodeToString(sum[:]), nil
}

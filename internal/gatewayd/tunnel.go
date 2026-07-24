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
	listeners   map[string]net.Listener
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
		listeners:   map[string]net.Listener{},
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
			if listener := t.listeners[id]; listener != nil {
				_ = listener.Close()
				delete(t.listeners, id)
			}
			_ = session.Close()
			delete(t.sessions, id)
		}
	}
}

func (t *Tunnel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := authenticate(t.tokens, r)
	claimID := r.Header.Get("X-Tunlease-Claim")
	claim, found := t.claim(claimID)
	if !ok || !found || claim.Owner != principal.Owner {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	listener, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", itoa(claim.RemotePort)))
	if err != nil {
		http.Error(w, "tunnel port unavailable", http.StatusConflict)
		return
	}

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		_ = listener.Close()
		return
	}
	conn := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	secure := tls.Server(conn, t.tlsConfig)
	if err = secure.HandshakeContext(r.Context()); err != nil {
		_ = listener.Close()
		_ = conn.Close()
		return
	}
	session, err := yamux.Server(secure, yamuxConfig())
	if err != nil {
		_ = listener.Close()
		_ = conn.Close()
		return
	}

	t.mu.Lock()
	if old := t.sessions[claimID]; old != nil {
		_ = old.Close()
	}
	t.sessions[claimID] = session
	t.listeners[claimID] = listener
	t.mu.Unlock()

	defer func() {
		_ = listener.Close()
		_ = session.Close()
		t.mu.Lock()
		if t.sessions[claimID] == session {
			delete(t.sessions, claimID)
			delete(t.listeners, claimID)
		}
		t.mu.Unlock()
	}()
	go func() {
		<-session.CloseChan()
		_ = listener.Close()
	}()

	for {
		client, err := listener.Accept()
		if err != nil {
			return
		}
		stream, err := session.Open()
		if err != nil {
			_ = client.Close()
			return
		}
		go bridge(client, stream)
	}
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

func bridge(a, b net.Conn) {
	done := make(chan struct{}, 2)
	copyOne := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if tcp, ok := dst.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		done <- struct{}{}
	}
	go copyOne(a, b)
	go copyOne(b, a)
	<-done
	_ = a.Close()
	_ = b.Close()
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

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	b := make([]byte, 0, 6)
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

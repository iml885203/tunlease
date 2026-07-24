package tunnelclient

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

func startTunnel(ctx context.Context, c *Client, claimID, fingerprint string, to int) (context.CancelFunc, error) {
	server, err := tunnelURL(c.gateway)
	if err != nil {
		return nil, err
	}
	tctx, cancel := context.WithCancel(ctx)
	session, err := dialTunnel(tctx, c.http, server, c.token, claimID, fingerprint, to)
	if err != nil {
		cancel()
		return nil, err
	}
	go reconnectLoop(tctx, session, c.http, server, c.token, claimID, fingerprint, to)
	return cancel, nil
}

func reconnectLoop(ctx context.Context, current *yamux.Session, httpClient *http.Client, server, token, claimID, fingerprint string, to int) {
	for {
		select {
		case <-ctx.Done():
			_ = current.Close()
			return
		case <-current.CloseChan():
		}
		for ctx.Err() == nil {
			if !waitRetry(ctx, time.Second) {
				return
			}
			next, err := dialTunnel(ctx, httpClient, server, token, claimID, fingerprint, to)
			if err == nil {
				current = next
				break
			}
		}
	}
}

func waitRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func dialTunnel(ctx context.Context, httpClient *http.Client, server, token, claimID, fingerprint string, to int) (*yamux.Session, error) {
	headers := http.Header{}
	if token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}
	headers.Set("X-Tunlease-Claim", claimID)
	ws, response, err := websocket.Dial(ctx, server, &websocket.DialOptions{HTTPClient: httpClient, HTTPHeader: headers})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	conn := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	secure := tls.Client(conn, pinnedTLSConfig(fingerprint))
	if err = secure.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	session, err := yamux.Client(secure, yamuxConfig())
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	go acceptStreams(session, to)
	return session, nil
}

func pinnedTLSConfig(fingerprint string) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, //nolint:gosec // authenticated claim pins the self-signed inner certificate
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) != 1 {
				return errors.New("unexpected tunnel certificate chain")
			}
			sum := sha256.Sum256(rawCerts[0])
			if hex.EncodeToString(sum[:]) != fingerprint {
				return errors.New("tunnel certificate fingerprint mismatch")
			}
			return nil
		},
	}
}

func yamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = io.Discard
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 25 * time.Second
	return cfg
}

func acceptStreams(session *yamux.Session, to int) {
	for {
		stream, err := session.Accept()
		if err != nil {
			return
		}
		go forwardLocal(stream, to)
	}
}

func forwardLocal(stream net.Conn, to int) {
	local, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(to)), time.Second)
	if err != nil {
		_ = stream.Close()
		return
	}
	bridge(stream, local)
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

func tunnelURL(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", errors.New("gateway URL scheme must be http or https")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/tunnel"
	return u.String(), nil
}

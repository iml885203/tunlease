// Package tunnelclient provides the reusable Tunlease claim and reverse-tunnel
// client used by both the standalone CLI and embedding applications.
package tunnelclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/iml885203/tunlease/internal/claimpath"
)

const (
	MaxPathsPerClaim = claimpath.MaxPathsPerClaim
	MaxPathLength    = claimpath.MaxPathLength
)

// Config configures a Client. HTTPClient is optional.
type Config struct {
	Gateway string
	Token   string
	// Insecure skips TLS certificate verification of the gateway connection
	// (API + tunnel WebSocket). Use only on trusted development networks.
	// Ignored when HTTPClient is provided.
	Insecure bool
	// DefaultScheme is used when Gateway has no scheme. Defaults to "https".
	// Set to "http" for a gateway without TLS (e.g. a local demo).
	DefaultScheme string
	HTTPClient    *http.Client
}

// Client talks to a Tunlease gateway and opens reverse tunnels.
type Client struct {
	gateway string
	token   string
	http    *http.Client
}

// Claim describes one active tunnel and its exclusively owned paths.
type Claim struct {
	ID        string     `json:"claim_id"`
	Owner     string     `json:"owner"`
	Paths     []string   `json:"paths"`
	StartedAt time.Time  `json:"started_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// APIError is a structured error returned by the gateway.
type APIError struct {
	Status    int    `json:"-"`
	Code      string `json:"error"`
	Detail    string `json:"detail"`
	ClaimedBy string `json:"claimed_by"`
}

func (e *APIError) Error() string {
	if e.Code == "path_claimed" {
		return fmt.Sprintf("path already claimed by %s", e.ClaimedBy)
	}
	if e.Detail != "" {
		return e.Detail
	}
	return fmt.Sprintf("gateway returned HTTP %d", e.Status)
}

// NormalizePath validates a callback path and removes a trailing slash.
// A trailing /* matches one child segment; /** matches the whole subtree.
func NormalizePath(path string) (string, error) {
	return claimpath.Normalize(path)
}

// EventType identifies a lifecycle change emitted by a Session.
type EventType string

const (
	EventTunnelDisconnected EventType = "tunnel_disconnected"
	EventTunnelReconnected  EventType = "tunnel_reconnected"
	EventLocalTargetError   EventType = "local_target_error"
	EventRequestActivity    EventType = "request_activity"
)

// Event describes a best-effort, non-terminal lifecycle notification. Slow
// consumers may miss events; Claim, Done, and Err are authoritative.
type Event struct {
	Type     EventType
	Claim    Claim
	Err      error
	Method   string
	Path     string
	Status   int
	Duration time.Duration
}

// Session owns one active tunnel and its paths.
type Session struct {
	client *Client
	cancel context.CancelFunc
	done   chan struct{}
	events chan Event

	mu    sync.RWMutex
	claim Claim
	err   error
	once  sync.Once
}

// New validates config and creates a reusable client.
// DefaultControlPrefix is the URL path prefix the gateway serves its control
// plane under on a same-domain deployment. The client adds it automatically so
// users only need to give the gateway's domain.
const DefaultControlPrefix = "/_tunlease"

func New(cfg Config) (*Client, error) {
	if cfg.DefaultScheme != "" && cfg.DefaultScheme != "http" && cfg.DefaultScheme != "https" {
		return nil, errors.New("default scheme must be http or https")
	}
	base, err := normalizeGateway(cfg.Gateway, cfg.DefaultScheme)
	if err != nil {
		return nil, err
	}
	h := cfg.HTTPClient
	if h == nil {
		if cfg.Insecure {
			// Explicit opt-in for a self-signed gateway on a trusted network.
			h = &http.Client{Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // opt-in for self-signed gateways
			}}
		} else {
			h = http.DefaultClient
		}
	}
	return &Client{gateway: base, token: cfg.Token, http: h}, nil
}

// normalizeGateway accepts only the supported whole-host topology and appends
// the fixed control-plane prefix.
func normalizeGateway(gateway, defaultScheme string) (string, error) {
	if gateway == "" {
		return "", errors.New("gateway is required")
	}
	if defaultScheme == "" {
		defaultScheme = "https"
	}
	if !strings.Contains(gateway, "://") {
		gateway = defaultScheme + "://" + gateway
	}
	u, err := url.Parse(gateway)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("gateway must be a host or an http(s) URL")
	}
	if strings.TrimRight(u.Path, "/") != "" {
		return "", errors.New("gateway must not include a path")
	}
	u.Path = DefaultControlPrefix
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// Gateway returns the normalized gateway base URL used by the client.
func (c *Client) Gateway() string { return c.gateway }

// Start claims paths by establishing a tunnel. The paths remain owned until
// ctx is cancelled or Close is called.
func (c *Client) Start(ctx context.Context, paths []string, localPort int) (*Session, error) {
	if len(paths) == 0 || len(paths) > MaxPathsPerClaim {
		return nil, fmt.Errorf("between 1 and %d paths are required", MaxPathsPerClaim)
	}
	if localPort < 1 || localPort > 65535 {
		return nil, errors.New("local port must be between 1 and 65535")
	}
	normalized := make([]string, 0, len(paths))
	for _, path := range paths {
		canonical, err := NormalizePath(path)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", path, err)
		}
		normalized = append(normalized, canonical)
	}
	sctx, cancel := context.WithCancel(ctx)
	claim, reconnects, stopTunnel, err := startTunnel(sctx, c, normalized, localPort)
	if err != nil {
		cancel()
		return nil, err
	}
	s := &Session{client: c, cancel: cancel, done: make(chan struct{}), events: make(chan Event, 8), claim: claim}
	go s.run(sctx, reconnects, stopTunnel)
	return s, nil
}

// Claim returns the current live tunnel. Its ID changes after reconnect.
func (s *Session) Claim() Claim {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.claim
	c.Paths = append([]string(nil), c.Paths...)
	return c
}

// Events reports best-effort, non-terminal lifecycle notifications. Consumers
// must not depend on receiving every event for correctness.
func (s *Session) Events() <-chan Event { return s.events }

// Done closes when the session has released its tunnel or failed terminally.
func (s *Session) Done() <-chan struct{} { return s.done }

// Err returns the terminal session error, or nil after a normal close.
func (s *Session) Err() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.err
}

// Close releases the paths and waits for the tunnel to stop.
func (s *Session) Close() error {
	s.once.Do(s.cancel)
	<-s.done
	return s.Err()
}

func (s *Session) run(ctx context.Context, reconnects <-chan tunnelUpdate, stopTunnel context.CancelFunc) {
	defer close(s.done)
	defer close(s.events)
	defer func() { stopTunnel() }()
	for {
		select {
		case <-ctx.Done():
			return
		case update, ok := <-reconnects:
			if !ok {
				if ctx.Err() == nil {
					s.setError(errors.New("tunnel stopped"))
				}
				return
			}
			if update.err != nil {
				s.setError(update.err)
				return
			}
			if update.event != nil {
				s.emit(*update.event)
				continue
			}
			s.setClaim(update.claim)
			s.emit(Event{Type: EventTunnelReconnected, Claim: update.claim})
		}
	}
}

func (s *Session) setClaim(claim Claim) {
	s.mu.Lock()
	s.claim = claim
	s.mu.Unlock()
}

func (s *Session) setError(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

func (s *Session) emit(event Event) {
	select {
	case s.events <- event:
	default:
	}
}

// List returns every active claim visible to the caller.
func (c *Client) List(ctx context.Context) ([]Claim, error) {
	var out struct {
		Claims []Claim `json:"claims"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/claims", nil, &out); err != nil {
		return nil, err
	}
	return out.Claims, nil
}

// Release releases a claim by ID.
func (c *Client) Release(ctx context.Context, claimID string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/claims/"+url.PathEscape(claimID), nil, nil)
}

func (c *Client) do(ctx context.Context, method, endpoint string, in, out any) error {
	body := bytes.NewReader(nil)
	if in != nil {
		payload, _ := json.Marshal(in)
		body = bytes.NewReader(payload)
	}
	u, err := joinURL(c.gateway, endpoint)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return c.connectHint(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		apiErr := APIError{Status: resp.StatusCode}
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		return &apiErr
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// connectHint annotates a connection failure with a hint to try http:// when
// the gateway is https (the default when the scheme is omitted). It does NOT
// retry over http automatically — silently downgrading to cleartext would be a
// security footgun and would mask the real error.
func (c *Client) connectHint(err error) error {
	if strings.HasPrefix(c.gateway, "https://") {
		return fmt.Errorf("%w (if the gateway has no TLS — e.g. localhost or an internal host — use an http:// gateway URL)", err)
	}
	return err
}

func joinURL(base, endpoint string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(endpoint, "/")
	return u.String(), nil
}

// Package tunnelclient provides the reusable Tunlease claim and reverse-tunnel
// client used by both the standalone CLI and embedding applications.
package tunnelclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Config configures a Client. HTTPClient is optional.
type Config struct {
	Gateway    string
	Token      string
	HTTPClient *http.Client
}

// Client talks to a Tunlease gateway and opens reverse tunnels.
type Client struct {
	gateway string
	token   string
	http    *http.Client
}

// Claim is an active lease returned by the gateway.
type Claim struct {
	ID          string    `json:"claim_id"`
	Owner       string    `json:"owner"`
	Paths       []string  `json:"paths"`
	ExpiresAt   time.Time `json:"expires_at"`
	Heartbeat   int       `json:"heartbeat_seconds"`
	Fingerprint string    `json:"tunnel_fingerprint,omitempty"`
}

// APIError is a structured error returned by the gateway.
type APIError struct {
	Status    int       `json:"-"`
	Code      string    `json:"error"`
	Detail    string    `json:"detail"`
	ClaimedBy string    `json:"claimed_by"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (e *APIError) Error() string {
	if e.Code == "path_claimed" {
		return fmt.Sprintf("path already claimed by %s (lease expires %s)", e.ClaimedBy, e.ExpiresAt.Local().Format("15:04:05"))
	}
	if e.Detail != "" {
		return e.Detail
	}
	return fmt.Sprintf("gateway returned HTTP %d", e.Status)
}

// NormalizePath validates a callback path and returns Tunlease's canonical
// trailing-wildcard representation.
func NormalizePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "/") {
		return "", errors.New("path must start with /")
	}
	if strings.Contains(path, "*") && !strings.HasSuffix(path, "/*") {
		return "", errors.New("wildcard is only allowed as trailing /*")
	}
	path = strings.TrimSuffix(path, "/*")
	path = strings.TrimRight(path, "/")
	if path == "" {
		return "", errors.New("root path is not allowed")
	}
	if strings.Contains(path, "*") {
		return "", errors.New("wildcard is only allowed as trailing /*")
	}
	return path + "/*", nil
}

// EventType identifies a lifecycle change emitted by a Session.
type EventType string

const (
	EventHeartbeatWarning  EventType = "heartbeat_warning"
	EventLeaseReclaimed    EventType = "lease_reclaimed"
	EventTunnelReconnected EventType = "tunnel_reconnected"
)

// Event describes a best-effort, non-terminal lifecycle notification. Slow
// consumers may miss events; Claim, Done, and Err are authoritative.
type Event struct {
	Type  EventType
	Claim Claim
	Err   error
}

// Session owns one claim, its heartbeat, and its reverse tunnel.
type Session struct {
	client *Client
	paths  []string
	to     int
	cancel context.CancelFunc
	done   chan struct{}
	events chan Event

	mu    sync.RWMutex
	claim Claim
	err   error
	once  sync.Once
}

// New validates config and creates a reusable client.
func New(cfg Config) (*Client, error) {
	if cfg.Gateway == "" {
		return nil, errors.New("gateway is required")
	}
	u, err := url.Parse(cfg.Gateway)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("gateway must be an absolute http or https URL")
	}
	h := cfg.HTTPClient
	if h == nil {
		h = http.DefaultClient
	}
	return &Client{gateway: strings.TrimRight(cfg.Gateway, "/"), token: cfg.Token, http: h}, nil
}

// Gateway returns the normalized gateway base URL used by the client.
func (c *Client) Gateway() string { return c.gateway }

// Start claims paths and establishes the tunnel before returning. The session
// keeps its lease alive until ctx is cancelled, Close is called, or a terminal
// error occurs.
func (c *Client) Start(ctx context.Context, paths []string, localPort int) (*Session, error) {
	if len(paths) == 0 {
		return nil, errors.New("at least one path is required")
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
	claim, err := c.create(ctx, normalized, localPort)
	if err != nil {
		return nil, err
	}
	sctx, cancel := context.WithCancel(ctx)
	stopTunnel, err := startTunnel(sctx, c, claim.ID, claim.Fingerprint, localPort)
	if err != nil {
		cancel()
		c.releaseQuietly(claim.ID)
		return nil, err
	}
	s := &Session{client: c, paths: normalized, to: localPort, cancel: cancel, done: make(chan struct{}), events: make(chan Event, 8), claim: claim}
	go s.run(sctx, stopTunnel)
	return s, nil
}

// Claim returns the session's current claim. Its ID may change after an
// expired lease is reclaimed.
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

// Done closes when the session has released its claim or failed terminally.
func (s *Session) Done() <-chan struct{} { return s.done }

// Err returns the terminal session error, or nil after a normal close.
func (s *Session) Err() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.err
}

// Close releases the claim and waits for the session to stop.
func (s *Session) Close() error {
	s.once.Do(s.cancel)
	<-s.done
	return s.Err()
}

func (s *Session) run(ctx context.Context, stopTunnel context.CancelFunc) {
	defer close(s.done)
	defer close(s.events)
	defer func() { stopTunnel() }()
	defer func() { s.client.releaseQuietly(s.Claim().ID) }()

	hb := time.Duration(s.Claim().Heartbeat) * time.Second
	if hb < time.Second {
		hb = 30 * time.Second
	}
	ticker := time.NewTicker(hb)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current := s.Claim()
			fingerprint, err := s.client.heartbeat(ctx, current.ID)
			var apiErr *APIError
			switch {
			case errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound:
				stopTunnel()
				next, createErr := s.client.create(ctx, s.paths, s.to)
				if createErr != nil {
					s.setError(fmt.Errorf("re-claim failed: %w", createErr))
					return
				}
				stopTunnel, createErr = startTunnel(ctx, s.client, next.ID, next.Fingerprint, s.to)
				if createErr != nil {
					s.client.releaseQuietly(next.ID)
					s.setError(fmt.Errorf("reconnect tunnel: %w", createErr))
					return
				}
				s.setClaim(next)
				s.emit(Event{Type: EventLeaseReclaimed, Claim: next})
			case err != nil && ctx.Err() == nil:
				s.emit(Event{Type: EventHeartbeatWarning, Claim: current, Err: err})
			case fingerprint != "" && fingerprint != current.Fingerprint:
				stopTunnel()
				stopTunnel, err = startTunnel(ctx, s.client, current.ID, fingerprint, s.to)
				if err != nil {
					s.setError(fmt.Errorf("reconnect tunnel: %w", err))
					return
				}
				current.Fingerprint = fingerprint
				s.setClaim(current)
				s.emit(Event{Type: EventTunnelReconnected, Claim: current})
			}
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

func (c *Client) create(ctx context.Context, paths []string, localPort int) (Claim, error) {
	var out Claim
	err := c.do(ctx, http.MethodPost, "/api/v1/claims", map[string]any{"paths": paths, "local": fmt.Sprintf("localhost:%d", localPort)}, &out)
	return out, err
}

func (c *Client) heartbeat(ctx context.Context, id string) (string, error) {
	var out struct {
		Fingerprint string `json:"tunnel_fingerprint"`
	}
	err := c.do(ctx, http.MethodPost, "/api/v1/claims/"+url.PathEscape(id)+"/heartbeat", nil, &out)
	return out.Fingerprint, err
}

func (c *Client) releaseQuietly(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.Release(ctx, id)
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
		return err
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

func joinURL(base, endpoint string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(endpoint, "/")
	return u.String(), nil
}

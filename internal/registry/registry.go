package registry

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrNotFound = errors.New("claim expired")

var ErrInvalidPath = errors.New("invalid claim path")

func ValidPath(p string) bool {
	return strings.HasPrefix(p, "/") && strings.HasSuffix(p, "/*") && len(p) > 2 && !strings.Contains(strings.TrimSuffix(p, "/*"), "*")
}

type Conflict struct {
	Owner     string
	ExpiresAt time.Time
}

func (e *Conflict) Error() string { return "path claimed" }

type NotAllowed struct{ Path string }

func (e *NotAllowed) Error() string { return "path not allowed" }

type NoPorts struct{}

func (*NoPorts) Error() string { return "no ports available" }

type Clock interface{ Now() time.Time }
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

type Claim struct {
	ID         string    `json:"claim_id"`
	Owner      string    `json:"owner"`
	Paths      []string  `json:"paths"`
	Local      string    `json:"local,omitempty"`
	RemotePort int       `json:"remote_port,omitempty"`
	ExpiresAt  time.Time `json:"expires_at"`
}
type Store interface {
	Create(owner string, paths []string, local string) (Claim, error)
	Heartbeat(owner, id string) (time.Time, error)
	Release(owner, id string, admin bool) error
	List() []Claim
	OwnsPort(owner string, port int) bool
	ReleaseByPath(owner, path string) error
	ReleaseByLocalPort(owner string, port int) []Claim
	Version() uint64
}
type Memory struct {
	mu      sync.Mutex
	claims  map[string]Claim
	free    []int
	allowed []string
	ttl     time.Duration
	clock   Clock
	version uint64
	log     *slog.Logger
}

func NewMemory(first, last int, allowed []string, ttl time.Duration, clock Clock, logger *slog.Logger) *Memory {
	free := make([]int, 0, last-first+1)
	for p := first; p <= last; p++ {
		free = append(free, p)
	}
	if clock == nil {
		clock = RealClock{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Memory{claims: map[string]Claim{}, free: free, allowed: allowed, ttl: ttl, clock: clock, log: logger}
}
func prefix(p string) string { return strings.TrimSuffix(p, "*") }
func overlap(a, b string) bool {
	return strings.HasPrefix(prefix(a), prefix(b)) || strings.HasPrefix(prefix(b), prefix(a))
}
func (m *Memory) expireLocked() {
	now := m.clock.Now()
	for id, c := range m.claims {
		if !c.ExpiresAt.After(now) {
			delete(m.claims, id)
			m.free = append(m.free, c.RemotePort)
			sort.Ints(m.free)
			m.version++
			m.log.Info("lease audit", "event", "expire", "who", c.Owner, "when", now.UTC(), "paths", c.Paths, "claim_id", id)
		}
	}
}
func (m *Memory) Create(owner string, paths []string, local string) (Claim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	for _, p := range paths {
		if !ValidPath(p) {
			return Claim{}, ErrInvalidPath
		}
		// An empty allowlist allows every path — the allowlist is opt-in.
		// Configure prefixes to restrict which paths may be claimed.
		ok := len(m.allowed) == 0
		for _, a := range m.allowed {
			if strings.HasPrefix(prefix(p), a) {
				ok = true
			}
		}
		if !ok {
			return Claim{}, &NotAllowed{p}
		}
		for _, c := range m.claims {
			for _, q := range c.Paths {
				if overlap(p, q) {
					return Claim{}, &Conflict{c.Owner, c.ExpiresAt}
				}
			}
		}
	}
	if len(m.free) == 0 {
		return Claim{}, &NoPorts{}
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	port := m.free[0]
	m.free = m.free[1:]
	c := Claim{ID: id, Owner: owner, Paths: append([]string(nil), paths...), Local: local, RemotePort: port, ExpiresAt: m.clock.Now().Add(m.ttl).UTC()}
	m.claims[id] = c
	m.version++
	m.log.Info("lease audit", "event", "claim", "who", owner, "when", m.clock.Now().UTC(), "paths", paths, "claim_id", id)
	return c, nil
}
func (m *Memory) Heartbeat(owner, id string) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	c, ok := m.claims[id]
	if !ok || c.Owner != owner {
		return time.Time{}, ErrNotFound
	}
	c.ExpiresAt = m.clock.Now().Add(m.ttl).UTC()
	m.claims[id] = c
	m.version++
	return c.ExpiresAt, nil
}
func (m *Memory) Release(owner, id string, admin bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	c, ok := m.claims[id]
	if !ok {
		return nil
	}
	if c.Owner != owner && !admin {
		return errors.New("forbidden")
	}
	delete(m.claims, id)
	m.free = append(m.free, c.RemotePort)
	sort.Ints(m.free)
	m.version++
	m.log.Info("lease audit", "event", "release", "who", owner, "when", m.clock.Now().UTC(), "paths", c.Paths, "claim_id", id)
	return nil
}
func (m *Memory) List() []Claim {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	out := make([]Claim, 0, len(m.claims))
	for _, c := range m.claims {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (m *Memory) OwnsPort(owner string, port int) bool {
	for _, c := range m.List() {
		if c.Owner == owner && c.RemotePort == port {
			return true
		}
	}
	return false
}
func (m *Memory) ReleaseByPath(owner, path string) error {
	for _, c := range m.List() {
		if c.Owner == owner {
			for _, p := range c.Paths {
				if p == path {
					return m.Release(owner, c.ID, false)
				}
			}
		}
	}
	return nil
}
func (m *Memory) ReleaseByLocalPort(owner string, port int) []Claim {
	var out []Claim
	for _, c := range m.List() {
		if c.Owner == owner && strings.HasSuffix(c.Local, ":"+itoa(port)) {
			out = append(out, c)
			_ = m.Release(owner, c.ID, false)
		}
	}
	return out
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
func (m *Memory) Version() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	return m.version
}

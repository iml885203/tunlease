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

var (
	ErrInvalidPath = errors.New("invalid claim path")
	ErrNotFound    = errors.New("claim not found")
)

const (
	MaxPathsPerClaim = 8
	MaxPathLength    = 512
)

type Conflict struct {
	Owner string
}

func (e *Conflict) Error() string { return "path claimed" }

type NotAllowed struct{ Path string }

func (e *NotAllowed) Error() string { return "path not allowed" }

type TooManyClaims struct{}

func (*TooManyClaims) Error() string { return "claim limit reached" }

type TooManyOwnerClaims struct{}

func (*TooManyOwnerClaims) Error() string { return "owner claim limit reached" }

type Claim struct {
	ID        string     `json:"claim_id"`
	Owner     string     `json:"owner"`
	Paths     []string   `json:"paths"`
	Local     string     `json:"-"`
	Started   time.Time  `json:"started_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Store contains only claims backed by a live tunnel session. There is no
// independent lease lifecycle: the tunnel creates the claim and releases it
// when the connection closes.
type Store interface {
	Create(owner string, paths []string, local string) (Claim, error)
	Release(owner, id string) error
	List() []Claim
	ListOwner(owner string) []Claim
}

type Memory struct {
	mu               sync.Mutex
	claims           map[string]Claim
	max              int
	maxPerOwner      int
	minPathSegments  int
	maxClaimDuration time.Duration
	allowed          []string
	log              *slog.Logger
}

type Options struct {
	MaxClaims            int
	MaxClaimsPerOwner    int
	MinClaimPathSegments int
	MaxClaimDuration     time.Duration
	Allowed              []string
}

func NewMemory(options Options, logger *slog.Logger) *Memory {
	if logger == nil {
		logger = slog.Default()
	}
	return &Memory{
		claims:           map[string]Claim{},
		max:              options.MaxClaims,
		maxPerOwner:      options.MaxClaimsPerOwner,
		minPathSegments:  options.MinClaimPathSegments,
		maxClaimDuration: options.MaxClaimDuration,
		allowed:          append([]string(nil), options.Allowed...),
		log:              logger,
	}
}

func ValidPath(path string) bool {
	return strings.HasPrefix(path, "/") &&
		strings.HasSuffix(path, "/*") &&
		len(path) > 2 &&
		len(path) <= MaxPathLength &&
		!strings.Contains(strings.TrimSuffix(path, "/*"), "*")
}

func prefix(path string) string { return strings.TrimSuffix(path, "*") }

func pathSegments(path string) int {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(path, "/"), "/*")
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "/"))
}

func overlap(a, b string) bool {
	return strings.HasPrefix(prefix(a), prefix(b)) ||
		strings.HasPrefix(prefix(b), prefix(a))
}

func (m *Memory) Create(owner string, paths []string, local string) (Claim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(paths) == 0 || len(paths) > MaxPathsPerClaim {
		return Claim{}, ErrInvalidPath
	}
	for _, path := range paths {
		if !ValidPath(path) {
			return Claim{}, ErrInvalidPath
		}
		if pathSegments(path) < m.minPathSegments {
			return Claim{}, &NotAllowed{Path: path}
		}
		allowed := len(m.allowed) == 0
		for _, candidate := range m.allowed {
			if strings.HasPrefix(prefix(path), candidate) {
				allowed = true
				break
			}
		}
		if !allowed {
			return Claim{}, &NotAllowed{Path: path}
		}
		for _, existing := range m.claims {
			for _, claimedPath := range existing.Paths {
				if overlap(path, claimedPath) {
					return Claim{}, &Conflict{Owner: existing.Owner}
				}
			}
		}
	}
	if len(m.claims) >= m.max {
		return Claim{}, &TooManyClaims{}
	}
	if m.maxPerOwner > 0 {
		owned := 0
		for _, claim := range m.claims {
			if claim.Owner == owner {
				owned++
			}
		}
		if owned >= m.maxPerOwner {
			return Claim{}, &TooManyOwnerClaims{}
		}
	}

	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return Claim{}, err
	}
	now := time.Now().UTC()
	claim := Claim{
		ID:      hex.EncodeToString(random),
		Owner:   owner,
		Paths:   append([]string(nil), paths...),
		Local:   local,
		Started: now,
	}
	if m.maxClaimDuration > 0 {
		expiresAt := now.Add(m.maxClaimDuration)
		claim.ExpiresAt = &expiresAt
	}
	m.claims[claim.ID] = claim
	m.log.Info("tunnel audit", "event", "connect", "who", owner, "when", now, "paths", paths, "claim_id", claim.ID)
	return clone(claim), nil
}

func (m *Memory) Release(owner, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	claim, ok := m.claims[id]
	if !ok {
		return ErrNotFound
	}
	if claim.Owner != owner {
		return errors.New("forbidden")
	}
	delete(m.claims, id)
	m.log.Info("tunnel audit", "event", "disconnect", "who", owner, "when", time.Now().UTC(), "paths", claim.Paths, "claim_id", id)
	return nil
}

func (m *Memory) List() []Claim {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Claim, 0, len(m.claims))
	for _, claim := range m.claims {
		out = append(out, clone(claim))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (m *Memory) ListOwner(owner string) []Claim {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Claim, 0)
	for _, claim := range m.claims {
		if claim.Owner == owner {
			out = append(out, clone(claim))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func clone(claim Claim) Claim {
	claim.Paths = append([]string(nil), claim.Paths...)
	if claim.ExpiresAt != nil {
		expiresAt := *claim.ExpiresAt
		claim.ExpiresAt = &expiresAt
	}
	return claim
}

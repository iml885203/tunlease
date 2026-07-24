package registry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type Redis struct {
	client  *redis.Client
	prefix  string
	max     int
	allowed []string
	ttl     time.Duration
	log     *slog.Logger
	errMu   sync.RWMutex
	lastErr error
}

func NewRedis(client *redis.Client, prefix string, maxClaims int, allowed []string, ttl time.Duration, logger *slog.Logger) *Redis {
	if prefix == "" {
		prefix = "tunlease"
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Redis{client: client, prefix: prefix, max: maxClaims, allowed: allowed, ttl: ttl, log: logger}
}
func (r *Redis) claimKey(id string) string { return r.prefix + ":claim:" + id }
func (r *Redis) idsKey() string            { return r.prefix + ":claims" }
func (r *Redis) metaKey() string           { return r.prefix + ":claim-meta" }
func (r *Redis) versionKey() string        { return r.prefix + ":version" }
func (r *Redis) lockKey() string           { return r.prefix + ":lock" }

func (r *Redis) withLock(fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	for {
		ok, err := r.client.SetNX(ctx, r.lockKey(), token, 10*time.Second).Result()
		if err != nil {
			return err
		}
		if ok {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	defer r.client.Eval(ctx, `if redis.call("get",KEYS[1]) == ARGV[1] then return redis.call("del",KEYS[1]) end return 0`, []string{r.lockKey()}, token)
	return fn(ctx)
}
func (r *Redis) validate(paths []string) error {
	for _, p := range paths {
		if !ValidPath(p) {
			return ErrInvalidPath
		}
		// An empty allowlist allows every path — the allowlist is opt-in.
		// Configure prefixes to restrict which paths may be claimed.
		ok := len(r.allowed) == 0
		for _, a := range r.allowed {
			if strings.HasPrefix(prefix(p), a) {
				ok = true
				break
			}
		}
		if !ok {
			return &NotAllowed{Path: p}
		}
	}
	return nil
}
func (r *Redis) list(ctx context.Context) ([]Claim, error) {
	ids, err := r.client.SMembers(ctx, r.idsKey()).Result()
	if err != nil {
		return nil, err
	}
	out := make([]Claim, 0, len(ids))
	stale := make([]interface{}, 0)
	for _, id := range ids {
		b, err := r.client.Get(ctx, r.claimKey(id)).Bytes()
		if errors.Is(err, redis.Nil) {
			stale = append(stale, id)
			continue
		}
		if err != nil {
			return nil, err
		}
		var c Claim
		if err := json.Unmarshal(b, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if len(stale) > 0 {
		for _, raw := range stale {
			id := raw.(string)
			if b, getErr := r.client.HGet(ctx, r.metaKey(), id).Bytes(); getErr == nil {
				var c Claim
				if json.Unmarshal(b, &c) == nil {
					r.log.Info("lease audit", "event", "expire", "who", c.Owner, "when", time.Now().UTC(), "paths", c.Paths, "claim_id", id)
				}
			}
		}
		_ = r.client.SRem(ctx, r.idsKey(), stale...).Err()
		_ = r.client.HDel(ctx, r.metaKey(), interfaceStrings(stale)...).Err()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (r *Redis) Create(owner string, paths []string, local string) (Claim, error) {
	if err := r.validate(paths); err != nil {
		return Claim{}, err
	}
	var result Claim
	err := r.withLock(func(ctx context.Context) error {
		claims, err := r.list(ctx)
		if err != nil {
			return err
		}
		for _, c := range claims {
			for _, p := range paths {
				for _, q := range c.Paths {
					if overlap(p, q) {
						return &Conflict{Owner: c.Owner, ExpiresAt: c.ExpiresAt}
					}
				}
			}
		}
		if len(claims) >= r.max {
			return &TooManyClaims{}
		}
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		result = Claim{ID: hex.EncodeToString(b), Owner: owner, Paths: append([]string(nil), paths...), Local: local, ExpiresAt: time.Now().Add(r.ttl).UTC()}
		payload, _ := json.Marshal(result)
		p := r.client.TxPipeline()
		p.Set(ctx, r.claimKey(result.ID), payload, r.ttl)
		p.SAdd(ctx, r.idsKey(), result.ID)
		p.HSet(ctx, r.metaKey(), result.ID, payload)
		p.Incr(ctx, r.versionKey())
		_, err = p.Exec(ctx)
		return err
	})
	if err == nil {
		r.log.Info("lease audit", "event", "claim", "who", owner, "when", time.Now().UTC(), "paths", paths, "claim_id", result.ID)
	}
	return result, err
}
func (r *Redis) Heartbeat(owner, id string) (time.Time, error) {
	var expires time.Time
	err := r.withLock(func(ctx context.Context) error {
		b, err := r.client.Get(ctx, r.claimKey(id)).Bytes()
		if errors.Is(err, redis.Nil) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var c Claim
		if json.Unmarshal(b, &c) != nil || c.Owner != owner {
			return ErrNotFound
		}
		expires = time.Now().Add(r.ttl).UTC()
		c.ExpiresAt = expires
		payload, _ := json.Marshal(c)
		p := r.client.TxPipeline()
		p.Set(ctx, r.claimKey(id), payload, r.ttl)
		p.HSet(ctx, r.metaKey(), id, payload)
		p.Incr(ctx, r.versionKey())
		_, err = p.Exec(ctx)
		return err
	})
	return expires, err
}
func (r *Redis) Release(owner, id string, admin bool) error {
	var released *Claim
	err := r.withLock(func(ctx context.Context) error {
		b, err := r.client.Get(ctx, r.claimKey(id)).Bytes()
		if errors.Is(err, redis.Nil) {
			return nil
		}
		if err != nil {
			return err
		}
		var c Claim
		if err := json.Unmarshal(b, &c); err != nil {
			return err
		}
		if c.Owner != owner && !admin {
			return errors.New("forbidden")
		}
		released = &c
		p := r.client.TxPipeline()
		p.Del(ctx, r.claimKey(id))
		p.SRem(ctx, r.idsKey(), id)
		p.HDel(ctx, r.metaKey(), id)
		p.Incr(ctx, r.versionKey())
		_, err = p.Exec(ctx)
		return err
	})
	if err == nil && released != nil {
		r.log.Info("lease audit", "event", "release", "who", owner, "when", time.Now().UTC(), "paths", released.Paths, "claim_id", id)
	}
	return err
}

func interfaceStrings(values []interface{}) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value.(string))
	}
	return out
}
func (r *Redis) List() []Claim {
	c, e := r.list(context.Background())
	r.errMu.Lock()
	r.lastErr = e
	r.errMu.Unlock()
	if e != nil {
		r.log.Error("redis list claims", "error", e)
		return nil
	}
	return c
}
func (r *Redis) LastError() error {
	r.errMu.RLock()
	defer r.errMu.RUnlock()
	return r.lastErr
}
func (r *Redis) ReleaseByPath(owner, path string) error {
	for _, c := range r.List() {
		if c.Owner == owner {
			for _, p := range c.Paths {
				if p == path {
					return r.Release(owner, c.ID, false)
				}
			}
		}
	}
	return nil
}
func (r *Redis) ReleaseByLocalPort(owner string, port int) []Claim {
	var out []Claim
	for _, c := range r.List() {
		if c.Owner == owner && strings.HasSuffix(c.Local, fmt.Sprintf(":%d", port)) {
			out = append(out, c)
			_ = r.Release(owner, c.ID, false)
		}
	}
	return out
}
func (r *Redis) Version() uint64 {
	claims := r.List()
	h := fnv.New64a()
	for _, c := range claims {
		_, _ = fmt.Fprintf(h, "%s|%s|%d|", c.ID, c.Owner, c.ExpiresAt.UnixNano())
		for _, p := range c.Paths {
			_, _ = h.Write([]byte(p))
			_, _ = h.Write([]byte{0})
		}
	}
	return h.Sum64()
}

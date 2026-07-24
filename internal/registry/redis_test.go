package registry

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisLifecycleConflictAndTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	r := NewRedis(client, "test", 64, []string{"/webhooks/"}, time.Minute, nil)
	a, err := r.Create("alice", []string{"/webhooks/a/*"}, "localhost:1")
	if err != nil || a.ID == "" {
		t.Fatalf("create: %#v %v", a, err)
	}
	if _, err = r.Create("bob", []string{"/webhooks/a/deep/*"}, ""); err == nil {
		t.Fatal("expected overlap conflict")
	}
	v := r.Version()
	mr.FastForward(time.Minute + time.Second)
	if got := r.List(); len(got) != 0 {
		t.Fatalf("expired claims: %#v", got)
	}
	if r.Version() == v {
		t.Fatal("version did not change after native Redis expiry")
	}
	b, err := r.Create("bob", []string{"/webhooks/b/*"}, "")
	if err != nil || b.ID == "" {
		t.Fatalf("slot not freed: %#v %v", b, err)
	}
	if _, err = r.Heartbeat("bob", b.ID); err != nil {
		t.Fatal(err)
	}
	if err = r.Release("bob", b.ID, false); err != nil {
		t.Fatal(err)
	}
	if n, _ := client.Exists(context.Background(), r.claimKey(b.ID)).Result(); n != 0 {
		t.Fatal("claim key remains")
	}
}

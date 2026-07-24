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
	r := NewRedis(client, "test", 42000, 42001, []string{"/webhooks/"}, time.Minute, nil)
	a, err := r.Create("alice", []string{"/webhooks/a/*"}, "localhost:1")
	if err != nil || a.RemotePort != 42000 {
		t.Fatalf("create: %#v %v", a, err)
	}
	if !r.OwnsPort("alice", 42000) || r.OwnsPort("bob", 42000) {
		t.Fatal("owner port ACL incorrect")
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
	if err != nil || b.RemotePort != 42000 {
		t.Fatalf("port not reused: %#v %v", b, err)
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

package auth

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newClient(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return c, mr
}

func TestLimiter_RPM(t *testing.T) {
	c, _ := newClient(t)
	l := NewLimiter(c)
	// advance a fake clock so the minute bucket is deterministic
	now := time.Unix(1_700_000_000, 0).UTC()
	l.now = func() time.Time { return now }

	for i := 0; i < 2; i++ {
		if ok, _ := l.AllowRPM(context.Background(), "k1", 2); !ok {
			t.Fatalf("request %d should be allowed", i)
		}
	}
	if ok, retry := l.AllowRPM(context.Background(), "k1", 2); ok {
		t.Error("3rd request should be denied")
	} else if retry <= 0 {
		t.Errorf("retryAfter should be > 0, got %d", retry)
	}
}

func TestLimiter_Unlimited(t *testing.T) {
	c, _ := newClient(t)
	l := NewLimiter(c)
	for i := 0; i < 100; i++ {
		if ok, _ := l.AllowRPM(context.Background(), "k", 0); !ok {
			t.Fatal("limit 0 means unlimited")
		}
	}
}

func TestQuota_DeductAndBalance(t *testing.T) {
	c, _ := newClient(t)
	q := NewQuota(c, nil) // no DB; seed redis directly
	ctx := context.Background()

	// seed balance = 100
	c.Set(ctx, "quota:bal:user1", 100, 0)

	got, err := q.Balance(ctx, "user1")
	if err != nil || got != 100 {
		t.Fatalf("balance = %d err=%v", got, err)
	}
	if ok, _ := q.HasCredit(ctx, "user1"); !ok {
		t.Error("should have credit")
	}
	if err := q.Deduct(ctx, "user1", 30); err != nil {
		t.Fatal(err)
	}
	got, _ = q.Balance(ctx, "user1")
	if got != 70 {
		t.Errorf("after deduct 30: balance = %d want 70", got)
	}
	// overdraw
	if err := q.Deduct(ctx, "user1", 100); err != nil {
		t.Fatal(err)
	}
	got, _ = q.Balance(ctx, "user1")
	if got != -30 {
		t.Errorf("overdraw balance = %d want -30", got)
	}
	if ok, _ := q.HasCredit(ctx, "user1"); ok {
		t.Error("should be out of credit after overdraw")
	}
}

func TestQuota_ZeroDeductNoop(t *testing.T) {
	c, _ := newClient(t)
	q := NewQuota(c, nil)
	c.Set(context.Background(), "quota:bal:u", 5, 0)
	if err := q.Deduct(context.Background(), "u", 0); err != nil {
		t.Fatal(err)
	}
	got, _ := q.Balance(context.Background(), "u")
	if got != 5 {
		t.Errorf("zero deduct should not change balance: %d", got)
	}
}

func TestIdentity_Whitelist(t *testing.T) {
	id := &Identity{Whitelist: nil}
	if !id.CanUseModel("any") {
		t.Error("empty whitelist should allow all")
	}
	id2 := &Identity{Whitelist: []string{"a", "b"}}
	if !id2.CanUseModel("a") || id2.CanUseModel("c") {
		t.Error("whitelist enforcement wrong")
	}
}

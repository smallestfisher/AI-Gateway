// Package health implements the circuit-breaker / health store backing the
// router's Health interface. It records per-(provider, upstream_model) samples
// into Redis sliding windows and trips the circuit when the error rate or the
// slow-TTFT rate exceeds the provider's thresholds. Recovery is automatic: the
// circuit stays open for a cooldown, then re-closes (the next request is the
// probe; a failed probe re-opens it). See docs/02-modules.md §5.
package health

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/aigateway/ai-hub/internal/registry"

	"github.com/redis/go-redis/v9"
)

// Sample is one observed request outcome.
type Sample struct {
	Success   bool
	TTFTMs    int // time to first token (-ish); 0 if unknown
	LatencyMs int
}

// Stats is a point-in-time window summary (for the admin health panel).
type Stats struct {
	Total      int
	Failures   int
	Slow       int
	ErrorRate  float64
	SlowRate   float64
	Open       bool
	OpenedAgoS float64 // seconds since the circuit opened (0 if closed)
}

// Store is a Redis-backed health store. It implements router.Health
// (Available) and egress's Recorder (Record).
type Store struct {
	rdb      redis.Cmdable
	defaults registry.HealthDefaults
	// lookup returns the current provider config (for per-provider thresholds),
	// or nil if unknown. Reads the registry's atomic snapshot — cheap.
	lookup func(providerID string) *registry.Provider
	now    func() float64 // unix seconds; injectable for tests
	seq    uint64
}

// Option configures a Store.
type Option func(*Store)

// WithClock sets the clock used for window scoring (tests).
func WithClock(f func() float64) Option {
	return func(s *Store) { if f != nil { s.now = f } }
}

// New creates a Store.
func New(rdb redis.Cmdable, defaults registry.HealthDefaults, lookup func(string) *registry.Provider, opts ...Option) *Store {
	if defaults.ErrorRate == 0 {
		defaults.ErrorRate = 0.3
	}
	if defaults.P95TTFTMs == 0 {
		defaults.P95TTFTMs = 8000
	}
	if defaults.WindowSec == 0 {
		defaults.WindowSec = 60
	}
	if defaults.CooldownSec == 0 {
		defaults.CooldownSec = 30
	}
	if defaults.MinSamples == 0 {
		defaults.MinSamples = 5
	}
	if defaults.SlowRate == 0 {
		defaults.SlowRate = 0.5
	}
	s := &Store{rdb: rdb, defaults: defaults, lookup: lookup, now: realClock}
	for _, o := range opts {
		o(s)
	}
	return s
}

func realClock() float64 { return float64(time.Now().UnixNano()) / 1e9 }

// thresholds resolves the effective config for a provider.
func (s *Store) thresholds(providerID string) registry.HealthDefaults {
	if s.lookup != nil {
		if p := s.lookup(providerID); p != nil {
			return p.HealthConfig(s.defaults)
		}
	}
	return s.defaults
}

// Available reports whether the circuit for (providerID, model) is closed.
// Implements router.Health.
func (s *Store) Available(providerID, model string) bool {
	ctx := context.Background()
	opened, ok, err := s.openedAt(ctx, providerID, model)
	if err != nil || !ok {
		return true // no state => closed
	}
	cfg := s.thresholds(providerID)
	return s.now()-opened >= float64(cfg.CooldownSec) // closed once cooldown elapses
}

// Record observes a sample and may trip the circuit. It satisfies
// egress.Recorder.
func (s *Store) Record(ctx context.Context, providerID, model string, success bool, ttftMs, latencyMs int) error {
	cfg := s.thresholds(providerID)
	now := s.now()
	windowStart := now - float64(cfg.WindowSec)

	id := atomic.AddUint64(&s.seq, 1)
	member := fmt.Sprintf("%d:%d", int64(now*1000), id)

	pipe := s.rdb.Pipeline()
	pipe.ZAdd(ctx, totalKey(providerID, model), redis.Z{Score: now, Member: member})
	if !success {
		pipe.ZAdd(ctx, failKey(providerID, model), redis.Z{Score: now, Member: member})
	}
	if ttftMs > cfg.P95TTFTMs && ttftMs > 0 {
		pipe.ZAdd(ctx, slowKey(providerID, model), redis.Z{Score: now, Member: member})
	}
	trim := func(key string) {
		pipe.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatFloat(windowStart, 'f', -1, 64))
	}
	trim(totalKey(providerID, model))
	trim(failKey(providerID, model))
	trim(slowKey(providerID, model))
	// cleanup TTL so keys don't leak forever
	ttl := time.Duration(cfg.WindowSec*2) * time.Second
	pipe.Expire(ctx, totalKey(providerID, model), ttl)
	pipe.Expire(ctx, failKey(providerID, model), ttl)
	pipe.Expire(ctx, slowKey(providerID, model), ttl)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return err
	}

	windowStartStr := strconv.FormatFloat(windowStart, 'f', -1, 64)
	totalN, err := s.rdb.ZCount(ctx, totalKey(providerID, model), windowStartStr, "+inf").Result()
	if err != nil {
		return err
	}
	if int(totalN) < cfg.MinSamples {
		return nil // not enough data to judge
	}
	failN, _ := s.rdb.ZCount(ctx, failKey(providerID, model), windowStartStr, "+inf").Result()
	if float64(totalN) > 0 && float64(failN)/float64(totalN) >= cfg.ErrorRate {
		return s.open(ctx, providerID, model, now, cfg)
	}
	slowN, _ := s.rdb.ZCount(ctx, slowKey(providerID, model), windowStartStr, "+inf").Result()
	if float64(totalN) > 0 && float64(slowN)/float64(totalN) >= cfg.SlowRate {
		return s.open(ctx, providerID, model, now, cfg)
	}
	return nil
}

func (s *Store) open(ctx context.Context, providerID, model string, now float64, cfg registry.HealthDefaults) error {
	val := strconv.FormatFloat(now, 'f', -1, 64)
	ttl := time.Duration(cfg.CooldownSec*2) * time.Second
	if ttl <= 0 {
		ttl = time.Minute
	}
	return s.rdb.Set(ctx, stateKey(providerID, model), val, ttl).Err()
}

// openedAt returns the unix-seconds timestamp the circuit opened at.
func (s *Store) openedAt(ctx context.Context, providerID, model string) (float64, bool, error) {
	val, err := s.rdb.Get(ctx, stateKey(providerID, model)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, false, nil
		}
		return 0, false, err
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return 0, false, err
	}
	return f, true, nil
}

// Stats returns a window summary (for the admin panel).
func (s *Store) Stats(ctx context.Context, providerID, model string) (Stats, error) {
	cfg := s.thresholds(providerID)
	now := s.now()
	windowStart := strconv.FormatFloat(now-float64(cfg.WindowSec), 'f', -1, 64)
	var st Stats
	t, err := s.rdb.ZCount(ctx, totalKey(providerID, model), windowStart, "+inf").Result()
	if err != nil {
		return st, err
	}
	f, _ := s.rdb.ZCount(ctx, failKey(providerID, model), windowStart, "+inf").Result()
	sl, _ := s.rdb.ZCount(ctx, slowKey(providerID, model), windowStart, "+inf").Result()
	st.Total, st.Failures, st.Slow = int(t), int(f), int(sl)
	if st.Total > 0 {
		st.ErrorRate = float64(st.Failures) / float64(st.Total)
		st.SlowRate = float64(st.Slow) / float64(st.Total)
	}
	if opened, ok, _ := s.openedAt(ctx, providerID, model); ok {
		st.OpenedAgoS = now - opened
		st.Open = st.OpenedAgoS < float64(cfg.CooldownSec)
	}
	return st, nil
}

// --- key helpers ---

func totalKey(p, m string) string { return "gw:health:" + p + ":" + m + ":total" }
func failKey(p, m string) string  { return "gw:health:" + p + ":" + m + ":fail" }
func slowKey(p, m string) string  { return "gw:health:" + p + ":" + m + ":slow" }
func stateKey(p, m string) string { return "gw:health:" + p + ":" + m + ":state" }

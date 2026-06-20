package health

import (
	"context"
	"testing"

	"github.com/aigateway/ai-hub/internal/registry"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestStore(t *testing.T, defaults registry.HealthDefaults, clk *float64) (*Store, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	*clk = 1000
	return New(rdb, defaults, nil, WithClock(func() float64 { return *clk })), mr
}

func TestInitiallyClosed(t *testing.T) {
	var clk float64
	s, _ := newTestStore(t, registry.HealthDefaults{MinSamples: 4, ErrorRate: 0.5}, &clk)
	if !s.Available("p", "m") {
		t.Error("should be closed with no samples")
	}
}

func TestCircuitOpensOnErrorRate(t *testing.T) {
	var clk float64
	s, _ := newTestStore(t, registry.HealthDefaults{MinSamples: 4, ErrorRate: 0.5, CooldownSec: 30, WindowSec: 60}, &clk)
	// 3 failures: under minSamples -> still closed
	for i := 0; i < 3; i++ {
		if err := s.Record(context.Background(), "p", "m", false, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	if !s.Available("p", "m") {
		t.Error("should stay closed below minSamples")
	}
	// 4th failure -> error rate 100% >= 0.5 -> open
	if err := s.Record(context.Background(), "p", "m", false, 0, 0); err != nil {
		t.Fatal(err)
	}
	if s.Available("p", "m") {
		t.Error("should be open after error rate exceeds threshold")
	}
}

func TestAutoRecoverAfterCooldown(t *testing.T) {
	var clk float64
	s, _ := newTestStore(t, registry.HealthDefaults{MinSamples: 2, ErrorRate: 0.5, CooldownSec: 30, WindowSec: 60}, &clk)
	for i := 0; i < 2; i++ {
		_ = s.Record(context.Background(), "p", "m", false, 0, 0)
	}
	if s.Available("p", "m") {
		t.Fatal("should be open")
	}
	clk += 31 // cooldown elapses
	if !s.Available("p", "m") {
		t.Error("should auto-recover (close) after cooldown")
	}
}

func TestStaysClosedOnSuccess(t *testing.T) {
	var clk float64
	s, _ := newTestStore(t, registry.HealthDefaults{MinSamples: 3, ErrorRate: 0.3}, &clk)
	for i := 0; i < 10; i++ {
		_ = s.Record(context.Background(), "p", "m", true, 100, 0)
	}
	if !s.Available("p", "m") {
		t.Error("should stay closed on all-success traffic")
	}
}

func TestSlowRateTrips(t *testing.T) {
	var clk float64
	s, _ := newTestStore(t, registry.HealthDefaults{
		MinSamples: 4, ErrorRate: 0.99, P95TTFTMs: 200, SlowRate: 0.5,
		CooldownSec: 30, WindowSec: 60,
	}, &clk)
	// all succeed but are slow (TTFT 500 > 200) -> slow rate 100% >= 0.5
	for i := 0; i < 4; i++ {
		_ = s.Record(context.Background(), "p", "m", true, 500, 0)
	}
	if s.Available("p", "m") {
		t.Error("slow-rate should trip the circuit")
	}
}

func TestPerProviderThresholds(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	var clk float64 = 1000
	// global default trips at 50%; provider "strict" trips at 10%.
	lookup := func(id string) *registry.Provider {
		if id == "strict" {
			return &registry.Provider{HealthErrorRate: 0.1, HealthMinSamples: 2, HealthWindowSec: 60, HealthCooldown: 30}
		}
		return nil
	}
	s := New(rdb, registry.HealthDefaults{MinSamples: 5, ErrorRate: 0.5}, lookup, WithClock(func() float64 { return clk }))

	// strict: 2 failures @ 10% threshold -> 100% rate >= 0.1 -> open with only 2 samples
	for i := 0; i < 2; i++ {
		_ = s.Record(context.Background(), "strict", "m", false, 0, 0)
	}
	if s.Available("strict", "m") {
		t.Error("strict provider should trip at its own minSamples/threshold")
	}
	// default provider: 2 failures but default minSamples=5 -> still closed
	for i := 0; i < 2; i++ {
		_ = s.Record(context.Background(), "default", "m", false, 0, 0)
	}
	if !s.Available("default", "m") {
		t.Error("default provider should stay closed below its minSamples")
	}
}

func TestStats(t *testing.T) {
	var clk float64
	s, _ := newTestStore(t, registry.HealthDefaults{MinSamples: 10, ErrorRate: 0.99, P95TTFTMs: 200, WindowSec: 60}, &clk)
	for i := 0; i < 6; i++ {
		_ = s.Record(context.Background(), "p", "m", i%2 == 0, 500, 0)
	}
	st, err := s.Stats(context.Background(), "p", "m")
	if err != nil {
		t.Fatal(err)
	}
	if st.Total != 6 {
		t.Errorf("total = %d", st.Total)
	}
	if st.Failures != 3 {
		t.Errorf("failures = %d", st.Failures)
	}
	if st.Slow != 6 {
		t.Errorf("slow = %d (all TTFT 500 > 200)", st.Slow)
	}
	if st.Open {
		t.Error("should not be open (threshold 0.99)")
	}
}

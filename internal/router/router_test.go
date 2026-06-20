package router

import (
	"errors"
	"testing"

	"github.com/aigateway/ai-hub/internal/registry"
)

func snap(chs ...*registry.Channel) registry.Source {
	b := registry.NewBuilder()
	for _, c := range chs {
		b.AddChannel(c)
	}
	return registry.NewStatic(b.Build())
}

func ch(alias, pid, model string, weight, priority int) *registry.Channel {
	return &registry.Channel{
		Alias:         alias,
		UpstreamModel: model,
		Weight:        weight,
		Priority:      priority,
		Provider:      &registry.Provider{ID: pid, Name: pid},
	}
}

func TestResolve_PriorityOrdering(t *testing.T) {
	// A priority 0 (higher), B priority 1 (lower). failover -> A first.
	src := snap(
		ch("m", "B", "um", 1, 1),
		ch("m", "A", "um", 1, 0),
	)
	r := New(src)
	got, _, err := r.Resolve("m")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Provider.ID != "A" {
		t.Errorf("priority order wrong: %+v", ids(got))
	}
}

func TestResolve_FailoverWeightTiebreak(t *testing.T) {
	// same priority, weights 1 and 9 -> heavier first in failover.
	src := snap(
		ch("m", "lo", "um", 1, 0),
		ch("m", "hi", "um", 9, 0),
	)
	r := New(src)
	got, _, _ := r.Resolve("m")
	if got[0].Provider.ID != "hi" {
		t.Errorf("weight tiebreak wrong: %+v", ids(got))
	}
}

func TestResolve_WeightedUsesRandom(t *testing.T) {
	src := snap(
		ch("m", "lo", "um", 1, 0),
		ch("m", "hi", "um", 9, 0),
	)
	// fixed rand always returns 0.5; with weights 1 and 9:
	// key_lo = 0.5^(1/1)=0.5 ; key_hi = 0.5^(1/9)=~0.926 -> hi first.
	r := New(src, WithRand(func() float64 { return 0.5 }))
	got, _, _ := r.Resolve("m")
	if got[0].Provider.ID != "hi" {
		t.Errorf("weighted expected hi first, got %+v", ids(got))
	}
}

func TestResolve_HealthFiltering(t *testing.T) {
	src := snap(
		ch("m", "A", "um", 1, 0),
		ch("m", "B", "um", 1, 0),
	)
	h := stubHealth{"B": false} // B is circuit-open
	r := New(src, WithHealth(h))
	got, _, err := r.Resolve("m")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Provider.ID != "A" {
		t.Errorf("health filter wrong: %+v", ids(got))
	}
}

func TestResolve_NoChannel(t *testing.T) {
	src := snap(ch("other", "A", "um", 1, 0))
	r := New(src)
	_, _, err := r.Resolve("missing")
	if !errors.Is(err, ErrNoChannel) {
		t.Errorf("want ErrNoChannel, got %v", err)
	}
}

func TestResolve_NoChannelAfterHealthFilter(t *testing.T) {
	src := snap(ch("m", "A", "um", 1, 0))
	r := New(src, WithHealth(stubHealth{"A": false}))
	_, _, err := r.Resolve("m")
	if !errors.Is(err, ErrNoChannel) {
		t.Errorf("want ErrNoChannel, got %v", err)
	}
}

func TestResolve_PolicyFallback(t *testing.T) {
	// no policy set -> default failover.
	src := snap(ch("m", "A", "um", 1, 0))
	r := New(src)
	_, p, _ := r.Resolve("m")
	if p == nil || p.Mode != "failover" {
		t.Errorf("default policy = %+v", p)
	}
}

// --- helpers ---

type stubHealth map[string]bool

func (s stubHealth) Available(providerID, _ string) bool {
	avail, ok := s[providerID]
	if !ok {
		return true // unspecified = available
	}
	return avail
}

func ids(chs []*registry.Channel) []string {
	out := make([]string, len(chs))
	for i, c := range chs {
		out[i] = c.Provider.ID
	}
	return out
}

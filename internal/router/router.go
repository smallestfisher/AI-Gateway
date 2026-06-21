// Package router resolves a model alias into an ordered list of candidate
// channels, applying health filtering and the configured selection policy
// (failover / weighted / auto). The returned list is ordered best-first so the
// caller can attempt channels in order for failover. See docs/02-modules.md §2.
package router

import (
	"errors"
	"math"
	"math/rand"
	"sort"

	"github.com/aigateway/ai-hub/internal/registry"
)

// Health reports whether a (provider, model) channel is currently usable.
// The real implementation (Redis-backed circuit breaker) lands in Phase 3;
// NoopHealth admits everything.
type Health interface {
	Available(providerID, upstreamModel string) bool
}

// NoopHealth admits all channels.
type NoopHealth struct{}

// Available always returns true.
func (NoopHealth) Available(string, string) bool { return true }

// Router resolves aliases to ordered candidate channels.
type Router struct {
	source registry.Source
	health Health
	// rand returns a float in [0,1); injectable for deterministic tests.
	rand func() float64
}

// Option configures a Router.
type Option func(*Router)

// WithHealth sets the health checker.
func WithHealth(h Health) Option {
	return func(r *Router) {
		if h != nil {
			r.health = h
		}
	}
}

// WithRand sets the random source used for weighted selection.
func WithRand(f func() float64) Option {
	return func(r *Router) {
		if f != nil {
			r.rand = f
		}
	}
}

// New creates a Router over the given config source.
func New(src registry.Source, opts ...Option) *Router {
	r := &Router{source: src, health: NoopHealth{}, rand: rand.Float64}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Snapshot returns the current configuration snapshot.
func (r *Router) Snapshot() (*registry.Snapshot, error) {
	return r.source.Snapshot()
}

// ErrNoChannel means the alias has no usable candidates.
var ErrNoChannel = errors.New("router: no available channel for alias")

// Resolve returns candidate channels for alias, ordered best-first, plus the
// effective policy. Channels filtered out by health are excluded.
func (r *Router) Resolve(alias string) ([]*registry.Channel, *registry.Policy, error) {
	snap, err := r.source.Snapshot()
	if err != nil {
		return nil, nil, err
	}
	all := snap.ChannelsFor(alias)
	policy := snap.PolicyFor(alias)

	// health filter
	cands := make([]*registry.Channel, 0, len(all))
	for _, ch := range all {
		if r.health.Available(ch.Provider.ID, ch.UpstreamModel) {
			cands = append(cands, ch)
		}
	}
	if len(cands) == 0 {
		return nil, policy, ErrNoChannel
	}
	return r.order(cands, policy), policy, nil
}

// order applies the policy to produce a best-first list.
//
//   - failover: priority asc, then weight desc (deterministic).
//   - weighted / auto: priority tiers; within a tier, weighted-random ordering
//     (Efraimidis-Spirakis weighted sampling) using r.rand.
func (r *Router) order(cands []*registry.Channel, policy *registry.Policy) []*registry.Channel {
	mode := "failover"
	if policy != nil && policy.Mode != "" {
		mode = policy.Mode
	}

	byPriority := tierByPriority(cands)
	out := make([]*registry.Channel, 0, len(cands))
	for _, tier := range byPriority {
		if mode == "failover" {
			sort.SliceStable(tier, func(i, j int) bool { return tier[i].Weight > tier[j].Weight })
			out = append(out, tier...)
			continue
		}
		out = append(out, r.weightedShuffle(tier)...)
	}
	return out
}

// weightedShuffle returns tier reordered by weighted sampling (heavier weight
// more likely first), without replacement.
func (r *Router) weightedShuffle(tier []*registry.Channel) []*registry.Channel {
	type pick struct {
		ch  *registry.Channel
		key float64
	}
	picks := make([]pick, len(tier))
	for i, ch := range tier {
		w := ch.Weight
		if w <= 0 {
			w = 1
		}
		// key = rand()^(1/w); larger is better. Avoid log(0).
		u := r.rand()
		if u <= 0 {
			u = 1e-12
		}
		picks[i] = pick{ch: ch, key: math.Pow(u, 1.0/float64(w))}
	}
	sort.SliceStable(picks, func(i, j int) bool { return picks[i].key > picks[j].key })
	res := make([]*registry.Channel, len(tier))
	for i, p := range picks {
		res[i] = p.ch
	}
	return res
}

// tierByPriority groups channels by priority and returns tiers in priority-asc order.
func tierByPriority(cands []*registry.Channel) [][]*registry.Channel {
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Priority < cands[j].Priority })
	var tiers [][]*registry.Channel
	for _, ch := range cands {
		if len(tiers) == 0 || tiers[len(tiers)-1][0].Priority != ch.Priority {
			tiers = append(tiers, []*registry.Channel{ch})
		} else {
			tiers[len(tiers)-1] = append(tiers[len(tiers)-1], ch)
		}
	}
	return tiers
}

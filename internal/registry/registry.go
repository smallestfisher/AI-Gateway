// Package registry holds runtime configuration: providers, model->channel
// bindings, client profiles, and routing policies. It is the in-memory
// (hot-path) view; a Source loads it from PostgreSQL in production and from a
// static builder in dev/tests. Reads are O(1) in memory; the DB is never hit
// on the request hot path. See docs/02-modules.md §4, docs/03-database.md.
package registry

import (
	"sort"
	"time"

	"github.com/aigateway/ai-hub/internal/adapter"
)

// Provider is a single upstream channel (runtime, decrypted, in-memory).
type Provider struct {
	ID             string
	Name           string
	Protocol       adapter.Protocol
	BaseURL        string
	APIKey         string
	ProxyURL       string
	Timeout        time.Duration
	ConnectTimeout time.Duration
	MaxRetries     int
	// Extra headers applied on egress (e.g. anthropic-version, custom tokens).
	Headers map[string]string

	// Health-check thresholds (zero values fall back to package defaults in
	// internal/health). See docs/03-database.md (providers.hc_*).
	HealthErrorRate float64 // e.g. 0.3
	HealthP95TTFTMs int     // e.g. 8000
	HealthWindowSec int     // e.g. 60
	HealthCooldown  int     // seconds the circuit stays open, e.g. 30
	HealthMinSamples int    // minimum samples before tripping, e.g. 5
}

// HealthConfig returns this provider's thresholds with zero fields replaced by
// the provided defaults.
func (p *Provider) HealthConfig(def HealthDefaults) HealthDefaults {
	d := def
	if p.HealthErrorRate > 0 {
		d.ErrorRate = p.HealthErrorRate
	}
	if p.HealthP95TTFTMs > 0 {
		d.P95TTFTMs = p.HealthP95TTFTMs
	}
	if p.HealthWindowSec > 0 {
		d.WindowSec = p.HealthWindowSec
	}
	if p.HealthCooldown > 0 {
		d.CooldownSec = p.HealthCooldown
	}
	if p.HealthMinSamples > 0 {
		d.MinSamples = p.HealthMinSamples
	}
	return d
}

// HealthDefaults are circuit-breaker thresholds.
type HealthDefaults struct {
	ErrorRate   float64
	P95TTFTMs   int
	WindowSec   int
	CooldownSec int
	MinSamples  int
	// SlowRate is the fraction of requests whose TTFT exceeds P95TTFTMs above
	// which the circuit trips (latency-based). Defaults to 0.5 when zero.
	SlowRate float64
}

// Channel is a resolved binding: an alias served by a (Provider, upstream model).
type Channel struct {
	Alias         string
	Provider      *Provider
	UpstreamModel string
	Weight        int
	Priority      int
	Profile       *ClientProfile
}

// ClientProfile is the egress impersonation bundle (UA/Origin/Referer/headers).
type ClientProfile struct {
	Headers            map[string]string
	UserAgent          string
	Origin             string
	Referer            string
	StripClientHeaders bool
}

// ApplyTo merges profile headers/UA/Origin/Referer into the given header set.
func (p *ClientProfile) ApplyTo(h map[string]string) {
	if p == nil {
		return
	}
	set := func(k, v string) {
		if v != "" {
			h[k] = v
		}
	}
	set("User-Agent", p.UserAgent)
	set("Origin", p.Origin)
	set("Referer", p.Referer)
	for k, v := range p.Headers {
		h[k] = v
	}
}

// Policy controls routing for a model (or globally).
type Policy struct {
	Mode        string // failover | weighted | auto
	MaxAttempts int    // 0 = unlimited within candidate set
}

// Snapshot is an immutable view of configuration. The registry atomically
// swaps whole snapshots (copy-on-write) so readers need no locks.
type Snapshot struct {
	// Channels maps alias -> candidate channels (order within is whatever the
	// Source produced; the Router re-orders on Resolve).
	Channels map[string][]*Channel
	// Policies maps alias -> policy; the key "" holds the global default.
	Policies map[string]*Policy
	// Providers maps providerID -> provider (for health-threshold lookups, etc.).
	Providers map[string]*Provider
}

// ProviderByID returns the provider, or nil.
func (s *Snapshot) ProviderByID(id string) *Provider {
	if s == nil || s.Providers == nil {
		return nil
	}
	return s.Providers[id]
}

// ChannelsFor returns candidates for an alias (empty if unknown).
func (s *Snapshot) ChannelsFor(alias string) []*Channel {
	if s == nil || s.Channels == nil {
		return nil
	}
	return s.Channels[alias]
}

// PolicyFor returns the policy for an alias, falling back to global then a default.
func (s *Snapshot) PolicyFor(alias string) *Policy {
	if s == nil {
		return defaultPolicy()
	}
	if p, ok := s.Policies[alias]; ok && p != nil {
		return p
	}
	if p, ok := s.Policies[""]; ok && p != nil {
		return p
	}
	return defaultPolicy()
}

func defaultPolicy() *Policy {
	return &Policy{Mode: "failover", MaxAttempts: 0}
}

// SnapshotBuilder builds a Snapshot conveniently (used by Static and, later,
// the DB loader).
type SnapshotBuilder struct {
	channels map[string][]*Channel
	policies map[string]*Policy
}

// NewBuilder creates an empty builder.
func NewBuilder() *SnapshotBuilder {
	return &SnapshotBuilder{
		channels: map[string][]*Channel{},
		policies: map[string]*Policy{},
	}
}

// AddChannel registers a candidate channel for an alias.
func (b *SnapshotBuilder) AddChannel(ch *Channel) *SnapshotBuilder {
	b.channels[ch.Alias] = append(b.channels[ch.Alias], ch)
	return b
}

// SetPolicy sets the policy for an alias ("" = global).
func (b *SnapshotBuilder) SetPolicy(alias string, p *Policy) *SnapshotBuilder {
	b.policies[alias] = p
	return b
}

// Build returns an immutable snapshot. Channels are pre-sorted by priority then
// weight so a failover read is sane even before the Router re-orders.
func (b *SnapshotBuilder) Build() *Snapshot {
	providers := map[string]*Provider{}
	for alias := range b.channels {
		sort.SliceStable(b.channels[alias], func(i, j int) bool {
			ci, cj := b.channels[alias][i], b.channels[alias][j]
			if ci.Priority != cj.Priority {
				return ci.Priority < cj.Priority
			}
			return ci.Weight > cj.Weight
		})
		for _, ch := range b.channels[alias] {
			if ch.Provider != nil {
				providers[ch.Provider.ID] = ch.Provider
			}
		}
	}
	return &Snapshot{Channels: b.channels, Policies: b.policies, Providers: providers}
}

// Source provides the current configuration snapshot.
type Source interface {
	Snapshot() (*Snapshot, error)
}

// Static is an in-memory Source (dev / tests / env-seeded bootstrap). The DB
// source arrives in Phase 3.
type Static struct {
	snap *Snapshot
}

// NewStatic wraps a prebuilt snapshot as a Source.
func NewStatic(snap *Snapshot) *Static { return &Static{snap: snap} }

// Snapshot returns the static snapshot.
func (s *Static) Snapshot() (*Snapshot, error) { return s.snap, nil }

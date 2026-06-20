// Package registrydb loads runtime configuration from PostgreSQL into a
// registry.Snapshot and keeps it hot via Redis pub/sub invalidation. It is the
// production Source for the Router/Egress; reads stay in-memory (the DB is
// never hit on the request hot path). See docs/02-modules.md §4, docs/03-database.md.
package registrydb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aigateway/ai-hub/internal/adapter"
	"github.com/aigateway/ai-hub/internal/registry"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DecryptFunc decrypts an api_key_enc blob. The default (identity) treats the
// stored bytes as the key directly — fine for dev; production wires a
// KMS-backed implementation when the admin layer (which encrypts on write) lands.
type DecryptFunc func([]byte) (string, error)

// Load reads all enabled configuration and builds an immutable snapshot.
func Load(ctx context.Context, pool *pgxpool.Pool, decrypt DecryptFunc) (*registry.Snapshot, error) {
	if decrypt == nil {
		decrypt = func(b []byte) (string, error) { return string(b), nil }
	}

	proxies, err := loadProxies(ctx, pool)
	if err != nil {
		return nil, fmt.Errorf("registrydb: load proxies: %w", err)
	}
	providers, err := loadProviders(ctx, pool, decrypt, proxies)
	if err != nil {
		return nil, fmt.Errorf("registrydb: load providers: %w", err)
	}
	aliasByModelID, err := loadModels(ctx, pool)
	if err != nil {
		return nil, fmt.Errorf("registrydb: load models: %w", err)
	}
	def, byProvider, byModel, err := loadProfiles(ctx, pool)
	if err != nil {
		return nil, fmt.Errorf("registrydb: load profiles: %w", err)
	}
	policies, err := loadPolicies(ctx, pool, aliasByModelID)
	if err != nil {
		return nil, fmt.Errorf("registrydb: load policies: %w", err)
	}

	b := registry.NewBuilder()
	for alias, p := range policies {
		b.SetPolicy(alias, p)
	}
	if err := loadChannels(ctx, pool, providers, aliasByModelID, def, byProvider, byModel, b); err != nil {
		return nil, fmt.Errorf("registrydb: load channels: %w", err)
	}
	return b.Build(), nil
}

func loadProxies(ctx context.Context, pool *pgxpool.Pool) (map[string]string, error) {
	rows, err := pool.Query(ctx, `SELECT id, url FROM proxy_egress WHERE enabled=true`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var id, url string
		if err := rows.Scan(&id, &url); err != nil {
			return nil, err
		}
		m[id] = url
	}
	return m, rows.Err()
}

func loadProviders(ctx context.Context, pool *pgxpool.Pool, decrypt DecryptFunc, proxies map[string]string) (map[string]*registry.Provider, error) {
	const q = `SELECT id, name, protocol, base_url, api_key_enc, proxy_id,
		timeout_ms, connect_timeout_ms, max_retries,
		hc_error_rate, hc_p95_ttft_ms, hc_window_sec, hc_cooldown_sec, metadata
		FROM providers WHERE enabled=true`
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]*registry.Provider{}
	for rows.Next() {
		var (
			p             registry.Provider
			proto         string
			apiKey        []byte
			proxyID       *string
			timeoutMS     int
			connTimeoutMS int
			maxRetries    int
			hcErr         float64
			hcTTFT        int
			hcWin         int
			hcCool        int
			metadata      []byte
		)
		if err := rows.Scan(&p.ID, &p.Name, &proto, &p.BaseURL, &apiKey, &proxyID,
			&timeoutMS, &connTimeoutMS, &maxRetries,
			&hcErr, &hcTTFT, &hcWin, &hcCool, &metadata); err != nil {
			return nil, err
		}
		p.Protocol = adapter.Protocol(proto)
		p.Timeout = time.Duration(timeoutMS) * time.Millisecond
		p.ConnectTimeout = time.Duration(connTimeoutMS) * time.Millisecond
		p.MaxRetries = maxRetries
		p.HealthErrorRate = hcErr
		p.HealthP95TTFTMs = hcTTFT
		p.HealthWindowSec = hcWin
		p.HealthCooldown = hcCool
		if len(apiKey) > 0 {
			if k, err := decrypt(apiKey); err == nil {
				p.APIKey = k
			}
		}
		if proxyID != nil {
			p.ProxyURL = proxies[*proxyID]
		}
		p.Headers = extractHeaders(metadata)
		m[p.ID] = &p
	}
	return m, rows.Err()
}

// extractHeaders pulls a {"headers": {...}} map out of the provider metadata blob.
func extractHeaders(metadata []byte) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	var doc struct {
		Headers map[string]string `json:"headers"`
	}
	if json.Unmarshal(metadata, &doc) != nil {
		return nil
	}
	return doc.Headers
}

func loadModels(ctx context.Context, pool *pgxpool.Pool) (map[string]string, error) {
	rows, err := pool.Query(ctx, `SELECT id, alias FROM models WHERE enabled=true`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{} // model_id -> alias
	for rows.Next() {
		var id, alias string
		if err := rows.Scan(&id, &alias); err != nil {
			return nil, err
		}
		m[id] = alias
	}
	return m, rows.Err()
}

func loadProfiles(ctx context.Context, pool *pgxpool.Pool) (def *registry.ClientProfile, byProvider, byModel map[string]*registry.ClientProfile, err error) {
	const q = `SELECT scope, target_id, headers, user_agent, origin, referer, strip_client_headers
		FROM client_profiles WHERE enabled=true`
	rows, qErr := pool.Query(ctx, q)
	if qErr != nil {
		return nil, nil, nil, qErr
	}
	defer rows.Close()
	byProvider = map[string]*registry.ClientProfile{}
	byModel = map[string]*registry.ClientProfile{}
	for rows.Next() {
		var (
			scope    string
			targetID *string
			headers  []byte
			ua       *string
			origin   *string
			referer  *string
			strip    bool
		)
		if e := rows.Scan(&scope, &targetID, &headers, &ua, &origin, &referer, &strip); e != nil {
			return nil, nil, nil, e
		}
		cp := &registry.ClientProfile{StripClientHeaders: strip}
		if len(headers) > 0 {
			_ = json.Unmarshal(headers, &cp.Headers)
		}
		if ua != nil {
			cp.UserAgent = *ua
		}
		if origin != nil {
			cp.Origin = *origin
		}
		if referer != nil {
			cp.Referer = *referer
		}
		switch scope {
		case "default":
			def = cp
		case "provider":
			if targetID != nil {
				byProvider[*targetID] = cp
			}
		case "model":
			if targetID != nil {
				byModel[*targetID] = cp
			}
		}
	}
	return def, byProvider, byModel, rows.Err()
}

// resolveProfile merges default < provider < model into one profile.
func resolveProfile(def *registry.ClientProfile, byProvider, byModel map[string]*registry.ClientProfile,
	providerID, modelID string) *registry.ClientProfile {
	out := &registry.ClientProfile{Headers: map[string]string{}}
	if def != nil {
		mergeProfileInto(out, def)
	}
	if cp := byProvider[providerID]; cp != nil {
		mergeProfileInto(out, cp)
	}
	if cp := byModel[modelID]; cp != nil {
		mergeProfileInto(out, cp)
	}
	if len(out.Headers) == 0 && out.UserAgent == "" && out.Origin == "" && out.Referer == "" && !out.StripClientHeaders {
		return nil
	}
	return out
}

func mergeProfileInto(dst, src *registry.ClientProfile) {
	if src == nil {
		return
	}
	if dst.Headers == nil {
		dst.Headers = map[string]string{}
	}
	for k, v := range src.Headers {
		dst.Headers[k] = v
	}
	if src.UserAgent != "" {
		dst.UserAgent = src.UserAgent
	}
	if src.Origin != "" {
		dst.Origin = src.Origin
	}
	if src.Referer != "" {
		dst.Referer = src.Referer
	}
	if src.StripClientHeaders {
		dst.StripClientHeaders = true
	}
}

func loadChannels(ctx context.Context, pool *pgxpool.Pool, providers map[string]*registry.Provider,
	aliasByModelID map[string]string, def *registry.ClientProfile, byProvider, byModel map[string]*registry.ClientProfile,
	b *registry.SnapshotBuilder) error {
	const q = `SELECT model_id, provider_id, upstream_model, weight, priority FROM model_channels WHERE enabled=true`
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var modelID, providerID, upstreamModel string
		var weight, priority int
		if e := rows.Scan(&modelID, &providerID, &upstreamModel, &weight, &priority); e != nil {
			return e
		}
		alias, ok := aliasByModelID[modelID]
		if !ok {
			continue // model disabled/unknown
		}
		prov, ok := providers[providerID]
		if !ok {
			continue // provider disabled/unknown
		}
		b.AddChannel(&registry.Channel{
			Alias:         alias,
			Provider:      prov,
			UpstreamModel: upstreamModel,
			Weight:        weight,
			Priority:      priority,
			Profile:       resolveProfile(def, byProvider, byModel, providerID, modelID),
		})
	}
	return rows.Err()
}

func loadPolicies(ctx context.Context, pool *pgxpool.Pool, aliasByModelID map[string]string) (map[string]*registry.Policy, error) {
	const q = `SELECT scope, model_id, mode, params FROM router_policies WHERE enabled=true`
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*registry.Policy{}
	for rows.Next() {
		var scope, mode string
		var modelID *string
		var params []byte
		if e := rows.Scan(&scope, &modelID, &mode, &params); e != nil {
			return nil, e
		}
		pol := &registry.Policy{Mode: mode}
		pol.MaxAttempts = paramInt(params, "max_attempts")
		switch scope {
		case "global":
			out[""] = pol
		case "model":
			if modelID != nil {
				if alias, ok := aliasByModelID[*modelID]; ok {
					out[alias] = pol
				}
			}
		}
	}
	return out, rows.Err()
}

func paramInt(b []byte, key string) int {
	if len(b) == 0 {
		return 0
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

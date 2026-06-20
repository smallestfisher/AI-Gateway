// Package admin implements the Admin API: CRUD over the configuration tables
// (providers, models, model-channels, client-profiles, router-policies). Every
// write bumps config_meta.version and publishes a config:invalidate notification
// so the registrydb.Reloader hot-swaps the in-memory snapshot. See docs/10-api.md.
package admin

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aigateway/ai-hub/internal/auth"
	"github.com/aigateway/ai-hub/internal/registrydb"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Store is the admin DB layer. Writes invalidate the runtime registry.
type Store struct {
	pool *pgxpool.Pool
	rdb  *redis.Client
}

// NewStore creates a Store.
func NewStore(pool *pgxpool.Pool, rdb *redis.Client) *Store {
	return &Store{pool: pool, rdb: rdb}
}

// invalidate bumps the config version and notifies subscribers.
func (s *Store) invalidate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx,
		`UPDATE config_meta SET version = version + 1, updated_at = now() WHERE id = 1`); err != nil {
		return fmt.Errorf("admin: bump version: %w", err)
	}
	if s.rdb != nil {
		if err := s.rdb.Publish(ctx, registrydb.InvalidateChannel, "reload").Err(); err != nil {
			// publish failure is non-fatal: the 30s polling fallback will still reload.
			return nil
		}
	}
	return nil
}

// inTx runs fn inside a transaction and invalidates on commit.
func (s *Store) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return s.invalidate(ctx)
}

// ---------------------------------------------------------------------------
// DTOs
// ---------------------------------------------------------------------------

// Provider is the admin view of a provider row.
type Provider struct {
	ID               string         `json:"id,omitempty"`
	Name             string         `json:"name"`
	Protocol         string         `json:"protocol"`
	BaseURL          string         `json:"base_url"`
	APIKey           string         `json:"api_key,omitempty"` // write-only plaintext
	ProxyID          *string        `json:"proxy_id,omitempty"`
	TimeoutMs        int            `json:"timeout_ms"`
	ConnectTimeoutMs int            `json:"connect_timeout_ms"`
	MaxRetries       int            `json:"max_retries"`
	Weight           int            `json:"weight"`
	Priority         int            `json:"priority"`
	Enabled          bool           `json:"enabled"`
	HCErrorRate      float64        `json:"hc_error_rate"`
	HCP95TTFTMs      int            `json:"hc_p95_ttft_ms"`
	HCWindowSec      int            `json:"hc_window_sec"`
	HCCooldownSec    int            `json:"hc_cooldown_sec"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

// Model is the admin view of a model alias.
type Model struct {
	ID          string `json:"id,omitempty"`
	Alias       string `json:"alias"`
	DisplayName string `json:"display_name"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
}

// ModelChannel is the admin view of an alias→(provider, upstream model) binding.
type ModelChannel struct {
	ID            string `json:"id,omitempty"`
	ModelID       string `json:"model_id"`
	ProviderID    string `json:"provider_id"`
	UpstreamModel string `json:"upstream_model"`
	Weight        int    `json:"weight"`
	Priority      int    `json:"priority"`
	Enabled       bool   `json:"enabled"`
}

// ClientProfile is the admin view of an egress-impersonation profile.
type ClientProfile struct {
	ID                string            `json:"id,omitempty"`
	Name              string            `json:"name"`
	Scope             string            `json:"scope"` // default | provider | model
	TargetID          *string           `json:"target_id,omitempty"`
	Headers           map[string]string `json:"headers,omitempty"`
	UserAgent         string            `json:"user_agent,omitempty"`
	Origin            string            `json:"origin,omitempty"`
	Referer           string            `json:"referer,omitempty"`
	StripClientHeaders bool             `json:"strip_client_headers"`
	Enabled           bool              `json:"enabled"`
}

// RouterPolicy is the admin view of a routing policy.
type RouterPolicy struct {
	ID     string         `json:"id,omitempty"`
	Scope  string         `json:"scope"` // global | model
	ModelID *string       `json:"model_id,omitempty"`
	Mode   string         `json:"mode"` // failover | weighted | auto
	Params map[string]any `json:"params,omitempty"`
	Enabled bool          `json:"enabled"`
}

// ---------------------------------------------------------------------------
// Providers
// ---------------------------------------------------------------------------

func (s *Store) ListProviders(ctx context.Context) ([]Provider, error) {
	rows, err := s.pool.Query(ctx, providerCols)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Provider
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) CreateProvider(ctx context.Context, p Provider) (string, error) {
	if p.Name == "" || p.Protocol == "" || p.BaseURL == "" {
		return "", ErrValidation
	}
	var id string
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO providers (name, protocol, base_url, api_key_enc, proxy_id,
				timeout_ms, connect_timeout_ms, max_retries, weight, priority, enabled,
				hc_error_rate, hc_p95_ttft_ms, hc_window_sec, hc_cooldown_sec, metadata)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
			RETURNING id`,
			p.Name, p.Protocol, p.BaseURL, []byte(p.APIKey), p.ProxyID,
			nz(p.TimeoutMs, 60000), nz(p.ConnectTimeoutMs, 10000), nz(p.MaxRetries, 2),
			nz(p.Weight, 1), p.Priority, p.Enabled,
			fnz(p.HCErrorRate, 0.3), nz(p.HCP95TTFTMs, 8000), nz(p.HCWindowSec, 60), nz(p.HCCooldownSec, 30),
			marshalJSON(p.Metadata),
		).Scan(&id)
	})
	return id, err
}

func (s *Store) UpdateProvider(ctx context.Context, id string, p Provider) error {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE providers SET name=$2, protocol=$3, base_url=$4, api_key_enc=COALESCE($5, api_key_enc),
				proxy_id=$6, timeout_ms=$7, connect_timeout_ms=$8, max_retries=$9, weight=$10, priority=$11,
				enabled=$12, hc_error_rate=$13, hc_p95_ttft_ms=$14, hc_window_sec=$15, hc_cooldown_sec=$16,
				metadata=$17, updated_at=now()
			WHERE id=$1`,
			id, p.Name, p.Protocol, p.BaseURL, optBytes(p.APIKey), p.ProxyID,
			nz(p.TimeoutMs, 60000), nz(p.ConnectTimeoutMs, 10000), nz(p.MaxRetries, 2),
			nz(p.Weight, 1), p.Priority, p.Enabled,
			fnz(p.HCErrorRate, 0.3), nz(p.HCP95TTFTMs, 8000), nz(p.HCWindowSec, 60), nz(p.HCCooldownSec, 30),
			marshalJSON(p.Metadata),
		)
		if err != nil {
			return err
		}
		return assertRowsAffected(tag, id)
	})
	return err
}

func (s *Store) DeleteProvider(ctx context.Context, id string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM providers WHERE id=$1`, id)
		if err != nil {
			return err
		}
		return assertRowsAffected(tag, id)
	})
}

// ---------------------------------------------------------------------------
// Models
// ---------------------------------------------------------------------------

func (s *Store) ListModels(ctx context.Context) ([]Model, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, alias, display_name, COALESCE(description,''), enabled FROM models`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Model
	for rows.Next() {
		var m Model
		if err := rows.Scan(&m.ID, &m.Alias, &m.DisplayName, &m.Description, &m.Enabled); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) CreateModel(ctx context.Context, m Model) (string, error) {
	if m.Alias == "" {
		return "", ErrValidation
	}
	var id string
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO models (alias, display_name, description, enabled) VALUES ($1,$2,$3,$4) RETURNING id`,
			m.Alias, firstNonEmpty(m.DisplayName, m.Alias), m.Description, m.Enabled,
		).Scan(&id)
	})
	return id, err
}

func (s *Store) DeleteModel(ctx context.Context, id string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM models WHERE id=$1`, id)
		if err != nil {
			return err
		}
		return assertRowsAffected(tag, id)
	})
}

// ---------------------------------------------------------------------------
// Model channels
// ---------------------------------------------------------------------------

func (s *Store) ListChannels(ctx context.Context) ([]ModelChannel, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, model_id, provider_id, upstream_model, weight, priority, enabled FROM model_channels`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelChannel
	for rows.Next() {
		var c ModelChannel
		if err := rows.Scan(&c.ID, &c.ModelID, &c.ProviderID, &c.UpstreamModel, &c.Weight, &c.Priority, &c.Enabled); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CreateChannel(ctx context.Context, c ModelChannel) (string, error) {
	if c.ModelID == "" || c.ProviderID == "" || c.UpstreamModel == "" {
		return "", ErrValidation
	}
	var id string
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO model_channels (model_id, provider_id, upstream_model, weight, priority, enabled)
			 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
			c.ModelID, c.ProviderID, c.UpstreamModel, nz(c.Weight, 1), c.Priority, c.Enabled,
		).Scan(&id)
	})
	return id, err
}

func (s *Store) DeleteChannel(ctx context.Context, id string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM model_channels WHERE id=$1`, id)
		if err != nil {
			return err
		}
		return assertRowsAffected(tag, id)
	})
}

// ---------------------------------------------------------------------------
// Client profiles + router policies
// ---------------------------------------------------------------------------

func (s *Store) ListProfiles(ctx context.Context) ([]ClientProfile, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, scope, target_id, headers, COALESCE(user_agent,''), COALESCE(origin,''),
		 COALESCE(referer,''), strip_client_headers, enabled FROM client_profiles`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClientProfile
	for rows.Next() {
		var p ClientProfile
		var headers []byte
		if err := rows.Scan(&p.ID, &p.Name, &p.Scope, &p.TargetID, &headers, &p.UserAgent,
			&p.Origin, &p.Referer, &p.StripClientHeaders, &p.Enabled); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(headers, &p.Headers)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) CreateProfile(ctx context.Context, p ClientProfile) (string, error) {
	if p.Name == "" || p.Scope == "" {
		return "", ErrValidation
	}
	if (p.Scope == "provider" || p.Scope == "model") && (p.TargetID == nil || *p.TargetID == "") {
		return "", ErrValidation
	}
	var id string
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO client_profiles (name, scope, target_id, headers, user_agent, origin, referer,
			  strip_client_headers, enabled)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
			p.Name, p.Scope, p.TargetID, marshalJSON(p.Headers), p.UserAgent, p.Origin, p.Referer,
			p.StripClientHeaders, p.Enabled,
		).Scan(&id)
	})
	return id, err
}

func (s *Store) DeleteProfile(ctx context.Context, id string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM client_profiles WHERE id=$1`, id)
		if err != nil {
			return err
		}
		return assertRowsAffected(tag, id)
	})
}

func (s *Store) ListPolicies(ctx context.Context) ([]RouterPolicy, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, scope, model_id, mode, params, enabled FROM router_policies`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RouterPolicy
	for rows.Next() {
		var p RouterPolicy
		var params []byte
		if err := rows.Scan(&p.ID, &p.Scope, &p.ModelID, &p.Mode, &params, &p.Enabled); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(params, &p.Params)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) UpsertPolicy(ctx context.Context, p RouterPolicy) (string, error) {
	if p.Scope == "" || p.Mode == "" {
		return "", ErrValidation
	}
	if p.Scope == "model" && (p.ModelID == nil || *p.ModelID == "") {
		return "", ErrValidation
	}
	var id string
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		// one policy per scope/target: delete existing then insert
		if p.Scope == "global" {
			if _, err := tx.Exec(ctx, `DELETE FROM router_policies WHERE scope='global'`); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(ctx, `DELETE FROM router_policies WHERE scope='model' AND model_id=$1`, p.ModelID); err != nil {
				return err
			}
		}
		return tx.QueryRow(ctx,
			`INSERT INTO router_policies (scope, model_id, mode, params, enabled)
			 VALUES ($1,$2,$3,$4,$5) RETURNING id`,
			p.Scope, p.ModelID, p.Mode, marshalJSON(p.Params), p.Enabled,
		).Scan(&id)
	})
	return id, err
}

// ConfigVersion returns the current config_meta.version (for /config/version).
func (s *Store) ConfigVersion(ctx context.Context) (int64, error) {
	var v int64
	err := s.pool.QueryRow(ctx, `SELECT version FROM config_meta WHERE id=1`).Scan(&v)
	return v, err
}

// ---------------------------------------------------------------------------
// Users, API keys, quotas
// ---------------------------------------------------------------------------

// CreateUser inserts a user and an initial quota row.
func (s *Store) CreateUser(ctx context.Context, name, email string, balance int64) (string, error) {
	if name == "" {
		return "", ErrValidation
	}
	var id string
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `INSERT INTO users (name,email) VALUES ($1,$2) RETURNING id`,
			name, strPtr(email)).Scan(&id); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `INSERT INTO user_quotas (user_id,balance) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
			id, balance)
		return e
	})
	return id, err
}

// IssueAPIKey generates a new key for a user and returns the plaintext (shown once).
func (s *Store) IssueAPIKey(ctx context.Context, userID, name string) (raw, id string, err error) {
	if userID == "" {
		return "", "", ErrValidation
	}
	raw = generateKey()
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO api_keys (user_id,name,key_prefix,key_hash) VALUES ($1,$2,$3,$4) RETURNING id`,
			userID, firstNonEmpty(name, "default"), raw[:16], auth.HashKey(raw),
		).Scan(&id)
	})
	return raw, id, err
}

// SetQuota upserts a user's quota (0 rpm/tpm => unlimited).
func (s *Store) SetQuota(ctx context.Context, userID string, balance int64, rpm, tpm int, whitelist []string) error {
	if userID == "" {
		return ErrValidation
	}
	wl := []byte("[]")
	if len(whitelist) > 0 {
		wl = marshalJSON(whitelist)
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO user_quotas (user_id,balance,rpm_limit,tpm_limit,model_whitelist,updated_at)
			VALUES ($1,$2,$3,$4,$5,now())
			ON CONFLICT (user_id) DO UPDATE SET
				balance=EXCLUDED.balance, rpm_limit=EXCLUDED.rpm_limit, tpm_limit=EXCLUDED.tpm_limit,
				model_whitelist=EXCLUDED.model_whitelist, updated_at=now()`,
			userID, balance, intPtr(rpm), intPtr(tpm), wl)
		return err
	})
}

// --- helpers ---

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func intPtr(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

func generateKey() string {
	b := make([]byte, 24)
	if _, err := crand.Read(b); err != nil {
		return "sk-aihub-fallback0000000000000000000"
	}
	return "sk-aihub-" + hex.EncodeToString(b)
}

// ---------------------------------------------------------------------------
// scan helpers
// ---------------------------------------------------------------------------

const providerCols = `SELECT id, name, protocol, base_url, proxy_id,
	timeout_ms, connect_timeout_ms, max_retries, weight, priority, enabled,
	hc_error_rate, hc_p95_ttft_ms, hc_window_sec, hc_cooldown_sec, metadata FROM providers`

func scanProvider(rows pgx.Rows) (Provider, error) {
	var (
		p       Provider
		proxyID *string
		meta    []byte
	)
	if err := rows.Scan(&p.ID, &p.Name, &p.Protocol, &p.BaseURL, &proxyID,
		&p.TimeoutMs, &p.ConnectTimeoutMs, &p.MaxRetries, &p.Weight, &p.Priority, &p.Enabled,
		&p.HCErrorRate, &p.HCP95TTFTMs, &p.HCWindowSec, &p.HCCooldownSec, &meta); err != nil {
		return p, err
	}
	p.ProxyID = proxyID
	_ = json.Unmarshal(meta, &p.Metadata)
	return p, nil
}

// ErrValidation is returned for invalid input.
var ErrValidation = errors.New("admin: invalid input")

// ErrNotFound is returned when a row isn't found.
var ErrNotFound = errors.New("admin: not found")

func nz(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}
func fnz(v, def float64) float64 {
	if v == 0 {
		return def
	}
	return v
}
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
func optBytes(s string) []byte {
	if s == "" {
		return nil
	}
	return []byte(s)
}
func marshalJSON(v any) []byte {
	if v == nil {
		return []byte("{}")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}
func assertRowsAffected(tag pgconn.CommandTag, id string) error {
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("admin: %w: %s", ErrNotFound, id)
	}
	return nil
}

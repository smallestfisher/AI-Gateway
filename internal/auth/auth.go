// Package auth authenticates proxy callers (API keys) and enforces per-key
// rate limits and per-user quota. API keys resolve to an Identity via the DB,
// cached in Redis; rate-limit windows and balances live in Redis for speed.
// See docs/02-modules.md §7, docs/10-api.md §1.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Identity is the resolved caller (cached).
type Identity struct {
	UserID    string   `json:"user_id"`
	APIKeyID  string   `json:"api_key_id"`
	RPM       int      `json:"rpm"`
	TPM       int      `json:"tpm"`
	Balance   int64    `json:"balance"`
	Whitelist []string `json:"whitelist"` // model aliases; empty = all allowed
}

// CanUseModel reports whether the alias is allowed for this identity.
func (i *Identity) CanUseModel(alias string) bool {
	if len(i.Whitelist) == 0 {
		return true
	}
	for _, m := range i.Whitelist {
		if m == alias {
			return true
		}
	}
	return false
}

// Resolver turns a raw API key into an Identity (DB-backed, Redis-cached).
type Resolver struct {
	pool    *pgxpool.Pool
	rdb     redis.Cmdable
	cacheTTL time.Duration
}

// NewResolver creates a Resolver. cacheTTL defaults to 15s.
func NewResolver(pool *pgxpool.Pool, rdb redis.Cmdable) *Resolver {
	return &Resolver{pool: pool, rdb: rdb, cacheTTL: 15 * time.Second}
}

// Sentinel errors; the server maps these to HTTP statuses.
var (
	ErrNoKey        = errors.New("auth: missing api key")
	ErrInvalidKey   = errors.New("auth: invalid api key")
	ErrUserDisabled = errors.New("auth: user disabled")
)

// HashKey returns the stored hash of a raw API key (sha256 hex).
func HashKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Resolve validates the raw key and returns the caller's Identity.
func (r *Resolver) Resolve(ctx context.Context, raw string) (*Identity, error) {
	if raw == "" {
		return nil, ErrNoKey
	}
	hash := HashKey(raw)

	// cache
	if r.rdb != nil {
		if b, err := r.rdb.Get(ctx, "auth:key:"+hash).Bytes(); err == nil {
			var id Identity
			if json.Unmarshal(b, &id) == nil {
				return &id, nil
			}
		}
	}

	// DB lookup (api_keys JOIN users LEFT JOIN user_quotas)
	var (
		id              Identity
		status          string
		userStatus      string
		expires         *time.Time
		wl              []byte
	)
	err := r.pool.QueryRow(ctx, `
		SELECT ak.id, ak.user_id, ak.status, ak.expires_at,
		       u.status,
		       COALESCE(uq.rpm_limit,0), COALESCE(uq.tpm_limit,0),
		       COALESCE(uq.balance,0), COALESCE(uq.model_whitelist,'[]'::jsonb)
		FROM api_keys ak
		JOIN users u ON u.id = ak.user_id
		LEFT JOIN user_quotas uq ON uq.user_id = ak.user_id
		WHERE ak.key_hash = $1`, hash,
	).Scan(&id.APIKeyID, &id.UserID, &status, &expires, &userStatus,
		&id.RPM, &id.TPM, &id.Balance, &wl)
	if err != nil {
		return nil, ErrInvalidKey
	}
	if status != "active" {
		return nil, ErrInvalidKey
	}
	if userStatus != "active" {
		return nil, ErrUserDisabled
	}
	if expires != nil && !expires.After(time.Now()) {
		return nil, ErrInvalidKey
	}
	_ = json.Unmarshal(wl, &id.Whitelist)

	// cache (best-effort)
	if r.rdb != nil {
		if b, err := json.Marshal(id); err == nil {
			_ = r.rdb.Set(ctx, "auth:key:"+hash, b, r.cacheTTL).Err()
		}
	}
	return &id, nil
}

// Invalidate drops the cache entry for a key (called when an api_key is revoked).
func (r *Resolver) Invalidate(ctx context.Context, raw string) {
	if r.rdb == nil || raw == "" {
		return
	}
	_ = r.rdb.Del(ctx, "auth:key:"+HashKey(raw)).Err()
}

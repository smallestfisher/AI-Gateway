package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Limiter enforces RPM via a Redis fixed-window counter per API key.
type Limiter struct {
	rdb redis.Cmdable
	now func() time.Time
}

// NewLimiter creates a Limiter.
func NewLimiter(rdb redis.Cmdable) *Limiter {
	return &Limiter{rdb: rdb, now: time.Now}
}

// AllowRPM reports whether a request is allowed under the per-minute limit.
// Returns (allowed, retryAfterSeconds).
func (l *Limiter) AllowRPM(ctx context.Context, apiKeyID string, limit int) (bool, int) {
	if limit <= 0 || l.rdb == nil {
		return true, 0
	}
	now := l.now()
	minute := now.Unix() / 60
	key := fmt.Sprintf("rl:rpm:%s:%d", apiKeyID, minute)
	n, err := l.rdb.Incr(ctx, key).Result()
	if err != nil {
		return true, 0 // fail-open on Redis error
	}
	if n == 1 {
		_ = l.rdb.Expire(ctx, key, time.Minute).Err()
	}
	if int(n) > limit {
		return false, 60 - now.Second()
	}
	return true, 0
}

// Quota tracks per-user token balances in Redis (lazily initialized from the DB)
// and deducts usage. Balance is in "credits"; Phase 5 costs 1 credit per token.
type Quota struct {
	rdb  redis.Cmdable
	pool *pgxpool.Pool
}

// NewQuota creates a Quota store.
func NewQuota(rdb redis.Cmdable, pool *pgxpool.Pool) *Quota {
	return &Quota{rdb: rdb, pool: pool}
}

func (q *Quota) key(userID string) string { return "quota:bal:" + userID }

// Balance returns the current balance, initializing from the DB if needed.
func (q *Quota) Balance(ctx context.Context, userID string) (int64, error) {
	if q.rdb == nil {
		return q.dbBalance(ctx, userID)
	}
	v, err := q.rdb.Get(ctx, q.key(userID)).Int64()
	if err == nil {
		return v, nil
	}
	if err != redis.Nil {
		return 0, err
	}
	// miss: lazy-init from DB
	bal, err := q.dbBalance(ctx, userID)
	if err != nil {
		return 0, err
	}
	_ = q.rdb.Set(ctx, q.key(userID), bal, 0).Err()
	return bal, nil
}

// HasCredit reports whether the user has any balance left.
func (q *Quota) HasCredit(ctx context.Context, userID string) (bool, error) {
	b, err := q.Balance(ctx, userID)
	return b > 0, err
}

// Deduct subtracts amount from the user's balance (Redis source of truth; the
// DB row is updated best-effort for durability). A negative result is allowed
// (small overdraw); the next request will be denied by HasCredit.
func (q *Quota) Deduct(ctx context.Context, userID string, amount int64) error {
	if amount <= 0 {
		return nil
	}
	if q.rdb != nil {
		if err := q.rdb.DecrBy(ctx, q.key(userID), amount).Err(); err != nil {
			return err
		}
	}
	if q.pool != nil {
		_, _ = q.pool.Exec(ctx,
			`UPDATE user_quotas SET balance = balance - $2, updated_at = now() WHERE user_id = $1`,
			userID, amount)
	}
	return nil
}

// dbBalance reads the balance from user_quotas (0 if no row).
func (q *Quota) dbBalance(ctx context.Context, userID string) (int64, error) {
	if q.pool == nil {
		return 0, nil
	}
	var bal int64
	err := q.pool.QueryRow(ctx, `SELECT COALESCE(balance,0) FROM user_quotas WHERE user_id=$1`, userID).Scan(&bal)
	if err == pgx.ErrNoRows {
		return 0, nil
	}
	return bal, err
}

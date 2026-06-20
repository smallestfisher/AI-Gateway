package registrydb

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/aigateway/ai-hub/internal/registry"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// InvalidateChannel is the Redis pub/sub channel config writes publish to.
const InvalidateChannel = "config:invalidate"

// Reloader is a registry.Source backed by PostgreSQL, kept hot via Redis pub/sub
// invalidation (with a polling fallback). Readers always hit the in-memory
// atomic snapshot; the DB is only read on (re)load.
type Reloader struct {
	pool         *pgxpool.Pool
	rdb          *redis.Client
	decrypt      DecryptFunc
	log          *slog.Logger
	pollInterval time.Duration
	cur          atomic.Pointer[registry.Snapshot]
	cancel       context.CancelFunc
}

// NewReloader creates a Reloader. pollInterval defaults to 30s.
func NewReloader(pool *pgxpool.Pool, rdb *redis.Client, decrypt DecryptFunc, log *slog.Logger) *Reloader {
	return &Reloader{pool: pool, rdb: rdb, decrypt: decrypt, log: log, pollInterval: 30 * time.Second}
}

// Start performs the initial load and spawns the invalidation listeners.
func (r *Reloader) Start(ctx context.Context) error {
	if err := r.Reload(ctx); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	if r.rdb != nil {
		go r.subscribeLoop(ctx)
	}
	go r.pollLoop(ctx)
	return nil
}

// Reload re-reads the DB and atomically swaps the snapshot.
func (r *Reloader) Reload(ctx context.Context) error {
	snap, err := Load(ctx, r.pool, r.decrypt)
	if err != nil {
		return err
	}
	r.cur.Store(snap)
	if r.log != nil {
		n := 0
		for _, chs := range snap.Channels {
			n += len(chs)
		}
		r.log.Info("registry reloaded", "models", len(snap.Channels), "channels", n, "providers", len(snap.Providers))
	}
	return nil
}

// Snapshot returns the current in-memory snapshot (registry.Source).
func (r *Reloader) Snapshot() (*registry.Snapshot, error) {
	s := r.cur.Load()
	if s == nil {
		return nil, errors.New("registrydb: snapshot not loaded yet")
	}
	return s, nil
}

// Stop shuts down the listeners.
func (r *Reloader) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
}

func (r *Reloader) reloadSafe() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.Reload(ctx); err != nil && r.log != nil {
		r.log.Warn("registry reload failed", "err", err)
	}
}

func (r *Reloader) subscribeLoop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		pubsub := r.rdb.Subscribe(ctx, InvalidateChannel)
		ch := pubsub.Channel()
		for {
			select {
			case <-ctx.Done():
				_ = pubsub.Close()
				return
			case _, ok := <-ch:
				if !ok {
					// subscription dropped; outer loop will reconnect
					_ = pubsub.Close()
					select {
					case <-ctx.Done():
						return
					case <-time.After(time.Second):
					}
					goto reconnect
				}
				r.reloadSafe()
			}
		}
	reconnect:
	}
}

func (r *Reloader) pollLoop(ctx context.Context) {
	t := time.NewTicker(r.pollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.reloadSafe()
		}
	}
}

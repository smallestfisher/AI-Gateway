// Command gateway is the AI Agent Gateway server process.
//
// Runtime config is loaded from PostgreSQL by the registrydb.Reloader and kept
// hot via Redis pub/sub invalidation; health/circuit-breaking runs on Redis.
// If a DB is unreachable, it falls back to a single upstream seeded from the
// environment (dev). See docs/02-modules.md, docs/11-roadmap.md.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aigateway/ai-hub/internal/adapter"
	"github.com/aigateway/ai-hub/internal/adapter/anthropicmessages"
	"github.com/aigateway/ai-hub/internal/adapter/openaichat"
	"github.com/aigateway/ai-hub/internal/adapter/openairesponses"
	"github.com/aigateway/ai-hub/internal/admin"
	"github.com/aigateway/ai-hub/internal/auth"
	"github.com/aigateway/ai-hub/internal/config"
	"github.com/aigateway/ai-hub/internal/egress"
	"github.com/aigateway/ai-hub/internal/health"
	"github.com/aigateway/ai-hub/internal/logging"
	"github.com/aigateway/ai-hub/internal/pipeline"
	"github.com/aigateway/ai-hub/internal/registry"
	"github.com/aigateway/ai-hub/internal/registrydb"
	"github.com/aigateway/ai-hub/internal/router"
	"github.com/aigateway/ai-hub/internal/server"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	cfg := config.Load()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level(cfg.LogLevel)}))
	slog.SetDefault(log)

	registryHub := adapter.NewRegistry(openaichat.New(), anthropicmessages.New(), openairesponses.New())

	src, healthStore, recorder, pool, rdb, closer := buildConfigAndHealth(&cfg, log)
	defer closer()

	rt := router.New(src, router.WithHealth(healthStore))
	eg := egress.New(registryHub)
	eg.SetRecorder(recorder) // records TTFT/latency/success for circuit-breaking
	pipe := pipeline.New(rt, eg)

	// Request logger (writes to request_logs table).
	var logger *logging.Logger
	if pool != nil {
		logger = logging.NewLogger(pool)
		log.Info("request logging enabled")
	} else {
		log.Warn("request logging disabled (no database)")
	}

	app := server.New(&cfg, log, server.Deps{
		Registry: registryHub,
		Pipeline: pipe,
		Auth:     buildAuth(pool, rdb),
		Logger:   logger,
	})

	// Admin API (config CRUD + hot-reload trigger).
	if pool != nil && cfg.AdminToken != "" {
		st := admin.NewStore(pool, rdb)
		admin.Mount(app, st, cfg.AdminToken,
			admin.WithDiagnostics(admin.NewDiagnostics(st, registryHub, pipe)),
		)
		log.Info("admin API enabled", "prefix", "/api/admin")
	} else if cfg.AdminToken == "" {
		log.Warn("admin API disabled (GATEWAY_ADMIN_TOKEN not set)")
	}

	go func() {
		log.Info("ai agent gateway listening", "addr", cfg.HTTPAddr,
			"protocols", registryHub.Protocols())
		if err := app.Listen(cfg.HTTPAddr); err != nil {
			log.Error("server stopped", "err", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("shutting down")
	_ = app.Shutdown()
}

// buildConfigAndHealth wires the configuration Source and the health store,
// preferring the DB+Redis stack and falling back to an env-seeded static config
// and a no-op circuit breaker when either is unavailable.
func buildConfigAndHealth(cfg *config.Config, log *slog.Logger) (registry.Source, router.Health, egress.Recorder, *pgxpool.Pool, *redis.Client, func()) {
	cleanup := func() {}

	rdb := newRedis(cfg, log)

	// Try DB-backed config.
	if pool, err := pgxpool.New(context.Background(), cfg.PostgresDSN); err == nil {
		if pingErr := pool.Ping(context.Background()); pingErr == nil {
			reloader := registrydb.NewReloader(pool, rdb, nil, log)
			if startErr := reloader.Start(context.Background()); startErr == nil {
				log.Info("using DB-backed config registry")
				cleanup = func() { reloader.Stop(); pool.Close() }
				hHealth, hRec := buildHealth(rdb, reloader, log)
				return reloader, hHealth, hRec, pool, rdb, cleanup
			} else {
				log.Warn("db registry start failed, falling back to env config", "err", startErr)
			}
			reloader.Stop()
		} else {
			log.Warn("db ping failed, falling back to env config", "err", pingErr)
		}
		pool.Close()
	} else {
		log.Warn("db connect failed, falling back to env config", "err", err)
	}

	// Fallback: env-seeded static config + (Redis or no-op) health.
	src := bootstrapSource()
	hHealth, hRec := buildHealth(rdb, src, log)
	return src, hHealth, hRec, nil, rdb, cleanup
}

// buildHealth returns a Redis-backed health store (implements both router.Health
// and egress.Recorder), or (NoopHealth, nil) when Redis is absent. The lookup
// reads provider thresholds from the current config snapshot.
func buildHealth(rdb *redis.Client, src registry.Source, log *slog.Logger) (router.Health, egress.Recorder) {
	if rdb == nil {
		log.Warn("no Redis: circuit-breaker disabled (NoopHealth)")
		return router.NoopHealth{}, nil
	}
	lookup := func(id string) *registry.Provider {
		snap, err := src.Snapshot()
		if err != nil {
			return nil
		}
		return snap.ProviderByID(id)
	}
	hs := health.New(rdb, registry.HealthDefaults{}, lookup)
	return hs, hs
}

func newRedis(cfg *config.Config, log *slog.Logger) *redis.Client {
	if cfg.RedisAddr == "" {
		return nil
	}
	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Warn("redis ping failed; health/hot-reload will be disabled", "err", err)
		return nil
	}
	return rdb
}

// buildAuth enables proxy auth (API key + rate limit + quota) when the DB is
// available. Without a DB there is nothing to resolve keys against.
func buildAuth(pool *pgxpool.Pool, rdb *redis.Client) *server.AuthDeps {
	if pool == nil {
		return nil
	}
	return &server.AuthDeps{
		Resolver: auth.NewResolver(pool, rdb),
		Limiter:  auth.NewLimiter(rdb),
		Quota:    auth.NewQuota(rdb, pool),
	}
}

// bootstrapSource builds an in-memory config source seeded from the environment.
func bootstrapSource() registry.Source {
	url := os.Getenv("GATEWAY_UPSTREAM_URL")
	if url == "" {
		return emptySource{}
	}
	proto := adapter.Protocol(envOr("GATEWAY_UPSTREAM_PROTOCOL", string(adapter.ProtocolChat)))
	prov := &registry.Provider{
		ID:       "bootstrap",
		Name:     "bootstrap",
		Protocol: proto,
		BaseURL:  url,
		APIKey:   os.Getenv("GATEWAY_UPSTREAM_API_KEY"),
		Timeout:  60 * time.Second,
	}
	ch := &registry.Channel{
		Alias:         envOr("GATEWAY_UPSTREAM_ALIAS", "default"),
		UpstreamModel: envOr("GATEWAY_UPSTREAM_MODEL", envOr("GATEWAY_UPSTREAM_ALIAS", "default")),
		Provider:      prov,
		Weight:        1,
		Priority:      0,
	}
	return registry.NewStatic(registry.NewBuilder().AddChannel(ch).Build())
}

type emptySource struct{}

func (emptySource) Snapshot() (*registry.Snapshot, error) {
	return registry.NewBuilder().Build(), nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func level(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

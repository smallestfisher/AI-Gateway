# AI Agent Gateway

A production-grade **AI Agent Gateway**: let Codex CLI, Claude Code, and the
OpenAI SDK reach any upstream (OpenAI, Anthropic, Gemini, Qwen, DeepSeek,
community mirrors, NewAPI, OpenRouter, …) through one endpoint, with protocol
conversion, tool-call compatibility, streaming-event translation, and smart
routing.

The hub is a **Unified IR (block model)** — not Chat Completions. Each protocol
implements one bidirectional **Adapter**; adding a protocol changes no core code.

> **Status:** Phase 0–5 (core) done — Unified IR, the adapter framework,
> `openai_chat` + `anthropic_messages` adapters (non-streaming **and** streaming,
> lossless cross-protocol), Router (failover/weighted) with a **Redis-backed
> circuit breaker**, Egress (real upstream calls, TTFT sampling, retries,
> Client-Profile injection, streaming), the Pipeline (pre-first-byte failover),
> a live Fiber ingress, a **PostgreSQL-backed config Registry with Redis
> hot-reload**, an **Admin API (CRUD + hot-reload + user/key issuance)**, and
> **proxy auth (API keys + RPM limiting + token-quota billing + model
> whitelist)**. The gateway is now an externally-usable, metered service. Admin
> web UI, the Responses adapter (Codex), and the log center / simulator remain.
> See [`docs/11-roadmap.md`](docs/11-roadmap.md).

Full design: [`docs/`](docs/) (start at [`docs/README.md`](docs/README.md)).

---

## Quick start

```bash
# 1. Run postgres + redis + gateway
docker compose up --build

# 2. Health check
curl http://localhost:8080/healthz
# => {"protocols":["openai_chat","anthropic_messages"],"status":"ok"}

# 3. Local dev (no docker): just run the binary
go run ./cmd/gateway
```

The DB is bootstrapped from `migrations/0001_init.up.sql` on first init.

## Configuration

Bootstrap is via env vars (all runtime config will live in the DB / admin UI):

| Var | Default |
|-----|---------|
| `GATEWAY_HTTP_ADDR` | `:8080` |
| `GATEWAY_POSTGRES_DSN` | `postgres://gateway:gateway@localhost:5432/aihub?sslmode=disable` |
| `GATEWAY_REDIS_ADDR` | `localhost:6379` |
| `GATEWAY_REDIS_PASSWORD` | `` |
| `GATEWAY_LOG_LEVEL` | `info` |

## Build & test

```bash
go build ./...
go test ./...
go vet ./...
```

## Project layout

```
cmd/gateway/              process entrypoint (env-seeded bootstrap upstream)
internal/
  ir/                     Unified IR types (docs/05)
  adapter/                Adapter interface + registry (docs/04)
    openaichat/           OpenAI Chat adapter (non-stream + stream)
    anthropicmessages/    Anthropic Messages adapter (non-stream + stream)
    crossprotocol/        lossless cross-protocol round-trip tests
  router/                 alias -> channels, failover/weighted selection (docs/02 §2)
  registry/               in-memory config snapshot + Source (docs/02 §4)
  registrydb/             PostgreSQL loader + Redis hot-reload Source (docs/02 §4)
  health/                 Redis circuit breaker (sliding window) (docs/02 §5)
  egress/                 upstream calls: transport pool, auth, profile, stream, TTFT (docs/02 §3)
  pipeline/               Ingress -> Router -> Egress -> encode, with failover
  admin/                  Admin API: config CRUD + hot-reload trigger (docs/10)
  auth/                   proxy auth: API keys, RPM limiting, token-quota billing
  config/                 bootstrap (env) config
  server/                 Fiber app: healthz + proxy ingress (stream + non-stream)
docs/                     full design (00–12)
migrations/               PostgreSQL schema (golang-migrate format)
Dockerfile, docker-compose.yml
```

## What works right now (Phase 0–3 core)

- ✅ Unified IR with lossless conversion between Chat and Messages
  (validated by `internal/adapter/crossprotocol` — parallel tool calls,
  tool results, system prompts, stop reasons, usage all survive the hop).
- ✅ **Live end-to-end proxying**: client → Router → Egress → upstream → back,
  both non-streaming and streaming, cross-protocol (e.g. Claude Code `/v1/messages`
  client backed by an OpenAI-Chat upstream, streaming).
- ✅ Streaming adapters: Chat ↔ IR ↔ Messages event state machines (block
  lifecycle, tool-input deltas, stop reasons, usage).
- ✅ **DB-backed config Registry**: loads providers/models/channels/profiles/
  policies from PostgreSQL; kept hot via Redis pub/sub invalidation + a polling
  fallback (atomic snapshot swap, zero-DB hot path). Verified against real Postgres.
- ✅ **Health / circuit breaker**: Redis sliding windows track error-rate and
  slow-TTFT-rate per (provider, model); trips open → cooldown → auto-recover.
  Per-provider thresholds. Egress records TTFT/latency on every call. Verified
  live (driving failures flips the Redis state key and the router filters the
  bad channel).
- ✅ Router: alias → candidate channels, priority tiers, weighted selection,
  health filtering.
- ✅ Egress: per-provider transport pool, auth, Client-Profile header injection
  (default<provider<model merge), timeouts, streaming SSE forwarding,
  retryable/non-retryable error classification.
- ✅ Pipeline with **pre-first-byte failover** (a failing first channel is
  transparently skipped before any byte reaches the client).
- ✅ **Admin API**: REST CRUD over providers/models/channels/profiles/policies
  with bearer-token auth; every write bumps `config_meta.version` and publishes
  `config:invalidate`, so config hot-reloads into routing in milliseconds
  (closed loop verified against real PG + Redis). Includes user creation, API-key
  issuance (plaintext shown once), and quota setting.
- ✅ **Proxy auth**: API-key resolution (DB + Redis cache), RPM rate limiting
  (Redis fixed window), per-user token-quota billing (Redis balance, lazy-DB
  init, deduct by usage), and model whitelist. Statuses: 401 / 403 / 402 / 429.
- ✅ Per-protocol error envelopes; model-not-found / bad-request / no-channel
  return the right status and shape.
- ✅ DB schema (16 tables) — migration verified against real Postgres.
- ✅ Docker stack; runnable from DB config or a single env-seeded upstream.

## Next (Phase 6+)

Admin **web UI** (Next.js on top of the Admin API), the **Responses adapter**
(Codex CLI), and the log center / request simulator. See `docs/11-roadmap.md`.

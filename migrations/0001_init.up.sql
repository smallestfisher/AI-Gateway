-- AI Agent Gateway — initial schema.
-- Mirrors docs/03-database.md. Idempotent-ish: safe to run via golang-migrate
-- or mounted into postgres /docker-entrypoint-initdb.d for dev bootstrap.
-- NOTE: tenancy is the "hybrid" model (global providers, per-user quotas).
-- If full isolation is later required, add owner_id to config tables.

CREATE EXTENSION IF NOT EXISTS pgcrypto; -- for gen_random_uuid()

-- ===== consumers & auth =====
CREATE TABLE IF NOT EXISTS users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL,
    email           TEXT UNIQUE,
    status          TEXT NOT NULL DEFAULT 'active',  -- active | disabled
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS api_keys (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    key_prefix      TEXT NOT NULL,
    key_hash        TEXT NOT NULL UNIQUE,
    scopes          JSONB NOT NULL DEFAULT '[]',
    status          TEXT NOT NULL DEFAULT 'active',  -- active | revoked
    expires_at      TIMESTAMPTZ,
    last_used_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_api_keys_user ON api_keys(user_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys(key_hash);

CREATE TABLE IF NOT EXISTS user_quotas (
    user_id         UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    balance         BIGINT NOT NULL DEFAULT 0,
    rpm_limit       INT,
    tpm_limit       INT,
    model_whitelist JSONB NOT NULL DEFAULT '[]',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ===== upstream & proxy =====
CREATE TABLE IF NOT EXISTS proxy_egress (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL,
    type            TEXT NOT NULL,                -- http | https | socks5
    url             TEXT NOT NULL,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS providers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    protocol        TEXT NOT NULL,                -- openai_chat | anthropic_messages | ...
    base_url        TEXT NOT NULL,
    api_key_enc     BYTEA,
    proxy_id        UUID REFERENCES proxy_egress(id),
    timeout_ms      INT NOT NULL DEFAULT 60000,
    connect_timeout_ms INT NOT NULL DEFAULT 10000,
    max_retries     INT NOT NULL DEFAULT 2,
    weight          INT NOT NULL DEFAULT 1,
    priority        INT NOT NULL DEFAULT 0,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    hc_error_rate   DOUBLE PRECISION NOT NULL DEFAULT 0.3,
    hc_p95_ttft_ms  INT  NOT NULL DEFAULT 8000,
    hc_window_sec   INT  NOT NULL DEFAULT 60,
    hc_cooldown_sec INT  NOT NULL DEFAULT 30,
    metadata        JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_providers_protocol ON providers(protocol) WHERE enabled;

-- ===== models & channels =====
CREATE TABLE IF NOT EXISTS models (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alias           TEXT NOT NULL UNIQUE,
    display_name    TEXT NOT NULL,
    description     TEXT,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    pricing         JSONB NOT NULL DEFAULT '{}',
    metadata        JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS model_channels (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    model_id        UUID NOT NULL REFERENCES models(id) ON DELETE CASCADE,
    provider_id     UUID NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    upstream_model  TEXT NOT NULL,
    weight          INT NOT NULL DEFAULT 1,
    priority        INT NOT NULL DEFAULT 0,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (model_id, provider_id, upstream_model)
);
CREATE INDEX IF NOT EXISTS idx_channels_model ON model_channels(model_id) WHERE enabled;
CREATE INDEX IF NOT EXISTS idx_channels_provider ON model_channels(provider_id);

-- ===== client profiles (egress impersonation) =====
CREATE TABLE IF NOT EXISTS client_profiles (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL,
    scope           TEXT NOT NULL,                 -- default | provider | model
    target_id       UUID,
    headers         JSONB NOT NULL DEFAULT '{}',
    user_agent      TEXT,
    origin          TEXT,
    referer         TEXT,
    cookies         JSONB NOT NULL DEFAULT '[]',
    strip_client_headers BOOLEAN NOT NULL DEFAULT false,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((scope='default' AND target_id IS NULL)
        OR (scope IN ('provider','model') AND target_id IS NOT NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_profile_default
    ON client_profiles((scope='default')) WHERE scope='default';
CREATE INDEX IF NOT EXISTS idx_profile_provider ON client_profiles(target_id) WHERE scope='provider';
CREATE INDEX IF NOT EXISTS idx_profile_model ON client_profiles(target_id) WHERE scope='model';

-- ===== protocols & router policies =====
CREATE TABLE IF NOT EXISTS protocols (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code            TEXT NOT NULL UNIQUE,
    display_name    TEXT NOT NULL,
    adapter_kind    TEXT NOT NULL,                 -- builtin | parameterized | plugin
    ingress_paths   JSONB NOT NULL DEFAULT '[]',
    config          JSONB NOT NULL DEFAULT '{}',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS router_policies (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scope           TEXT NOT NULL,                 -- global | model
    model_id        UUID REFERENCES models(id) ON DELETE CASCADE,
    mode            TEXT NOT NULL,                 -- failover | weighted | auto
    params          JSONB NOT NULL DEFAULT '{}',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((scope='global' AND model_id IS NULL) OR (scope='model' AND model_id IS NOT NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_policy_global
    ON router_policies((scope='global')) WHERE scope='global';

-- ===== MCP (reserved) =====
CREATE TABLE IF NOT EXISTS mcp_servers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    transport       TEXT NOT NULL,                 -- stdio | sse | http
    command_url     TEXT NOT NULL,
    env             JSONB NOT NULL DEFAULT '{}',
    enabled         BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS mcp_bindings (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mcp_server_id   UUID NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
    scope           TEXT NOT NULL,                 -- model | client | global
    target_id       UUID,
    permission      JSONB NOT NULL DEFAULT '{}',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    UNIQUE (mcp_server_id, scope, target_id)
);

-- ===== logs & meta (sinks) =====
CREATE TABLE IF NOT EXISTS request_logs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ts              TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id         UUID,
    api_key_id      UUID,
    client_protocol TEXT NOT NULL,
    model           TEXT NOT NULL,
    provider_id     UUID,
    upstream_model  TEXT,
    stream          BOOLEAN NOT NULL DEFAULT false,
    status          TEXT NOT NULL,
    http_status     INT,
    stop_reason     TEXT,
    ttft_ms         INT,
    latency_ms      INT,
    input_tokens    INT,
    output_tokens   INT,
    cache_read_tokens     INT,
    cache_creation_tokens INT,
    reasoning_tokens INT,
    error_code      TEXT,
    error_msg       TEXT,
    request_id      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_logs_ts ON request_logs(ts DESC);
CREATE INDEX IF NOT EXISTS idx_logs_user_ts ON request_logs(user_id, ts DESC);
CREATE INDEX IF NOT EXISTS idx_logs_provider_ts ON request_logs(provider_id, ts DESC);
CREATE INDEX IF NOT EXISTS idx_logs_model_ts ON request_logs(model, ts DESC);

CREATE TABLE IF NOT EXISTS audit_logs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ts              TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_id        UUID,
    action          TEXT NOT NULL,
    target_type     TEXT NOT NULL,
    target_id       UUID,
    diff            JSONB,
    request_id      TEXT
);
CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_logs(ts DESC);

CREATE TABLE IF NOT EXISTS config_meta (
    id              INT PRIMARY KEY DEFAULT 1,
    version         BIGINT NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (id = 1)
);
INSERT INTO config_meta(id, version) VALUES (1, 0) ON CONFLICT DO NOTHING;

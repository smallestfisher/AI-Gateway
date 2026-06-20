# 03 · 数据库设计（ER + DDL）

> PostgreSQL 16。本 DDL 可直接 `psql -f` 执行。配置类表为「源」，运行时读内存缓存；日志/审计类表为「汇」。

---

## 1. ER 图

```
                       ┌─────────────┐
                       │   users     │
                       │  (消费用户)  │
                       └──────┬──────┘
              ┌───────────────┼────────────────┐
              ▼                                ▼
       ┌────────────┐                  ┌──────────────┐
       │  api_keys  │                  │  user_quotas │
       │  (鉴权)     │◀───(user_id)────│  (额度/限流)  │
       └─────┬──────┘                  └──────────────┘
             │ (rate-limit/usage 也写 Redis)
             │
   (api_key 解析出 user，请求经 router 选 provider)
             │
┌────────────┴──────────────────────────────────────────────┐
│                                                            │
│   ┌──────────────┐        ┌──────────────┐                │
│   │   providers  │───────▶│ proxy_egress │  (代理出口池)    │
│   │  (上游渠道)   │ (proxy)│              │                │
│   └──────┬───────┘        └──────────────┘                │
│          │ 1                                                 │
│          │                                                   │
│          │ N            ┌──────────────┐                    │
│          └─────────────▶│ model_channels│◀──────N───┐       │
│              (provider,  │ (绑定别名↔上游) │           │       │
│               upstream_  └──────┬────────┘           │       │
│               model)            │ N                  │ 1     │
│                                 ▼                    │       │
│                          ┌──────────────┐            │       │
│                          │   models     │────────────┘       │
│                          │ (统一别名)    │                    │
│                          └──────┬───────┘                    │
│                                 │ 1                          │
│                                 │                            │
│   ┌──────────────────┐    ┌─────┴────────────┐               │
│   │ client_profiles  │    │ router_policies  │  (路由策略     │
│   │ (出口伪装: UA/    │    │ scope: global/   │   global/model)│
│   │  Origin/Cookie)  │    │       model      │               │
│   │ scope: default/   │    └──────────────────┘               │
│   │  provider/model   │                                       │
│   └──────────────────┘                                       │
│                                                              │
│   ┌──────────────┐    ┌──────────────┐                      │
│   │  mcp_servers │───▶│ mcp_bindings │ (绑定到 model/client) │
│   └──────────────┘    └──────────────┘                      │
│                                                              │
│   ┌──────────────┐                                           │
│   │  protocols   │  (协议注册表：内置/自定义 Adapter 登记)     │
│   └──────────────┘                                           │
└──────────────────────────────────────────────────────────────┘

  汇聚（异步写入）:
  ┌──────────────┐   ┌──────────────┐   ┌──────────────┐
  │ request_logs │   │ audit_logs   │   │ config_meta  │
  │ (请求/可切CH) │   │ (后台操作)    │   │ (版本号热更新) │
  └──────────────┘   └──────────────┘   └──────────────┘
```

> **多租户（若启用完全隔离）**：所有配置表加 `owner_id UUID`（指向 users 或 organizations），查询统一加 `WHERE owner_id=$me`。本 DDL 按「混合模型」给出，不含 `owner_id`，注释标出加列位置。

---

## 2. DDL

### 2.1 消费用户与鉴权

```sql
CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL,
    email           TEXT UNIQUE,
    status          TEXT NOT NULL DEFAULT 'active',   -- active | disabled
    -- 完全多租户隔离时在此加: owner_id UUID
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE api_keys (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,                     -- 备注名
    key_prefix      TEXT NOT NULL,                     -- 展示用前缀（如 sk-aihub-abc..）
    key_hash        TEXT NOT NULL UNIQUE,              -- 存 hash（bcrypt/argon2 或 HMAC），不存明文
    scopes          JSONB NOT NULL DEFAULT '[]',       -- 允许的协议/模型/权限
    status          TEXT NOT NULL DEFAULT 'active',    -- active | revoked
    expires_at      TIMESTAMPTZ,
    last_used_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_api_keys_user ON api_keys(user_id);
CREATE INDEX idx_api_keys_hash ON api_keys(key_hash);

CREATE TABLE user_quotas (
    user_id         UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    balance         BIGINT NOT NULL DEFAULT 0,          -- 余额（按计价单位/分）
    -- 限流（也可仅用 Redis；这里存默认配额）
    rpm_limit       INT,
    tpm_limit       INT,
    model_whitelist JSONB NOT NULL DEFAULT '[]',        -- 允许的别名列表；空=全部
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### 2.2 上游与代理

```sql
CREATE TABLE proxy_egress (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL,
    type            TEXT NOT NULL,                      -- http | https | socks5
    url             TEXT NOT NULL,                      -- 含认证（secret 字段建议加密）
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE providers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    protocol        TEXT NOT NULL,                      -- openai_chat | openai_responses | anthropic_messages | google_gemini | custom
    base_url        TEXT NOT NULL,
    api_key_enc     BYTEA,                              -- 加密后的上游密钥（KMS/应用层加密）
    proxy_id        UUID REFERENCES proxy_egress(id),
    timeout_ms      INT NOT NULL DEFAULT 60000,
    connect_timeout_ms INT NOT NULL DEFAULT 10000,
    max_retries     INT NOT NULL DEFAULT 2,
    weight          INT NOT NULL DEFAULT 1,             -- 默认权重（可被 channel 覆盖）
    priority        INT NOT NULL DEFAULT 0,             -- 默认优先级
    enabled         BOOLEAN NOT NULL DEFAULT true,
    -- 健康阈值（供 health 模块读取；实时态在 Redis）
    hc_error_rate   DOUBLE PRECISION NOT NULL DEFAULT 0.3,
    hc_p95_ttft_ms  INT  NOT NULL DEFAULT 8000,
    hc_window_sec   INT  NOT NULL DEFAULT 60,
    hc_cooldown_sec INT  NOT NULL DEFAULT 30,
    metadata        JSONB NOT NULL DEFAULT '{}',        -- 额外配置（如 anthropic-version、自定义头模板）
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_providers_protocol ON providers(protocol) WHERE enabled;
```

### 2.3 模型与通道（别名映射核心）

```sql
CREATE TABLE models (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alias           TEXT NOT NULL UNIQUE,               -- 对客户端暴露的统一名，如 claude-sonnet
    display_name    TEXT NOT NULL,
    description     TEXT,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    -- 计费系数（input/output/cache_create/cache_read/reasoning，单位: 每千 token 价格×10000）
    pricing         JSONB NOT NULL DEFAULT '{}',
    metadata        JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE model_channels (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    model_id        UUID NOT NULL REFERENCES models(id) ON DELETE CASCADE,
    provider_id     UUID NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    upstream_model  TEXT NOT NULL,                      -- 该 provider 上的真实模型名
    weight          INT NOT NULL DEFAULT 1,
    priority        INT NOT NULL DEFAULT 0,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (model_id, provider_id, upstream_model)
);
CREATE INDEX idx_channels_model ON model_channels(model_id) WHERE enabled;
CREATE INDEX idx_channels_provider ON model_channels(provider_id);
```

### 2.4 Client Profile（出口伪装）

```sql
CREATE TABLE client_profiles (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL,
    scope           TEXT NOT NULL,                       -- default | provider | model
    target_id       UUID,                                -- scope=provider→provider_id; =model→model_id; default→null
    headers         JSONB NOT NULL DEFAULT '{}',         -- {"User-Agent":..., "anthropic-beta":...}
    user_agent      TEXT,
    origin          TEXT,
    referer         TEXT,
    cookies         JSONB NOT NULL DEFAULT '[]',         -- [{name,value,domain,path,...}]
    strip_client_headers BOOLEAN NOT NULL DEFAULT false, -- 是否剥离客户端原始头
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((scope='default' AND target_id IS NULL)
        OR (scope IN ('provider','model') AND target_id IS NOT NULL))
);
CREATE UNIQUE INDEX uq_profile_default ON client_profiles((scope='default')) WHERE scope='default';
CREATE INDEX idx_profile_provider ON client_profiles(target_id) WHERE scope='provider';
CREATE INDEX idx_profile_model ON client_profiles(target_id) WHERE scope='model';
```

### 2.5 协议注册表 & 路由策略

```sql
CREATE TABLE protocols (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code            TEXT NOT NULL UNIQUE,                -- openai_chat | custom:myproto
    display_name    TEXT NOT NULL,
    adapter_kind    TEXT NOT NULL,                       -- builtin | parameterized | plugin
    ingress_paths   JSONB NOT NULL DEFAULT '[]',         -- ["/v1/messages"]
    config          JSONB NOT NULL DEFAULT '{}',         -- 参数化映射表/插件路径
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE router_policies (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scope           TEXT NOT NULL,                        -- global | model
    model_id        UUID REFERENCES models(id) ON DELETE CASCADE, -- scope=model 时必填
    mode            TEXT NOT NULL,                        -- failover | weighted | auto
    params          JSONB NOT NULL DEFAULT '{}',          -- {max_attempts, retry_on:[...], auto_decay:...}
    enabled         BOOLEAN NOT NULL DEFAULT true,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((scope='global' AND model_id IS NULL) OR (scope='model' AND model_id IS NOT NULL))
);
CREATE UNIQUE INDEX uq_policy_global ON router_policies((scope='global')) WHERE scope='global';
```

### 2.6 MCP（预留）

```sql
CREATE TABLE mcp_servers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    transport       TEXT NOT NULL,                        -- stdio | sse | http
    command_url     TEXT NOT NULL,                        -- stdio:命令；sse/http:url
    env             JSONB NOT NULL DEFAULT '{}',
    enabled         BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE mcp_bindings (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mcp_server_id   UUID NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
    scope           TEXT NOT NULL,                        -- model | client | global
    target_id       UUID,
    permission      JSONB NOT NULL DEFAULT '{}',          -- 允许调用的工具白名单/自动批准策略
    enabled         BOOLEAN NOT NULL DEFAULT true,
    UNIQUE (mcp_server_id, scope, target_id)
);
```

### 2.7 日志与元数据（汇）

```sql
-- request_logs：前期落 PG（短留存），后期可整体迁 ClickHouse（结构保持一致）
CREATE TABLE request_logs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ts              TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id         UUID,
    api_key_id      UUID,
    client_protocol TEXT NOT NULL,
    model           TEXT NOT NULL,                        -- 别名
    provider_id     UUID,
    upstream_model  TEXT,
    stream          BOOLEAN NOT NULL DEFAULT false,
    status          TEXT NOT NULL,                        -- success | client_error | upstream_error | timeout | rate_limited | circuit_open
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
CREATE INDEX idx_logs_ts ON request_logs(ts DESC);
CREATE INDEX idx_logs_user_ts ON request_logs(user_id, ts DESC);
CREATE INDEX idx_logs_provider_ts ON request_logs(provider_id, ts DESC);
CREATE INDEX idx_logs_model_ts ON request_logs(model, ts DESC);

CREATE TABLE audit_logs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ts              TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_id        UUID,                                 -- 操作者（管理员）
    action          TEXT NOT NULL,                        -- provider.create | model.update ...
    target_type     TEXT NOT NULL,
    target_id       UUID,
    diff            JSONB,                                -- 变更前后
    request_id      TEXT
);
CREATE INDEX idx_audit_ts ON audit_logs(ts DESC);

-- 配置版本号：每次配置变更递增，Redis pub/sub 广播用
CREATE TABLE config_meta (
    id              INT PRIMARY KEY DEFAULT 1,
    version         BIGINT NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (id = 1)
);
INSERT INTO config_meta(id, version) VALUES (1, 0) ON CONFLICT DO NOTHING;
```

---

## 3. 设计说明

- **密钥加密**：`providers.api_key_enc`、`proxy_egress.url` 中的密钥部分用应用层加密（KMS / 主密钥），DB 不存明文。`api_keys.key_hash` 存不可逆 hash。
- **热更新触发**：Admin 写任何配置表后，事务内 `UPDATE config_meta SET version=version+1 RETURNING version`，并 `PUBLISH`；各实例收到重载。
- **JSONB 用途**：`scopes`/`params`/`pricing`/`metadata` 等可扩展字段用 JSONB，避免频繁加列；关键字段（status/enabled/外键）保持列式以便索引。
- **时序与分区**：`request_logs` 量大时按 `ts` 做月度分区（`PARTITION BY RANGE`）或迁 ClickHouse；表结构保持一致以便平滑迁移。
- **索引策略**：热查询是「按时间倒序的日志筛选」与「配置按 id 查」，索引围绕这两类；配置类表数据量小，主键/唯一索引足够。

---

## 4. 与模块的对应

| 表 | 主要消费模块 |
|----|-------------|
| users / api_keys / user_quotas | `auth` / `billing` / `ratelimit` |
| providers / proxy_egress | `egress`（TransportPool）/ `registry` |
| models / model_channels | `router` / `registry` |
| client_profiles | `profile`（ProfileIndex） |
| protocols | `adapter.Registry` / Admin「协议管理」 |
| router_policies | `router`（模式选择） |
| mcp_servers / mcp_bindings | `mcp`（预留） |
| request_logs | `log`（LogSink）/ Admin「日志中心」 |
| audit_logs | `admin`（操作审计） |
| config_meta | `registry`（热更新版本号） |

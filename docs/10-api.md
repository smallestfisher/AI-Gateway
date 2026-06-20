# 10 · API 设计

> 两个平面：**Proxy 面**（客户端调用网关）与 **Admin 面**（后台管理）。二者共用同一个 Fiber 进程，路由前缀隔离。

---

## 1. Proxy 面（客户端 → 网关）

### 1.1 鉴权
- `Authorization: Bearer <API_KEY>`（OpenAI/通用）或 `x-api-key: <API_KEY>`（Anthropic 风格）。
- 网关统一识别这两种头，解析为 user + scopes。

### 1.2 端点

| 方法 | 路径 | 协议 | 用途 |
|------|------|------|------|
| POST | `/v1/chat/completions` | openai_chat | OpenAI SDK / 兼容客户端 |
| POST | `/v1/responses` | openai_responses | Codex CLI / Agents SDK |
| POST | `/v1/messages` | anthropic_messages | Claude Code |
| GET  | `/v1/models` | — | 列出可用别名（OpenAI 风格 `{object:"list",data:[{id:alias,...}]}`） |
| GET  | `/v1/models/:alias` | — | 单个别名详情 |
| POST | `/v1beta/models/:model:generateContent` | google_gemini（预留） | Gemini 客户端 |
| POST | `/v1/mcp/...` | mcp（预留） | 见 `08-mcp.md §4` |

### 1.3 行为约定
- 请求体即各协议原生格式，网关**原样接受**（客户端无感）。
- 响应体即各协议原生格式（含 SSE 流）。
- 网关附加响应头：`X-Request-Id`、`X-Upstream-Model`、`X-Provider-Id`（便于排障，可配置是否暴露）。
- `GET /v1/models` 返回 `enabled` 且当前用户有权访问的别名。

### 1.4 错误模型（按入口协议回包）
错误**必须按客户端协议的格式返回**，否则客户端 SDK 解析失败：

| 协议 | 错误体格式 | 示例 |
|------|------------|------|
| openai_chat | `{error:{message,type,code,param}}` + HTTP 状态码 | `{"error":{"message":"rate limited","type":"rate_limit_exceeded"}}` |
| openai_responses | `{error:{code,message,...}}`（Responses 风格） | |
| anthropic_messages | `{type:"error",error:{type,message}}` + HTTP 状态码 | `{"type":"error","error":{"type":"rate_limit_error","message":"..."}}` |

**状态码映射**：
| 网关内部状态 | HTTP |
|--------------|------|
| API Key 无效/过期 | 401 |
| 权限不足（模型/协议） | 403 |
| 请求格式错误（Adapter 解析失败） | 400 |
| 配额不足 | 429（`quota_exceeded`） |
| 限流（RPM/TPM） | 429（`rate_limit_exceeded`，带 `Retry-After`） |
| 所有候选通道不可用（熔断/全失败） | 503（`no_available_channel`） |
| 上游错误（透传） | 502 |

---

## 2. Admin 面（后台 → 网关）

### 2.1 鉴权
- 管理员登录 → JWT（`Authorization: Bearer <jwt>`）。
- 所有写操作记 `audit_logs`。

### 2.2 约定
- 前缀：`/api/admin`。
- 风格：RESTful；列表支持 `?page&size&q&status&sort`；统一响应包络：
  ```json
  { "code": 0, "data": {...}, "message": "ok" }
  ```
  错误时 `code != 0`，`message` 描述。
- 写操作返回创建/更新后的实体。

### 2.3 资源端点

#### Provider
| 方法 | 路径 | 用途 |
|------|------|------|
| GET | `/api/admin/providers` | 列表（筛选/分页） |
| POST | `/api/admin/providers` | 新增 |
| GET | `/api/admin/providers/:id` | 详情 |
| PUT | `/api/admin/providers/:id` | 编辑 |
| PATCH | `/api/admin/providers/:id` | 局部更新（如 enabled） |
| DELETE | `/api/admin/providers/:id` | 删除 |
| POST | `/api/admin/providers/:id/test` | 测试连通（发一个最小探活请求，返回延迟/状态） |

#### Model & Channel
| 方法 | 路径 | 用途 |
|------|------|------|
| GET/POST | `/api/admin/models` | 列表/新增别名 |
| GET/PUT/DELETE | `/api/admin/models/:id` | 详情/编辑/删除 |
| GET/POST | `/api/admin/models/:id/channels` | 该别名的通道列表/新增通道 |
| PUT/DELETE | `/api/admin/model-channels/:id` | 编辑/删除通道 |

#### Client Profile
| 方法 | 路径 | 用途 |
|------|------|------|
| GET/POST | `/api/admin/client-profiles` | 列表/新增 |
| GET/PUT/DELETE | `/api/admin/client-profiles/:id` | 详情/编辑/删除 |
| POST | `/api/admin/client-profiles/:id/test` | 用该 Profile 发一个测试请求 |

#### Protocol
| 方法 | 路径 | 用途 |
|------|------|------|
| GET | `/api/admin/protocols` | 已注册协议（内置 + 自定义） |
| POST | `/api/admin/protocols` | 登记自定义/参数化协议 |
| PUT/DELETE | `/api/admin/protocols/:id` | 编辑/删除（内置仅可启停） |

#### Router Policy
| 方法 | 路径 | 用途 |
|------|------|------|
| GET | `/api/admin/router-policies` | 全局 + 各模型策略 |
| PUT | `/api/admin/router-policies/:id` | 设置模式/参数 |

#### MCP
| 方法 | 路径 | 用途 |
|------|------|------|
| GET/POST | `/api/admin/mcp/servers` | MCP server 列表/新增 |
| PUT/DELETE | `/api/admin/mcp/servers/:id` | 编辑/删除 |
| GET/POST | `/api/admin/mcp/bindings` | 绑定列表/新增 |

#### User / API Key / Quota
| 方法 | 路径 | 用途 |
|------|------|------|
| GET/POST | `/api/admin/users` | 用户列表/新增 |
| GET/PUT/DELETE | `/api/admin/users/:id` | 详情/编辑/删除 |
| GET/POST | `/api/admin/api-keys` | Key 列表/签发（返回明文仅一次） |
| DELETE | `/api/admin/api-keys/:id` | 吊销 |
| GET/PUT | `/api/admin/quotas/:user_id` | 额度/限流配置 |

#### 可观测
| 方法 | 路径 | 用途 |
|------|------|------|
| GET | `/api/admin/logs` | 请求日志查询（筛选 user/model/provider/status/时间） |
| GET | `/api/admin/metrics` | 汇总指标（请求数/成功率/平均延迟，按维度） |
| GET | `/api/admin/health` | Provider×model 健康面板（成功率/TTFT/错误率/熔断态） |
| GET | `/api/admin/dashboard` | Dashboard 卡片数据 |

#### 请求模拟器（重点）
```
POST /api/admin/simulator
Body: {
  "client_protocol": "anthropic_messages",
  "provider_id": "...",
  "upstream_model": "gpt-4o",
  "alias": "claude-sonnet",
  "raw_request": { ...原始客户端请求体... },
  "stream": false
}
Response: {
  "stages": {
    "raw":            { ...原始请求... },
    "unified":        { ...IR... },                 // ingressAdapter.DecodeRequest 结果
    "upstream_url":   "https://api.openai.com/v1/chat/completions",
    "upstream_headers": { "Authorization":"...", "User-Agent":"..." },
    "upstream_body":  { ...egressAdapter.BuildUpstreamBody 结果... },
    "stream_format":  "openai_chat_sse",            // 流式时标注
    "transform_notes": ["reasoning signature stripped (target not supported)", ...]
  }
}
```
- 不真实发送（dry-run），仅渲染各阶段，用于排查兼容性。
- 可选 `dry_run:false` 真实发送一次并返回真实响应。

---

## 3. 版本与变更

- 所有 Admin 写端点完成后触发 `config_meta.version++` 并广播失效（见 `02-modules.md §4`）。
- Admin API 自带 `/api/admin/version` 与 `/api/admin/healthz`。

---

## 4. 与后台模块对应

每个 Admin 资源端点 ↔ 一个后台页面 ↔ 一组 DB 表，三者一一对应（见 `09-frontend.md`）。这保证了「全可视化、不依赖配置文件」。

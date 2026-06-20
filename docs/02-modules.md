# 02 · 核心模块设计

> 后端 Go 包结构（占位 module `github.com/aigateway/ai-hub`）：
>
> ```
> cmd/gateway/            进程入口
> internal/
>   config/               配置加载（进程级：监听/DB/Redis）
>   server/               Fiber 装配、路由注册
>   ingress/              入口层（dispatch / middleware / 代理端点）
>   ir/                   Unified IR 类型（见 05）
>   adapter/              协议 Adapter（见 04）
>   router/               别名解析 + 选路策略
>   egress/               出口层（构建/发送/重试/流式转发）
>   profile/              Client Profile 解析与合并
>   registry/             配置 Registry（内存缓存 + 热更新）
>   health/               健康指标 + 熔断
>   ratelimit/            限流（令牌桶）
>   auth/                 AuthN/AuthZ + API Key
>   billing/              配额/额度扣减
>   log/                  异步日志（LogSink 接口，PG/CH 可切换）
>   mcp/                  MCP 预留（见 08）
>   admin/                Admin API
>   store/                PostgreSQL 访问层（pgx）
>   cache/                Redis 封装
> ```

---

## 1. Ingress（入口层）

职责：接收客户端请求 → 解析为 IR → 交给 Router/Egress。

```go
// 代理端点（按 path 绑定 Adapter）
POST /v1/chat/completions   → adapter.openai_chat
POST /v1/responses          → adapter.openai_responses
POST /v1/messages           → adapter.anthropic_messages
GET  /v1/models             → 列出可用别名
```

中间件链（顺序）：
1. **request_id**：生成/透传 `X-Request-Id`。
2. **AuthN**：`Authorization: Bearer <key>` → `auth.Resolve(key)`（Redis 缓存）→ `ClientContext`。失败返回协议对应的 401。
3. **RateLimit**：API Key 维度 + user×model 维度令牌桶（Redis）。
4. **Quota（预检）**：额度/余量预检（轻量；真正扣减在响应完成后）。
5. **Body 读取 + DecodeRequest**：`adapter.DecodeRequest(raw, hdr) → IR`。
6. 交给 **Pipeline**（Router → Egress → 回写）。

> **流式感知**：`Stream==true` 时，Ingress 立即向客户端写 SSE 头（200, `text/event-stream`），后续由 Egress 边转边写。首字节前发生错误仍可 failover；首字节后不可。

---

## 2. Router（选路）

```go
type Router interface {
    // Resolve 把别名解析为有序候选 Channel（已过滤熔断）
    Resolve(alias string, ctx *ir.ClientContext) ([]*Channel, error)
}

type Channel struct {
    ProviderID    string
    UpstreamModel string          // alias 已被解析为真实上游模型名
    Protocol      adapter.Protocol
    Provider      *Provider
    Profile       *ClientProfile  // 已解析的出口伪装
    Weight        int
    Priority      int
}
```

### 2.1 解析流程
```
1. registry.Models[alias] → model_channels 列表（DB 配置，内存缓存）
2. 过滤：enabled && 未被熔断（health.CircuitState == open 的剔除）
3. 按 priority 分层（数字小=高优先级，先尝试）
4. 层内按 weight 加权随机选一个；失败 failover 到同层/下层下一个
```

### 2.2 三种策略

| 模式 | 行为 |
|------|------|
| **failover** | 严格按优先级顺序，A 失败试 B，B 失败试 C |
| **weighted** | 层内按权重随机；失败按权重递补 |
| **auto** | 在 weighted 基础上，根据健康快照（成功率/TTFT/错误率）**动态调整有效权重**，劣化的通道被自动降权 |

> auto 模式的「动态权重」由 health 模块提供 `EffectiveWeight(channel)`，Router 调用即可，不直接读 Redis。

### 2.3 重试边界
- 只在**未向客户端首字节**前重试（见 `07-streaming.md §4`）。
- 可重试错误：5xx、超时、连接错误、429（若配置允许）、熔断。
- 不可重试：4xx 业务错误（含 400 参数错误、内容安全拒绝）、流式已 committed。
- 最大尝试次数 = 候选数 与 配置上限 取小。

---

## 3. Egress（出口层）

职责：把 IR 发给上游，收回响应/流，回交给 Ingress 编码。

```go
type Egress struct {
    adapters *adapter.Registry
    health   health.Store
    pool     *TransportPool   // 每 provider/代理一个 *http.Transport
}

// Send 执行一次出口（含 failover 由上层 Pipeline 编排，或在此内部循环）
func (e *Egress) Send(ctx context.Context, req *ir.UnifiedRequest,
    ch *Channel) (*ir.UnifiedResponse, error)   // 非流式

func (e *Egress) SendStream(ctx context.Context, req *ir.UnifiedRequest,
    ch *Channel, out chan<- ir.StreamEvent) error  // 流式：事件推入 out
```

### 3.1 一次出口的步骤
```
1. body  = egressAdapter.BuildUpstreamBody(req, ch.UpstreamModel)
2. url   = ch.Provider.BaseURL + 协议路径
3. hdr   = 基础头
         + 鉴权（Authorization / x-api-key，按协议）
         + ch.Profile 注入（UA/Origin/Referer/Cookie/自定义头）
         + 模型相关头（如 anthropic-version）
4. resp  = pool.Do(url, hdr, body, timeout, proxy)   # 重试在此层
5. 指标采样开始（TTFT 计时）
6. 解码：
     非流式 → egressAdapter.DecodeUpstreamResponse(body)
     流式   → egressAdapter.NewStreamDecoder()，逐 chunk Feed → IR 事件
7. 指标落 Redis（成功率/延迟/TTFT/token），日志入异步队列，配额扣减
```

### 3.2 TransportPool（连接池）
- key = `(providerID, proxyID)`；每个 key 一个独立 `*http.Transport`，独立连接复用与超时。
- 代理出口 IP 轮换：一个 Provider 可绑定多个 proxy，池内轮询/按健康选。
- MaxIdleConnsPerHost 调高，避免高频上游连接抖动。

---

## 4. Config Registry（配置热更新）

```go
type Registry struct {
    Providers map[string]*Provider
    Models    map[string]*Model          // alias → 模型 + channels
    Profiles  *ProfileIndex              // 按 scope 索引
    Policies  map[string]*RouterPolicy   // global / per-model
    version   uint64                     // 配置版本号
}
```

- **加载**：启动从 PG 全量加载到内存。
- **热更新**：
  - 方案 A（推荐）：Admin 写库后 `PUBLISH config:invalidate <version>`；各实例订阅，收到后**仅重载变更实体**（增量）或全量重载（视变更范围）。
  - 方案 B（兜底）：每 5s 轮询 `config_meta.version`，变了就重载。
- **并发安全**：`Registry` 内部用 `atomic.Pointer[Registry]` 整体替换（Copy-on-Write），读侧无锁。

---

## 5. Health / Circuit Breaker

```go
type Store interface {
    Record(providerID, model string, s Sample)        // 写采样
    Snapshot(providerID, model string, window time.Duration) Stats
    CircuitState(providerID, model string) State      // closed|open|halfOpen
}
type State int
const (
    StateClosed State = iota  // 正常放行
    StateOpen                 // 熔断，拒绝（Router 过滤掉）
    StateHalfOpen             // 半开，允许少量探测
)
```

- 维度：`(providerID, upstream_model)`。
- Redis 实现：滑动窗口计数（`BITSET`/`ZSET` 按时间分桶，或 `t-digest` 估 p95）。
- 熔断条件（可配置）：窗口内 `错误率 > ErrThreshold` 或 `p95(TTFT) > LatencyThreshold`。
- 恢复：open 持续 `Cooldown`（如 30s）后转 halfOpen，放 1 个探测请求；成功 → closed + 恢复权重，失败 → 重新 open。
- 自动降权：auto 路由下，未熔断但指标劣化的通道 `EffectiveWeight` 下调。

---

## 6. Client Profile 解析

```go
type ProfileIndex struct {
    Default *ClientProfile
    ByProvider map[string]*ClientProfile   // providerID → profile
    ByModel    map[string]*ClientProfile   // alias → profile
}

// Resolve 按优先级合并：Model > Provider > Default
func (p *ProfileIndex) Resolve(alias, providerID string) *ClientProfile
```

- 合并规则：Header map 按「高优先级覆盖低优先级」合并；UA/Origin/Referer/Cookie 同理。
- `Client Profile` 内容：`Headers map[string]string`、`UserAgent`、`Origin`、`Referer`、`Cookies []http.Cookie`、`StripClientHeaders bool`（是否剥离客户端原始头）。
- 解决场景：某 Claude 镜像要求特定 `User-Agent` 与 `anthropic-beta` 头；某公益站要求 `Origin`。

---

## 7. Auth / RateLimit / Billing

| 模块 | 职责 | 存储 |
|------|------|------|
| `auth` | API Key → user；权限校验（能否用某模型/协议） | Redis 缓存（TTL），PG 为源 |
| `ratelimit` | 令牌桶：API Key 维度 RPM/TPM；user×model 维度 | Redis（`INCR + EXPIRE` 或令牌桶脚本） |
| `billing` | 额度预检 + 完成后扣减（按 token 计费） | Redis（实时余额）+ PG（账单流水） |

- 配额扣减在响应/流结束后，基于 `Usage`（含 cache/reasoning token 分别计价）。
- 流式按最终 `message_delta` 的 usage 扣减。

---

## 8. Log（异步日志）

```go
type LogSink interface {
    Write(entry RequestLog) error  // 非阻塞：入队即返回
}
// 实现：PgSink（前期）、ClickHouseSink（后期）；由配置选择
```

- 写入走内存队列 → 批量 worker 落盘，**绝不阻塞热路径**。
- 队列满时丢弃最旧并计数（`log_dropped` 指标），保证网关可用性。
- 记录字段见 `03-database.md` 的 `request_logs`。

---

## 9. Pipeline（编排一次请求的总线）

```
Ingress.DecodeRequest
   │
   ▼
Pipeline.Run(req):
   candidates = router.Resolve(req.Model, req.Client)
   for ch in candidates:                     # failover 循环
       resp/err = egress.Send[Stream](req, ch)
       if err == nil: break
       if !retryable(err) || committed: return err
   # 选定成功通道后
   wire = ingressAdapter.Encode[Stream](resp)
   return wire
```

- Pipeline 是唯一串联 Ingress/Router/Egress/Health/Log/Billing 的地方，便于追踪与测试。
- 模拟器（后台「请求模拟器」）复用 Pipeline 的各阶段，但把「实际发送」替换为「渲染最终 URL/Header/Body/Stream 格式」（见 `09-frontend.md`）。

---

## 10. Admin API 模块

见 `10-api.md`。所有后台操作走 `internal/admin`，写库后触发 Registry 失效广播。

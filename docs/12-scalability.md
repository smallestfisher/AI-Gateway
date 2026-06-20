# 12 · 可扩展性设计（100+ Provider / 1000+ Model）

> 目标：在 100+ 上游、1000+ 模型别名、高并发流式场景下，热路径稳定低延迟、可水平扩展、配置秒级热更新。

---

## 1. 扩展性目标与负载画像

| 维度 | 量级 | 说明 |
|------|------|------|
| Provider | 100+ | 含官方/镜像/公益站/NewAPI/OpenRouter… |
| Model 别名 | 1000+ | 平均每别名 2~4 通道 → 3000~5000 条 `model_channels` |
| 并发请求 | 数千~万级 | 大量为长连接流式（SSE） |
| 配置体量 | 几千行关系数据 | 轻量，可全量驻留内存 |
| 日志吞吐 | 高 | 异步、可降级、可外迁 ClickHouse |

**核心结论**：配置量极小（内存无压力），瓶颈在**热路径延迟**与**连接/流式资源管理**，不在数据库。

---

## 2. 热路径零数据库（最重要）

单次推理请求的同步链路：

```
请求 ─▶ AuthN(Redis缓存) ─▶ 解析别名(内存map) ─▶ Adapter(纯函数) ─▶ Egress(连接池) ─▶ 回包
```

| 步骤 | 是否触达 DB | 实现 |
|------|------------|------|
| API Key 鉴权 | ❌（Redis 缓存，未命中再查 PG 并回填） | `auth` 模块，TTL ~5min |
| 别名→通道解析 | ❌（Registry 内存） | `router` 读 `Registry.Models` |
| Client Profile 解析 | ❌（内存 `ProfileIndex`） | `profile` 模块 |
| Adapter 转换 | ❌（纯计算） | — |
| 发送上游 | ❌（连接池复用） | `TransportPool` |
| 健康判定 | ❌（Redis 滑动窗口） | `health` 模块 |
| 限流 | ❌（Redis） | `ratelimit` |
| 配额扣减 | ❌（Redis 原子操作，PG 仅流水） | `billing` |
| 写日志 | ❌（入内存队列，异步落盘） | `log` |

> PG 只在**配置变更写**和**异步日志落盘**时被触达。读路径完全绕开 PG。

---

## 3. 配置 Registry 的内存缓存与热更新

```
PG(源) ──启动全量加载──▶ Registry(内存, atomic.Pointer COW)
                              ▲
        Admin 写库 ─▶ version++ ─▶ PUBLISH config:invalidate ─▶ 各实例订阅 ─▶ 增量/全量重载
```

- **COW 无锁读**：`Registry` 用 `atomic.Pointer[snapshot]`，读侧永远无锁；更新时整体构建新 snapshot 后原子替换。
- **热更新收敛**：Redis pub/sub 广播，~1s 内所有实例生效。
- **增量优化**：广播带「变更实体类型 + id」，实例只重载受影响部分；兜底全量重载（几千行，毫秒级）。
- **容量**：5000 通道 + 1000 模型 + 100 Provider + Profile/Policy ≈ 几万条指针，内存 < 50MB。

---

## 4. 水平扩展（无状态网关）

```
LB ─▶ [Gateway#1 .. Gateway#N]
        │ 共享：PG(配置/日志) + Redis(指标/熔断/限流/配额/缓存)
```

- **无状态**：任一副本服务任一请求；不依赖粘性会话。
- **流式连接**：SSE 由接管的那个副本独占完成（连接级状态在该副本内存）；副本宕机则该流中断（客户端 SDK 会重试）。
- **熔断一致性**：熔断态在 Redis（跨副本共享），不是本地内存。
- **配置一致**：所有副本订阅同一失效广播。
- **扩缩容**：新副本启动即全量加载配置 + 订阅广播，秒级就绪。

---

## 5. 连接与出口管理

- **TransportPool**：key = `(provider_id, proxy_id)`，每个 key 一个独立 `*http.Transport`：
  - 独立 `MaxIdleConnsPerHost`（调高，如 256），复用 TCP/TLS。
  - 独立超时（来自 Provider 配置）。
- **代理出口 IP 池**：一个 Provider 可绑多 proxy；按健康/轮换选，避免单 IP 被上游限速/封禁。
- **TLS 复用**：对 HTTPS 上游保持长连接，减少握手。
- **流式背压**：上游→网关→客户端逐块转发，不在网关缓冲整段；客户端慢则反压上游读取。

---

## 6. 指标与日志的可扩展性

### 6.1 指标（Redis）
- 滑动窗口计数：用 Redis `ZSET`（score=时间戳）或分桶 `INCR` + `EXPIRE`。
- p95 估计：`t-digest` 或固定桶直方图（省内存）。
- 熔断/限流判定全部走 Redis（O(log n) 或 O(1)）。
- **不进 PG**：避免每请求写库。

### 6.2 日志（异步 + 可外迁）
```
热路径 ─▶ 内存有界队列 ─▶ worker 批量 ─▶ LogSink(PgSink / ClickHouseSink)
                            │ 队列满 ─▶ 丢弃最旧 + log_dropped 计数
```
- 批量写入（每 N 条或 T 毫秒 flush）。
- ClickHouse 切换：实现 `LogSink` 接口即可，表结构与 `request_logs` 一致。
- PG 阶段用**分区表**（按月）控制单表体积。

---

## 7. 限流与配额的可扩展性

- **令牌桶（Redis Lua）**：原子取令牌，支持 RPM/TPM；维度 = API Key、user×model。
- **配额扣减**：Redis 原子 `DECRBY`（基于 token 估算/最终值）；PG 仅记流水（幂等键防重复扣）。
- **分布式一致**：所有限流/配额状态在 Redis，跨副本一致。

---

## 8. Adapter / 协议扩展性

- 新增协议 = 实现一个 Adapter，注册即可（见 `04-plugin-adapters.md`）。
- Adapter 无状态、纯函数 → 可被并发安全复用，单实例服务所有协议组合。
- 100+ Provider 用 4~6 种协议 → Adapter 数量极少；Provider 数量与 Adapter 无关。

---

## 9. 容量规划参考

| 资源 | 100 Provider / 1000 Model / 5000 通道 | 估算 |
|------|--------------------------------------|------|
| 配置内存 | 全量驻留 | < 50MB / 实例 |
| 连接池 | 每 (provider,proxy) 一个 Transport | ~数百 Transport，每池数百空闲连接 |
| Redis | 指标窗口 + 限流 + 熔断 + 缓存 | 单实例足够；可读写分离/集群 |
| PG | 配置(小) + 日志(分区/迁CH) | 配置库轻量；日志库独立 |
| 网关副本 | CPU 主要消耗在 SSE 转发 + JSON 编解码 | 按并发流数水平扩 |

---

## 10. 扩展性 Checklist（验收）

- [x] 热路径无 PG 读（Redis/内存命中）。
- [x] 配置变更秒级热生效，无需重启。
- [x] 网关无状态，可水平扩缩容。
- [x] 熔断/限流/配额跨副本一致（Redis）。
- [x] 日志异步、可降级、可外迁 ClickHouse。
- [x] 新增 Provider/协议不改核心代码。
- [x] 流式转发不缓冲整段，背压传递。
- [x] 压测下 TTFT/延迟稳定，资源随副本线性。

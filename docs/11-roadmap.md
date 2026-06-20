# 11 · 开发路线图

> 每个 Phase 有明确**验收标准**。可在 P1 完成后即对外提供最小可用服务，后续迭代增能。

---

## Phase 0 — 工程脚手架（~3 天）

**产出**：
- Go module `github.com/aigateway/ai-hub`：目录骨架（见 `02-modules.md`）、Fiber 装配、健康检查端点。
- PostgreSQL 迁移（golang-migrate / goose）：`03-database.md` 的全部 DDL。
- Redis 连接封装。
- `apps/web` Next.js 初始化 + shadcn/ui + 登录骨架。
- `docker-compose.yml`：gateway + postgres + redis（一键起本地环境）。
- CI：lint（golangci-lint）、vet、build。

**验收**：`docker compose up` 起来，`/healthz` 200，迁移跑通，前端登录页可访问。

---

## Phase 1 — 核心链路打通（~1.5 周）

**目标**：端到端跑通「一个入口协议 ↔ 一个出口协议」的一条链路（非流式 → 流式）。

**范围**：
- IR 全套类型（`05-unified-protocol.md`）。
- 两个 Adapter：`openai_chat`、`anthropic_messages`（含工具调用 + thinking）。
- Ingress（dispatch + 中间件骨架，AuthN 先用静态 key）。
- Egress（BuildUpstream + 发送 + 非流式解码）。
- 单 Provider、单 Channel（无路由策略，直连）。
- 非流式 → 流式（流式编解码状态机）。

**验收**：
- Claude Code 指向网关 `/v1/messages`，后端接 OpenAI 官方 `/v1/chat/completions`，能完成**带工具调用的多轮对话**（参考 `06-tool-calling.md §7` 走查）。
- 流式：首 token、工具入参增量、`stop_reason` 正确（参考 `07-streaming.md §5`）。
- Adapter「无损往返」单测全绿。

---

## Phase 2 — Responses + 路由 + 出口能力（~1.5 周）

**目标**：覆盖 Codex CLI；具备多上游路由与客户端伪装能力。

**范围**：
- `openai_responses` Adapter（Codex）。
- Router：failover + weighted；模型别名 ↔ 多 Channel。
- Client Profile 解析（default/provider/model 三级合并）+ Egress 注入 UA/Origin/Cookie/头。
- Egress：TransportPool、超时、重试、failover（首字节前）。
- Auth：DB 化的 API Key + 用户；限流（令牌桶）。

**验收**：
- Codex CLI 指向网关 `/v1/responses` 正常工作。
- 一个别名绑 2~3 个 Provider，手工熔断/停用 A，流量自动切到 B（首字节前 failover）。
- 公益站要求特殊 UA/Origin 时，通过 Client Profile 成功访问。
- 限流：超 RPM 返回协议正确的 429。

---

## Phase 3 — 健康熔断 + auto 路由 + Admin Web 核心（~2 周）

**范围**：
- Health 模块：Redis 滑动窗口、`(provider,model)` 熔断（open/half/closed）、自动降权。
- Router `auto` 模式（基于健康动态权重）。
- Admin API + 后台页面：Provider / 模型 / 通道 / Client Profile / 用户 / API Key / Quota。
- 配置热更新（`config_meta.version` + Redis pub/sub）。

**验收**：
- 后台可视化完成「新增 Provider → 绑定通道 → 设路由策略」全流程，秒级热生效，无需重启。
- 模拟故障：某 Provider 错误率飙升 → 自动熔断 → 恢复后自动 closed。
- Dashboard 卡片有真实数据。

---

## Phase 4 — 可观测 + 模拟器 + MCP 注册（~1.5 周）

**范围**：
- 请求模拟器（`/api/admin/simulator` + 后台页面，分阶段展示，dry-run）。
- 日志中心（异步落 PG，查询/筛选/导出）。
- 健康监控页。
- 审计日志（`audit_logs`）。
- MCP：注册表 CRUD + 工具发现接口（`Discover` 空实现就位，见 `08-mcp.md`）。

**验收**：
- 模拟器能渲染「原始 → IR → 出口 URL/Header/Body/流格式 + 转换备注」。
- 日志中心能按用户/模型/Provider/状态/时间筛选，详情可看转换备注。
- MCP server 可在后台登记（不要求可调用）。

---

## Phase 5 — 规模化 + 扩展协议（持续）

**范围**：
- 接入 ClickHouse（`LogSink` 切换），请求日志与历史指标迁出 PG。
- 更多 Adapter：`google_gemini`、`openrouter`、Bedrock。
- 连接池调优、代理出口 IP 池轮换、指标分片。
- 网关侧 MCP 工具执行（v2，`Call` + 会话状态）。
- 多副本部署验证（无状态、流式单实例承接、配置一致）。

**验收**：
- 压测：1000+ 并发流式请求稳定，TTFT/延迟达标，内存/CPU 线性。
- 日志量增长不再拖累 PG。
- Gemini/OpenRouter 客户端可经网关使用。

---

## 里程碑速览

| 里程碑 | 可对外能力 |
|--------|-----------|
| M1（P1 末） | Claude Code/OpenAI SDK 经网关用 1 个上游，工具调用 + 流式可用 |
| M2（P2 末） | Codex 可用；多上游 failover/weighted；客户端伪装 |
| M3（P3 末） | 完整后台可视化 + 健康熔断 + auto 路由（**生产可用的最小 SaaS**） |
| M4（P4 末） | 全可观测 + 模拟器 + MCP 注册 |
| M5（P5） | 规模化 + 多协议扩展 |

---

## 风险与对策

| 风险 | 对策 |
|------|------|
| 协议互转的隐蔽丢字段 | Adapter 金标准单测 + 模拟器可视化转换备注 |
| 推理签名持久化踩坑 | 集中在 `05 §5` 处理；egress 降级必须留日志 |
| 流式跨 chunk 解析错误 | Decoder 用行缓冲状态机；真实分片样本测试 |
| 配额/计费精度 | token 分类计价（cache/reasoning 单独）；扣减幂等 |
| 上游变更致兼容破坏 | 模拟器 + 日志快速定位；Adapter 版本化 |

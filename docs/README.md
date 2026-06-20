# AI Agent Gateway — 设计文档

> 生产级 AI Agent Gateway。让 Codex CLI / Claude Code / OpenAI SDK 通过统一入口无感访问任意上游。
> 内核：一套 **Unified IR（block 模型）** 作为所有协议互转的枢纽；每协议一个双向 **Adapter**，新增协议零改核心。

这是**设计文档**（非工程代码）。工程实现按 `11-roadmap.md` 分阶段推进。

---

## 📑 文档导航

| # | 文档 | 内容 | 建议先读 |
|---|------|------|---------|
| 00 | [项目总览](./00-overview.md) | 定位、设计原则、决策记录(ADR)、术语表、技术栈 | ✅ |
| 01 | [系统架构](./01-architecture.md) | 完整架构图、请求生命周期、部署拓扑 | ✅ |
| 05 | [Unified Protocol (IR)](./05-unified-protocol.md) | **核心 IR**：Block 模型、Request/Response、字段映射矩阵 | ✅ |
| 04 | [插件 / Adapter](./04-plugin-adapters.md) | 双向 Adapter 接口、注册表、职责切分、新增协议指南 | |
| 06 | [Tool Calling](./06-tool-calling.md) | 工具全生命周期、三协议互转矩阵、并行/流式工具调用 | |
| 07 | [Stream Event](./07-streaming.md) | 统一流事件模型、三协议事件映射、状态机、failover 约束 | |
| 02 | [核心模块](./02-modules.md) | Ingress/Router/Egress/Registry/Health/Auth/Log/Pipeline | |
| 03 | [数据库](./03-database.md) | ER 图 + **可执行 DDL** | |
| 08 | [MCP 预留](./08-mcp.md) | Registry/Routing/Permission 接口与 v1/v2 范围 | |
| 10 | [API 设计](./10-api.md) | Proxy 面 + Admin 面 + 错误模型 + 模拟器接口 | |
| 09 | [Web 后台原型](./09-frontend.md) | 各模块 ASCII 线框图（含请求模拟器） | |
| 11 | [开发路线图](./11-roadmap.md) | Phase 0–5 里程碑与验收标准 | |
| 12 | [可扩展性](./12-scalability.md) | 100+Provider/1000+Model 的热路径、配置分发、水平扩展 | |

**推荐阅读顺序**：00 → 01 → 05（IR）→ 04 → 06 → 07 → 02 → 03 → 08 → 10 → 09 → 11 → 12

---

## 🎯 一句话理解

```
Client Protocol ──ingress Adapter──▶ Unified IR ──egress Adapter──▶ Provider Protocol
                                     (block 模型)
```
- 入口协议（客户端用的）与出口协议（上游用的）相互独立，任意组合由 IR 居中翻译。
- Adapter 只做协议体纯转换；传输层（URL/鉴权/Header/代理/重试/模型名重写）下沉到 Egress。
- 热路径零数据库（鉴权/解析/限流/熔断全在 Redis/内存），网关无状态可水平扩展。

---

## 🔑 关键设计决策（摘自 `00-overview.md` ADR）

| 决策 | 选择 |
|------|------|
| 内部中心协议 | Unified IR（block 模型）—— 不以 Chat Completions 为中心 |
| 协议转换 | 每协议一个双向 Adapter |
| 配置存储 | PostgreSQL（源）+ 内存缓存 + Redis 失效广播 |
| 指标/熔断 | Redis 滑动窗口 |
| 日志存储 | PG（前期）→ ClickHouse（可选，后期），`LogSink` 接口平滑切换 |
| 租户模型 | **混合（待确认）**：全局 Provider + 按用户配额 |
| 流式语法 | Anthropic 式 block 生命周期（表达力最强） |
| 技术栈 | Go+Fiber / PostgreSQL+Redis / Next.js+shadcn/ui |

---

## ⚠️ 待确认项

- **租户模型（ADR-06）**：当前文档按「混合模型」描述（运营方全局管理 Provider/模型/Profile，用户持 API Key 消费并带独立额度/限流/权限）。如需「完全多租户隔离」，所有配置表加 `owner_id` + 查询隔离即可，改动可控。请确认。

---

## 📦 下一步

评审通过后，按 `11-roadmap.md` 进入 Phase 0（脚手架）→ Phase 1（核心链路）工程实现。

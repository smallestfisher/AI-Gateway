# 00 · 项目总览与设计原则

> AI Agent Gateway —— 让 Codex CLI / Claude Code / OpenAI SDK 通过统一入口无感访问任意上游。

---

## 1. 项目定位

| 维度 | 说明 |
|------|------|
| **是什么** | 生产级 **AI Agent Gateway（AI 智能体网关）** |
| **不是什么** | 不是聊天聚合平台，不是 OneAPI / NewAPI 的二次封装 |
| **核心服务对象** | Codex CLI、Claude Code、OpenAI SDK（后续：OpenAI Agents SDK / Cursor / Cline / Aider / Roo Code） |
| **核心目标** | 客户端只需配置 `https://gateway.xxx.com` + `API_KEY`，即可无感切换 OpenAI / Anthropic / Gemini / Qwen / DeepSeek / 公益站 / NewAPI / Sub2API / OpenRouter |

### 1.1 它与 Chat Gateway 的本质区别

传统 Chat Gateway 以 **Chat Completions** 为内部中心，所有上游都「翻译成 Chat 格式」。这条路在 Agent 场景会**信息丢失**：

- OpenAI Responses API 的 `reasoning` item（o-series 推理签名）在 Chat 格式里无处安放；
- Anthropic 的 `content blocks`（thinking / tool_use / tool_result 混排）拍平成 Chat 会丢失块边界与配对关系；
- 流式事件三套完全不同的语法（Chat delta / Responses item 事件 / Anthropic content_block 事件），强行对齐到 Chat 会破坏工具调用与推理的流式增量。

因此本项目以一套**更富表达能力的 Unified IR（统一中间表示）**为中心，Chat / Responses / Messages / Gemini 都只是它的「外方言」。

### 1.2 重点解决的问题

```
协议兼容       —— Chat / Responses / Messages / Gemini 之间互转，无损
工具调用兼容   —— OpenAI Function Calling / Anthropic Tool Use / MCP 统一
流式事件兼容   —— 三套流语法统一为一种内部事件序列
模型路由       —— 别名 → 多通道，failover / weighted / auto
客户端模拟     —— Client Profile 注入 UA/Origin/Cookie，绕过上游客户端限制
上游管理       —— Provider 动态增删改、热更新、健康熔断
```

---

## 2. 设计原则

1. **IR First，不以任何单一协议为中心**
   内部标准协议 = Unified Request / Response / Tool Call / Stream Event。所有协议先转成 IR，再从 IR 转出。

2. **Adapter 无状态、纯函数**
   协议体 ↔ IR 的转换不触碰网络、不读配置。传输层职责（URL / 鉴权 / Header / 代理 / 重试）全部下沉到 **Egress 层**。这样 Adapter 可单测、可复用、可热插拔。

3. **热路径零数据库**
   单次推理请求的同步链路上不查 PostgreSQL。鉴权（Redis 缓存）、别名解析（内存 map）、Adapter（纯函数）、Egress（连接池）全部命中内存/Redis。DB 只在配置变更与异步落日志时触达。

4. **配置即数据，全部后台可视化**
   禁止依赖配置文件驱动运行时行为（除进程级参数如监听端口、DB/Redis 连接串）。所有 Provider / 模型 / Profile / 路由策略通过 Web 后台管理，落库后秒级热生效。

5. **协议可插拔**
   新增协议（Gemini / OpenRouter / 自定义）= 实现一个 Adapter 并在后台注册，不改动核心代码。

6. **网关无状态、可水平扩展**
   实例间无共享内存状态；状态全部落在 PostgreSQL（配置）+ Redis（指标/熔断/限流/会话）。流式连接由单实例承接，负载均衡器无需粘性。

7. **可观测优先**
   每个请求有唯一 `request_id`，贯穿日志/指标/模拟器；TTFT、首块延迟、上下游 token、停机原因全量采样。

---

## 3. 决策记录（ADR 摘要）

| # | 决策 | 选择 | 理由 |
|---|------|------|------|
| ADR-01 | 内部中心协议 | Unified IR（block 模型） | 唯一能无损表达三种主流协议的表示（见 §1.1） |
| ADR-02 | 协议转换粒度 | 每协议一个双向 Adapter | 新增协议零改核心，职责单一 |
| ADR-03 | 配置存储 | PostgreSQL（源）+ 内存缓存 + Redis 失效广播 | 热路径零 DB，秒级热更新 |
| ADR-04 | 指标/熔断 | Redis 滑动窗口 | 低延迟、天然适合计数与窗口统计 |
| ADR-05 | 日志存储 | PG（前期）→ ClickHouse（可选，后期） | 接口预留 `LogSink`，平滑切换 |
| ADR-06 | 租户模型 | **混合（待最终确认）**：全局 Provider + 按用户配额 | 贴合 SaaS；若改全隔离仅加 `owner_id` |
| ADR-07 | 流式事件语法 | Anthropic 式 `block 生命周期` | 表达力最强，能向下兼容 Chat/Responses |
| ADR-08 | 后端框架 | Go + Fiber | 高并发、低分配、SSE 友好 |
| ADR-09 | 前端 | Next.js + shadcn/ui | 后台可视化、SSR、组件生态 |

> **ADR-06 待你最终确认。** 本文档及全系列默认按「混合模型」描述。如需「完全多租户隔离」，所有配置表加 `owner_id` + 查询加隔离条件即可，改动可控。

---

## 4. 术语表

| 术语 | 含义 |
|------|------|
| **IR** | Intermediate Representation，统一中间表示（Unified Request/Response） |
| **Adapter** | 协议适配器，负责某协议的 wire ↔ IR 双向转换 |
| **Protocol** | 协议标识：`openai_chat` / `openai_responses` / `anthropic_messages` / `google_gemini` … |
| **Provider** | 上游渠道（OpenAI / Anthropic / 某公益站 / NewAPI 实例 …） |
| **Model（别名）** | 对客户端暴露的统一模型名，如 `claude-sonnet` |
| **Channel（model_channel）** | 别名到一个具体 (Provider, upstream_model) 的绑定，带权重/优先级 |
| **Client Profile** | 出口客户端伪装包（UA/Origin/Referer/Cookie/特殊 Header） |
| **Block** | IR 的内容原子单元（text / image / tool_use / tool_result / thinking / reasoning） |
| **Egress** | 出口层，负责传输：URL/鉴权/Header/代理/重试/模型名重写/流式转发 |
| **TTFT** | Time To First Token，首 token 延迟 |
| **MCP** | Model Context Protocol，工具/资源协议，本系列做接口预留 |

---

## 5. 技术栈

| 层 | 选型 |
|----|------|
| 后端 | Go 1.22+、Fiber v2、pgx（PostgreSQL 驱动）、go-redis |
| 数据库 | PostgreSQL 16（配置/关系/日志）、Redis 7（指标/熔断/限流/缓存） |
| 日志（可选） | ClickHouse（海量请求日志与时序） |
| 前端 | Next.js 14（App Router）、React、Tailwind、shadcn/ui、TanStack Query |
| 部署 | Docker / docker-compose / K8s（无状态网关多副本） |
| Go module（占位） | `github.com/aigateway/ai-hub`（可改） |

---

## 6. 文档导航

见 `docs/README.md`。建议阅读顺序：00 → 01 → 05（IR）→ 04 → 06 → 07 → 02 → 03 → 08 → 10 → 09 → 11 → 12。

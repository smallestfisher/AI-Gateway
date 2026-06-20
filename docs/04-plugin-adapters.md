# 04 · 插件 / Adapter 架构

> 一个协议 = 一个 Adapter。新增协议 = 实现一个 Adapter 并注册，零改核心。

---

## 1. Adapter 接口

每个 Adapter 覆盖**一个协议的两个方向、四类操作**：

```go
package adapter

import "net/http"

// Protocol 协议标识
type Protocol string

const (
    ProtocolChat      Protocol = "openai_chat"        // /v1/chat/completions
    ProtocolResponses Protocol = "openai_responses"   // /v1/responses
    ProtocolMessages  Protocol = "anthropic_messages" // /v1/messages
    ProtocolGemini    Protocol = "google_gemini"      // /v1beta/models/...:generateContent
    // 可扩展：openrouter / custom / ...
)

// Adapter 协议适配器（无状态、纯转换）
type Adapter interface {
    Protocol() Protocol

    // ============ 入口方向：client wire ↔ IR ============

    // DecodeRequest 把客户端原始请求体解析成 IR。
    // hdr 用于读取协议特有的头（如 anthropic-version / 用户自定义透传头）。
    DecodeRequest(raw []byte, hdr http.Header) (*ir.UnifiedRequest, error)

    // EncodeResponse 把 IR 非流式响应编码为客户端协议的 JSON。
    EncodeResponse(resp *ir.UnifiedResponse) ([]byte, error)

    // NewStreamEncoder 创建流式编码器：IR 事件 → 客户端 SSE 字节。
    // 流式编码有状态（需跟踪块索引/usage 累积），故返回有状态对象。
    NewStreamEncoder() StreamEncoder

    // ============ 出口方向：IR ↔ upstream wire ============

    // BuildUpstreamBody 把 IR 构造为上游协议的请求体 JSON。
    // 注意：只生成 body，不涉及 URL/Header/鉴权（那是 Egress 的职责）。
    BuildUpstreamBody(req *ir.UnifiedRequest, upstreamModel string) ([]byte, error)

    // DecodeUpstreamResponse 把上游非流式响应解析成 IR。
    DecodeUpstreamResponse(raw []byte) (*ir.UnifiedResponse, error)

    // NewStreamDecoder 创建流式解码器：上游 SSE chunk → IR 事件。
    // 流式解码有状态（跨 chunk 拼接 JSON、跟踪 block 生命周期）。
    NewStreamDecoder() StreamDecoder
}

// StreamEncoder IR 事件 → 客户端 SSE
type StreamEncoder interface {
    // Encode 把一个 IR 事件转成一段 SSE（可返回 nil 表示该事件在本协议下不产生输出）
    Encode(ev ir.StreamEvent) ([]byte, error)
    // Flush 结束流：发送协议要求的终止帧（如 Chat 的 data: [DONE]）
    Flush() ([]byte, error)
}

// StreamDecoder 上游 SSE chunk → IR 事件（可能 0 个或多个）
type StreamDecoder interface {
    // Feed 喂入一段原始字节（可能是不完整的 SSE 帧），返回解析出的 IR 事件。
    Feed(chunk []byte) ([]ir.StreamEvent, error)
    // Finalize 上游流结束时，冲刷残留状态。
    Finalize() ([]ir.StreamEvent, error)
}
```

### 1.1 为什么请求方向用 Decode/Build 而非对称 Encode/Decode

- **入口**用 `DecodeRequest`：客户端 wire 是「事实输入」，解析 + 校验 + 规整为 IR。
- **出口**用 `BuildUpstreamBody`：构造上游 body 时还需要「模型名重写、按 provider 能力降级（如剥离 reasoning）」，语义上是「按目标构建」而非简单编码。
- 命名不对称是为了**强调两边职责差异**，避免误以为可逆。响应方向同理：`DecodeUpstreamResponse`（上游→IR）vs `EncodeResponse`（IR→客户端）。

---

## 2. Adapter 与 Egress 的职责切分（关键）

```
┌─────────────────────────────────────────────────────────────┐
│ Adapter（协议层，纯函数，可单测）                             │
│  · wire ↔ IR 的结构转换                                       │
│  · 工具调用 / 推理 / 多模态的字段映射                          │
│  · 流式事件的状态机                                           │
│  ✗ 不碰：URL / 鉴权 / Header / 代理 / 重试 / 模型名映射表      │
└─────────────────────────────────────────────────────────────┘
                          │ 仅产出 body（[]byte）
                          ▼
┌─────────────────────────────────────────────────────────────┐
│ Egress（传输层，有 IO/状态）                                  │
│  · 组装 URL（Provider.BaseURL + 协议路径）                    │
│  · 鉴权注入（Provider.APIKey → Authorization/x-api-key）      │
│  · Client Profile 注入（UA/Origin/Referer/Cookie/Header）     │
│  · 模型名重写（alias → upstream_model，已由 Router 给定）     │
│  · 代理出口选择 / 连接池                                      │
│  · 超时 / 重试 / failover                                     │
│  · 流式字节转发                                               │
└─────────────────────────────────────────────────────────────┘
```

> 这条边界让 Adapter **可以不启动任何服务就做完整单测**（给定 wire，断言 IR；给定 IR，断言 wire），大幅提升正确性保障。

---

## 3. 注册表（Adapter Registry）

```go
package adapter

type Registry struct {
    mu       sync.RWMutex
    byProto  map[Protocol]Adapter
}

func NewRegistry(builtin ...Adapter) *Registry { /* 注册内置 Adapter */ }

// Register 注册/覆盖一个协议 Adapter（热插拔，自定义协议用）
func (r *Registry) Register(a Adapter) { /* ... */ }

// Get 按协议取 Adapter
func (r *Registry) Get(p Protocol) (Adapter, bool) { /* ... */ }

// ByPath 根据入口路径推断协议（用于 Route Dispatch）
//   /v1/chat/completions → openai_chat
//   /v1/responses        → openai_responses
//   /v1/messages         → anthropic_messages
//   /v1beta/models/...   → google_gemini
func (r *Registry) ByPath(path string) (Protocol, bool) { /* ... */ }

// Protocols 列出已注册协议（供后台「协议管理」展示）
func (r *Registry) Protocols() []Protocol { /* ... */ }
```

内置 Adapter 在进程启动时注册；自定义协议通过后台「协议管理」上传/启用后，调用 `Register` 注入（插件以 Go plugin 或预编译形式加载，见 §5）。

---

## 4. 一次请求如何选用 Adapter

```
1. Route Dispatch:  path = /v1/messages  →  clientProto = anthropic_messages
2. ingressAdapter  = registry.Get(clientProto)            // 入口用
3. Router 解析 alias → Channel{ProviderID, Provider.Protocol, upstreamModel}
4. egressAdapter   = registry.Get(provider.Protocol)      // 出口用（可能与入口不同！）
5. 转换链:
     client wire ──ingressAdapter.DecodeRequest──▶ IR
     IR ──egressAdapter.BuildUpstreamBody──▶ upstream body ──Egress 发出──▶ upstream
     upstream resp ──egressAdapter.DecodeUpstream*──▶ IR
     IR ──ingressAdapter.Encode*──▶ client wire
```

> 入口协议与出口协议相互独立。例：客户端用 Anthropic Messages（Claude Code），上游是 OpenAI Chat 兼容的公益站 —— `ingressAdapter=Messages`，`egressAdapter=Chat`，IR 居中翻译。

---

## 5. 协议可插拔机制

支持三种「新增协议」方式，按侵入度递增：

### 5.1 内置 Adapter（推荐，最常见）
- 在 `internal/adapter/<proto>/` 下实现 `Adapter` 接口，启动时注册。
- 适合主流协议：gemini、openrouter、bedrock 等。

### 5.2 后台「协议管理」自定义映射（零代码，有限场景）
- 对于「本质是某内置协议的方言」（如某 NewAPI 实例只是改了路径/字段名），后台提供**协议模板 + 字段映射表**，运行时用一个「参数化 Adapter」解释执行。
- 适合：字段名差异、路径差异、额外必填头。

### 5.3 编译期插件 / 外挂 Adapter（高级）
- 复杂私有协议：实现接口，编译为独立二进制或 Go plugin，后台启用。
- 仅在协议语义与内置差异巨大时使用。

### 5.4 新增协议 Checklist
1. 定义 `Protocol` 常量与入口路径。
2. 实现四个核心方法 + 流式编解码器。
3. 编写「无损往返」单测：`(wire→IR→wire)` 与 `(IR→wire→IR)` 语义等价。
4. 提供一份真实抓包样本作为金标准测试。
5. 在后台「协议管理」登记，可被 Provider 选择。

---

## 6. Adapter 单测金标准（验收）

每个 Adapter 必须通过：

| 测试 | 内容 |
|------|------|
| `TestDecodeRequest_*` | 各类请求（纯文本 / 多模态 / 工具 / 推理 / 并行工具）wire → IR |
| `TestEncodeResponse_*` | IR → wire，含工具调用与推理块 |
| `TestRoundTrip` | `wire → IR → wire` 语义等价（结构对比，忽略空白） |
| `TestBuildUpstream_*` | IR → 上游 body，含模型名重写、降级（剥离不支持字段） |
| `TestStreamDecode_*` | 用真实分片样本（跨 chunk 的 SSE）喂入，断言 IR 事件序列 |
| `TestStreamEncode_*` | 给定 IR 事件序列，断言输出 SSE 含协议终止帧 |

> 流式分片测试尤其重要：上游 SSE 帧可能在一个 TCP 包里粘多条、也可能一条 JSON 被拆到多个包。Decoder 必须用「缓冲 + 行解析」处理，不能假设 chunk 边界 == 事件边界。

---

## 7. 与其它文档的关系

- IR 类型定义见 `05-unified-protocol.md`。
- 工具调用如何在 Adapter 间互转见 `06-tool-calling.md`。
- 流式事件状态机见 `07-streaming.md`。
- Adapter 在 Egress 中如何被驱动见 `02-modules.md`（Egress 模块）。

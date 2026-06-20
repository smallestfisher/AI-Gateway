# 05 · Unified Protocol（统一中间表示 IR）

> 这是全系统的枢纽。所有协议先翻译成 IR，再从 IR 翻译出去。
> 设计准则：**IR 的表达能力 ≥ 任何单一协议**，绝不以 Chat Completions 为中心。

---

## 1. 核心建模思想：内容块（Block）而非消息字符串

三种主流协议的内容表示：

| 协议 | 内容单位 | 特点 |
|------|----------|------|
| Anthropic Messages | `content: []content_block` | 块数组，原生支持 text/image/tool_use/tool_result/thinking 混排 |
| OpenAI Responses | `output: []item` | item 数组，function_call/function_call_output/reasoning/message 混排 |
| OpenAI Chat | `messages[]`，content 多为字符串 | assistant.tool_calls 数组；tool 角色消息；reasoning_content 副字段 |

**结论**：IR 用「消息 = 多个 Block 的序列」建模。它是 Anthropic 块数组的超集，能无损拍平为 Chat，也能一一对应到 Responses item。多个 `tool_use` 块在同一消息里 = 并行工具调用。

---

## 2. 类型定义（Go，简化展示，省略 json tag/校验）

### 2.1 内容块

```go
package ir

type BlockType string

const (
    BlockText       BlockType = "text"        // 普通文本
    BlockImage      BlockType = "image"       // 图片（URL 或 base64）
    BlockToolUse    BlockType = "tool_use"    // assistant 发起：请求调用工具
    BlockToolResult BlockType = "tool_result" // user 回传：工具执行结果
    BlockThinking   BlockType = "thinking"    // Claude thinking / DeepSeek reasoning_content
    BlockReasoning  BlockType = "reasoning"   // OpenAI o-series 推理（携带加密签名）
    BlockAudio      BlockType = "audio"       // 预留（输入/输出音频）
)

// Block 是 IR 的内容原子。同一时刻只有与 Type 对应的字段有意义。
type Block struct {
    Type BlockType `json:"type"`

    // 稳定标识：用于 tool_use↔tool_result 配对、流式 block 索引、去重。
    // Anthropic 用 content_block index；OpenAI tool 用 tool_call_id / call_id；
    // IR 统一用 ID，Adapter 负责与各家 id 互转。
    ID string `json:"id,omitempty"`

    // —— text / thinking / reasoning 共用 ——
    Text string `json:"text,omitempty"`

    // —— image ——
    Image *ImageSource `json:"image,omitempty"`

    // —— tool_use（assistant 请求调用）——
    ToolCall *ToolCall `json:"tool_call,omitempty"`

    // —— tool_result（user 回传结果）——
    ToolResult *ToolResult `json:"tool_result,omitempty"`

    // —— 推理签名（关键，见 §5）——
    // OpenAI: reasoning.encrypted_content / reasoning.id
    // Claude: thinking.signature
    // 工具调用多轮中，上轮推理必须原样回传，否则上游拒绝或丢失上下文。
    Signature string `json:"signature,omitempty"`

    // —— 缓存提示（见 §6）——
    Cache *CacheHint `json:"cache,omitempty"`
}

type ImageSource struct {
    URL      string `json:"url,omitempty"`       // 远程 URL
    Base64   string `json:"base64,omitempty"`    // 内联 base64
    MediaType string `json:"media_type,omitempty"` // image/png | image/jpeg | image/webp | image/gif
}

type ToolCall struct {
    ID    string          `json:"id"`            // 上游分配的工具调用 id
    Name  string          `json:"name"`
    Input json.RawMessage `json:"input"`         // 结构化参数（JSON 对象）
}

type ToolResult struct {
    ToolUseID string  `json:"tool_use_id"`       // 对应的 tool_use 块 ID
    Content   []Block `json:"content"`           // 结果可以是多块（文本+图片）
    IsError   bool    `json:"is_error"`          // 工具执行失败标记
}

type CacheHint struct {
    Strategy string `json:"strategy"` // 目前仅 "ephemeral"
}
```

### 2.2 消息与系统提示

```go
type Role string

const (
    RoleSystem    Role = "system"
    RoleUser      Role = "user"
    RoleAssistant Role = "assistant"
)

type Message struct {
    Role   Role    `json:"role"`
    Blocks []Block `json:"blocks"`
}
```

> **System 一等公民**：IR 把系统提示从 `messages` 中抽出来，单独放在 `UnifiedRequest.System`（`[]Block`）。原因：Anthropic 的 `system` 是顶层独立参数且支持 `cache_control`；Responses 用 `instructions`；Chat 把它塞进首条 `system` 消息。独立字段让缓存与转换都更干净。

### 2.3 统一请求

```go
type UnifiedRequest struct {
    ID             string            `json:"id"`              // 网关 request_id
    ClientProtocol Protocol          `json:"client_protocol"` // 入口协议（由 path 决定）
    Model          string            `json:"model"`           // 别名（Router 解析）

    System         []Block           `json:"system,omitempty"`
    Messages       []Message         `json:"messages"`

    Tools          []Tool            `json:"tools,omitempty"`
    ToolChoice     *ToolChoice       `json:"tool_choice,omitempty"`
    Thinking       *Thinking         `json:"thinking,omitempty"`

    Stream         bool              `json:"stream,omitempty"`
    MaxTokens      int               `json:"max_tokens,omitempty"`
    Temperature    *float64          `json:"temperature,omitempty"`
    TopP           *float64          `json:"top_p,omitempty"`
    Stop           []string          `json:"stop,omitempty"`
    Seed           *int64            `json:"seed,omitempty"`
    ResponseFormat *ResponseFormat   `json:"response_format,omitempty"`

    Metadata       map[string]any    `json:"metadata,omitempty"`

    // 解析后的客户端上下文（AuthN 之后填充，非来自 wire）
    Client         *ClientContext    `json:"client,omitempty"`
}

type Tool struct {
    Name        string          `json:"name"`
    Description string          `json:"description,omitempty"`
    InputSchema json.RawMessage `json:"input_schema"`        // JSON Schema（参数定义）
    Kind        ToolKind        `json:"kind"`                // function | builtin | mcp
    // builtin: web_search / code_interpreter / file_search ...
    Builtin string `json:"builtin,omitempty"`
}

type ToolKind string
const (
    ToolKindFunction ToolKind = "function"
    ToolKindBuiltin  ToolKind = "builtin"
    ToolKindMCP      ToolKind = "mcp"
)

type ToolChoice struct {
    Mode ToolChoiceMode `json:"mode"` // auto | any | none | specific
    Name string         `json:"name,omitempty"` // mode=specific 时指定工具名
}
type ToolChoiceMode string
const (
    ToolChoiceAuto     ToolChoiceMode = "auto"
    ToolChoiceAny      ToolChoiceMode = "any"      // ← OpenAI "required"
    ToolChoiceNone     ToolChoiceMode = "none"
    ToolChoiceSpecific ToolChoiceMode = "specific"
)

type Thinking struct {
    Enabled      bool   `json:"enabled"`
    BudgetTokens int    `json:"budget_tokens,omitempty"` // Claude budget_tokens
    Effort       string `json:"effort,omitempty"`        // OpenAI: low|medium|high
}

type ResponseFormat struct {
    Type       ResponseFormatType `json:"type"` // json_object | json_schema | text
    JSONSchema json.RawMessage    `json:"json_schema,omitempty"`
}
type ResponseFormatType string
const (
    FormatText       ResponseFormatType = "text"
    FormatJSONObject ResponseFormatType = "json_object"
    FormatJSONSchema ResponseFormatType = "json_schema"
)

type ClientContext struct {
    UserID        string         `json:"user_id"`
    APIKeyID      string         `json:"api_key_id"`
    ClientProfile *ClientProfile `json:"client_profile,omitempty"` // 解析后的出口伪装
    Quota         *QuotaContext  `json:"quota,omitempty"`
}
```

### 2.4 统一响应

```go
type UnifiedResponse struct {
    ID            string      `json:"id"`
    Model         string      `json:"model"`           // 客户端请求的别名
    UpstreamModel string      `json:"upstream_model"`  // 实际上游模型名
    ProviderID    string      `json:"provider_id"`

    Blocks        []Block     `json:"blocks"`          // 最终 assistant 内容
    StopReason    StopReason  `json:"stop_reason"`
    Usage         Usage       `json:"usage"`

    Stream        bool        `json:"stream"`
}

type StopReason string
const (
    StopEndTurn       StopReason = "end_turn"
    StopToolUse       StopReason = "tool_use"
    StopMaxTokens     StopReason = "max_tokens"
    StopSequence      StopReason = "stop_sequence"
    StopContentFilter StopReason = "content_filter"
    StopError         StopReason = "error"
)

type Usage struct {
    InputTokens         int `json:"input_tokens"`
    OutputTokens        int `json:"output_tokens"`
    CacheCreationTokens int `json:"cache_creation_tokens"` // prompt cache 写入
    CacheReadTokens     int `json:"cache_read_tokens"`     // prompt cache 命中
    ReasoningTokens     int `json:"reasoning_tokens"`      // o-series 推理消耗
}
```

---

## 3. 字段映射矩阵

### 3.1 IR ↔ OpenAI Chat Completions

| IR | Chat Completions | 说明 |
|----|------------------|------|
| `System []Block` | `messages[0]{role:"system",content:拼接文本}` | System 块拼成一个 system 消息 |
| `Message{role:user,blocks:[text,image]}` | `{role:"user",content:"..."\|[]}` | 多块 → Chat 的多模态 content 数组 |
| `Message{role:assistant,blocks:[text,tool_use×N]}` | `{role:"assistant",content:text,tool_calls:[{id,function:{name,arguments}}]}` | tool_use 块 → tool_calls；并行=N 个 |
| `Message{role:user,blocks:[tool_result×N]}` | N × `{role:"tool",tool_call_id,content}` | 每个 tool_result 块拆成一条 tool 消息 |
| `BlockThinking(Text)` | `message.reasoning_content`（DeepSeek/兼容） | 非标准但常见 |
| `BlockReasoning(Signature)` | OpenAI Chat 不直接支持 | 转换时丢弃或转入 metadata（见 §5） |
| `Tools` | `tools:[{type:"function",function:{name,description,parameters}}]` | InputSchema → parameters |
| `ToolChoice{any}` | `"required"` | any ↔ required |
| `MaxTokens` | `max_tokens` | |
| `StopReason.tool_use` | `finish_reason:"tool_calls"` | |
| `Usage` | `usage{prompt_tokens,completion_tokens,...}` | cache/reasoning 字段按需扩展 |

### 3.2 IR ↔ OpenAI Responses API

| IR | Responses | 说明 |
|----|-----------|------|
| `System []Block` | `instructions`（string）+ 可选 system items | |
| `Message{assistant,blocks}` | `input` 里的 message item `{role,content:[]}` | content 部分用 Responses 的 content part |
| `tool_use` 块 | `function_call` item `{type:"function_call",call_id,name,arguments}` | |
| `tool_result` 块 | `function_call_output` item `{type:"function_call_output",call_id,output}` | output 序列化为字符串 |
| `BlockReasoning(Signature)` | `reasoning` item `{summary[]/encrypted_content,id,status}` | **必须原样回传**（§5） |
| `Tools` | `tools:[{type:"function",name,description,parameters}]`（扁平，非嵌套 function） | Responses 风格 |
| `ToolChoice{any}` | `"required"` | |
| `Thinking.Effort` | `reasoning:{effort}` | |
| 响应 `Blocks` | `output[]` items（message/function_call/reasoning） | |

### 3.3 IR ↔ Anthropic Messages

| IR | Messages | 说明 |
|----|----------|------|
| `System []Block` | 顶层 `system`（string 或 `[{type:text,text,cache_control}]`） | 块直接映射，Cache → cache_control |
| `Message{blocks}` | `messages[]{role,content:[]}` | 块几乎 1:1 |
| `tool_use` 块 | `{type:"tool_use",id,name,input}` | |
| `tool_result` 块 | `{type:"tool_result",tool_use_id,content,is_error}` | |
| `BlockThinking` | `{type:"thinking",thinking,signature}` | 签名一并回传 |
| `Tools` | `tools:[{name,description,input_schema}]` | InputSchema → input_schema（原生 JSON Schema） |
| `ToolChoice{any/specific}` | `{type:"any"/"tool",name}` | |
| `Thinking{BudgetTokens}` | `thinking:{type:"enabled",budget_tokens}` | |
| `StopReason` | `stop_reason`（end_turn/tool_use/max_tokens/stop_sequence） | 几乎 1:1 |
| `Usage` | `usage{input_tokens,output_tokens,cache_creation_input_tokens,cache_read_input_tokens}` | |

> 三张表共同证明：IR 是三家超集，任意两两互转都不丢信息（除「目标协议不支持的特性」被显式降级，见 §5）。

---

## 4. System / Cache / 多模态 细则

### 4.1 System 多块与缓存
- IR 允许 `System` 是多个 Block（用于把「固定系统提示 + 动态上下文」分段打不同缓存断点）。
- 出口到 Anthropic：保留分块 + `cache_control`。
- 出口到 Chat：拼成单条 system 消息（缓存由 provider 隐式处理）。
- 出口到 Responses：取首块文本作 `instructions`，其余可作为 system message item。

### 4.2 图片
- `Image.URL` 与 `Image.Base64` 二选一。出口时按目标协议要求转换：
  - Anthropic：`{type:"image",source:{type:"base64",media_type,data}}` 或 url source。
  - Chat：`{type:"image_url",image_url:{url}}`。
  - Responses：input_image part。

---

## 5. 推理（Thinking / Reasoning）的持久化 ⚠️ 最易踩坑

**问题**：OpenAI o 系列、Claude extended thinking、DeepSeek-R1 的「推理过程」在工具调用多轮中**必须回传上一轮推理（含签名/加密内容）**，否则：
- 上游拒绝继续（OpenAI o-series：function calling 要求回传 reasoning items）；
- 或丢失推理上下文导致质量下降（Claude thinking signature）。

**IR 解法**：

```
上轮 assistant 输出包含 BlockReasoning{Signature} / BlockThinking{Signature}
        │
        │ 客户端把整段历史（含推理块）作为下一轮 messages 回传
        ▼
入口 Adapter 把客户端历史里的 reasoning/thinking 还原成 IR 的 BlockReasoning/BlockThinking（含 Signature）
        │
        ▼
出口 Adapter（egress）「按 provider 能力 + 请求标志」决定：
   - 目标支持且开启推理 → 原样带上 Signature 回传给上游
   - 目标不支持 → 降级：剥离签名，仅保留可见文本（若需要）
```

- `Thinking` 结构同时承载 Claude `budget_tokens` 与 OpenAI `effort`，出口侧翻译。
- 当请求里出现历史 `BlockReasoning` 但目标模型不支持时，egress 必须记录一条「降级」日志（供模拟器/排障查看），不能静默丢弃。

---

## 6. 缓存提示（Cache Hint）

- Block 上的 `Cache` 提示「此断点之后的内容可被 prompt cache」。
- 出口适配：
  - Anthropic → `cache_control:{type:"ephemeral"}` 挂在对应块。
  - OpenAI → 隐式 prompt cache（无需字段，但保留顺序以利命中）。
  - 其它 → 忽略，不报错。
- 统计侧：`Usage.CacheCreationTokens` / `CacheReadTokens` 用于计费与命中率面板。

---

## 7. 不可变约束（实现须遵守）

1. **IR 不可包含网络/配置状态**：Adapter 接收/返回 IR 时不得访问 DB/Redis/HTTP。
2. **ID 稳定**：一次请求内 Block.ID 唯一且不变；工具调用 id 由上游生成后回填，IR 透传。
3. **流式与非流式同源**：流式聚合后的最终 IR 必须与非流式响应结构一致（同样的 `Blocks/StopReason/Usage`），便于统一计费与日志。
4. **无损往返**：`(client wire → IR → client wire)` 必须语义等价（允许字段顺序/冗余空白差异），这是 Adapter 的验收标准之一。

> 下一步：`04-plugin-adapters.md` 定义如何用这套 IR 实现各协议 Adapter；`06-tool-calling.md` 专门讲工具调用全生命周期。

# 06 · Tool Calling 设计

> 工具调用是 Agent 场景的核心。本项目要求**完整支持**多步、并行、流式工具调用，并在 OpenAI Function Calling / Anthropic Tool Use / MCP 之间统一。

---

## 1. 全生命周期

```
 ①注册        ②调用          ③结果          ④多步/并行         ⑤结束
 ┌────┐      ┌─────────┐    ┌──────────┐    ┌──────────────┐   ┌──────┐
 │Tools│ ──▶ │tool_use │ ─▶ │tool_result│ ─▶│ 下一轮请求    │──▶│end / │
 │定义 │      │(assistant)│  │(user)     │    │(带结果历史)   │   │loop  │
 └────┘      └─────────┘    └──────────┘    └──────────────┘   └──────┘
   客户端把工具定义随请求下发      客户端执行工具后回传结果   循环直到 stop_reason ≠ tool_use
```

### 1.1 IR 中的工具表示（见 `05-unified-protocol.md`）

```go
type Tool struct {
    Name        string          // 工具名
    Description string
    InputSchema json.RawMessage // JSON Schema 参数定义
    Kind        ToolKind        // function | builtin | mcp
    Builtin     string          // web_search / code_interpreter ...
}

type ToolChoice struct {
    Mode ToolChoiceMode         // auto | any | none | specific
    Name string
}
```

- **注册**：客户端在请求 `Tools` 字段下发工具定义（Function / Anthropic 风格），网关透传并统一为 IR `Tool`。
- **调用**：上游返回 assistant 内容里的 `tool_use` 块（IR）。
- **结果**：客户端执行后，在下一轮请求里以 `tool_result` 块回传（user 消息内）。
- **多步**：循环「调用→结果→再请求」，直到 `StopReason != tool_use`。
- **并行**：一条 assistant 消息含多个 `tool_use` 块；对应多个 `tool_result` 块回传。

---

## 2. 三协议互转矩阵

### 2.1 工具定义（注册）

| IR | OpenAI Chat | OpenAI Responses | Anthropic Messages |
|----|-------------|------------------|--------------------|
| `Tool{name,description,InputSchema}` | `{type:"function",function:{name,description,parameters:InputSchema}}` | `{type:"function",name,description,parameters:InputSchema}` | `{name,description,input_schema:InputSchema}` |
| `Kind=builtin (web_search)` | `{type:"web_search_preview"}` | `{type:"web_search_preview"}` | Anthropic 服务端工具 `{type:"web_search_20250305",name}` |
| `ToolChoice{auto/any/none/specific}` | `"auto"/"required"/"none"/{type:"function",function:{name}}` | `"auto"/"required"/"none"/{type:"function",name}` | `{type:"auto"/"any"/"none"/"tool",name}` |

> `any ↔ required`：OpenAI 没有 `any`，用 `required` 表达「必须调用某个工具」。

### 2.2 工具调用（assistant 发起）

| IR | OpenAI Chat | OpenAI Responses | Anthropic Messages |
|----|-------------|------------------|--------------------|
| `Block{Type:tool_use, ToolCall:{ID,Name,Input}}` | `assistant.tool_calls[]:{id,type:"function",function:{name,arguments:JSON.stringify(Input)}}` | item `{type:"function_call",call_id:ID,name,arguments:JSON.stringify(Input)}` | content block `{type:"tool_use",id:ID,name,input:Input}` |
| 多个 tool_use 块（并行） | `tool_calls` 数组多项 | 多个 function_call item | 多个 tool_use content block |

**关键差异**：
- OpenAI 的 `arguments` 是**字符串**（JSON 序列化）；Anthropic 的 `input` 是**对象**。IR 用 `json.RawMessage`（对象），出口到 OpenAI 时 `json.Marshal` 成字符串。
- id 字段：Chat 用 `id`，Responses 用 `call_id`，Anthropic 用 `id`。IR 统一 `ToolCall.ID`，Adapter 互转。

### 2.3 工具结果（user 回传）

| IR | OpenAI Chat | OpenAI Responses | Anthropic Messages |
|----|-------------|------------------|--------------------|
| `Block{Type:tool_result, ToolResult:{ToolUseID,Content,IsError}}` | 单独消息 `{role:"tool",tool_call_id:ToolUseID,content:JSON/text}` | item `{type:"function_call_output",call_id:ToolUseID,output:序列化字符串}` | user 消息内的 content block `{type:"tool_result",tool_use_id:ToolUseID,content:[...],is_error}` |

**关键差异**：
- Chat：每个结果**独立成一条** `role:"tool"` 消息（不能合并）。
- Responses：作为 `function_call_output` item（与消息平级）。
- Anthropic：作为 user 消息内的 content block（可多条同消息）。
- IR 统一把它们放在**一条 user 消息的多个 tool_result 块**里；出口到 Chat 时由 Adapter **拆分**成多条 tool 消息。

### 2.4 结果内容的多模态

IR 的 `ToolResult.Content` 是 `[]Block`（可以是文本 + 图片）。出口时：
- Anthropic：直接多 content block。
- Chat/Responses：文本拼接为字符串；图片视目标支持情况降级为文本描述或省略（记录降级日志）。
- `IsError`：Anthropic 有原生 `is_error`；Chat/Responses 用 `content` 前缀 `[ERROR]` 或在 `metadata` 标注（协议无原生字段时）。

---

## 3. 并行工具调用

```
Client 发请求(Tools=[A,B,C])
        │
        ▼
Upstream 返回 assistant:
   Blocks = [ text? , tool_use(A), tool_use(B) ]   ← 并行 2 个
        │  stop_reason = tool_use
        ▼
Client 执行 A、B，下一轮回传:
   user message Blocks = [ tool_result(A), tool_result(B) ]
        │
        ▼
Upstream 继续推理 ……（可能再发起 tool_use，进入下一循环）
```

- IR 天然支持：一条 message 的 `Blocks` 里含多个 `tool_use` / 多个 `tool_result`。
- Adapter 必须保证 **id 配对正确**：`tool_result.ToolUseID` ↔ `tool_use.ToolCall.ID`。
- 部分上游对并行数量有上限：egress 层可配置 `max_parallel_tool_calls`，超出时**不在网关侧拆分**（会破坏语义），而是报错提示运营方调整。

---

## 4. 多步工具调用（Multi-Step）

- 多步由**客户端驱动循环**：网关本身**不持有工具执行器**（除非启用 MCP 网关侧执行，见 `08-mcp.md`）。
- 网关职责：保证每一轮的 IR 历史完整、无损地在协议间转换；推理块（thinking/reasoning）随历史回传（见 `05-unified-protocol.md §5`）。
- 循环终止判据：上游 `StopReason != tool_use`（如 `end_turn`）。

---

## 5. 流式工具调用（Streaming Tool Call）

工具调用的参数在流式中是**增量**到达的。Unified Stream Event 模型（详见 `07-streaming.md`）用 block 生命周期表达：

```
content_block_start  { index:1, block:{type:"tool_use", id, name} }
content_block_delta  { index:1, delta:{type:"input_json", partial_json:"{\"loc\"" } }
content_block_delta  { index:1, delta:{type:"input_json", partial_json:"ation\":\"Pari" } }
content_block_delta  { index:1, delta:{type:"input_json", partial_json:"s\"}" } }
content_block_stop   { index:1 }
```

- `input_json` 增量片段**拼接后**才是完整 `Input` 对象。
- 各协议原生流式：
  - Anthropic：`input_json_delta`。
  - Responses：`response.function_call_arguments.delta`。
  - Chat：`choices[].delta.tool_calls[].function.arguments` 增量（含 `index` 区分并行工具）。
- IR 出入口 Adapter 负责把各家增量片段映射为 `input_json` delta，并在非流式聚合时还原完整对象。

---

## 6. MCP 工具（预留，详见 `08-mcp.md`）

- MCP 工具在 IR 中标记 `Kind=mcp`，结构同 Function（Name/Description/InputSchema）。
- v1：MCP 工具可被**注册并随请求透传**给支持工具的上游（上游原生执行）。
- 未来：网关可作为 MCP 工具宿主，把 MCP 工具暴露给**不支持工具**的上游（网关侧注入工具循环）。接口预留，不在 v1 实现。

---

## 7. 工具调用完整端到端示例

**场景**：Claude Code（Anthropic Messages）经网关访问 OpenAI Chat 兼容上游，含一次并行工具调用。

### 第 1 轮（请求：带工具定义 + 用户问题）
```
入口 /v1/messages:
  system: "你是助手"
  tools: [{name:"get_weather", input_schema:{type:"object",properties:{city:{type:"string"}}}}]
  messages: [{role:"user", content:"巴黎和东京的天气？"}]

ingressAdapter(Messages).DecodeRequest →
  IR.System = [text:"你是助手"]
  IR.Tools  = [{Name:"get_weather", InputSchema:{...}}]
  IR.Messages = [{role:user, blocks:[text:"巴黎和东京的天气？"]}]

egressAdapter(Chat).BuildUpstreamBody →
  { model:"gpt-...", messages:[{role:system,...},{role:user,content:"..."}],
    tools:[{type:"function",function:{name:"get_weather",parameters:{...}}}] }
```

### 第 1 轮（响应：并行工具调用）
```
上游返回:
  choices[0].message.tool_calls = [
    {id:"call_A", function:{name:"get_weather", arguments:"{\"city\":\"Paris\"}"}},
    {id:"call_B", function:{name:"get_weather", arguments:"{\"city\":\"Tokyo\"}"}}]
  finish_reason:"tool_calls"

egressAdapter(Chat).DecodeUpstreamResponse →
  IR.Blocks = [tool_use(id:A), tool_use(id:B)]; StopReason=tool_use

ingressAdapter(Messages).EncodeResponse →
  { content:[{type:"tool_use",id:"A",name:"get_weather",input:{city:"Paris"}},
             {type:"tool_use",id:"B",name:"get_weather",input:{city:"Tokyo"}}],
    stop_reason:"tool_use" }
```

### 第 2 轮（请求：回传工具结果）
```
入口 /v1/messages:
  messages: [...历史..., {role:"user", content:[
      {type:"tool_result", tool_use_id:"A", content:"Paris 18℃"},
      {type:"tool_result", tool_use_id:"B", content:"Tokyo 25℃"} ]}]

ingressAdapter(Messages).DecodeRequest →
  IR.Messages = [..., {role:user, blocks:[tool_result(A), tool_result(B)]}]

egressAdapter(Chat).BuildUpstreamBody →
  messages: [...历史 assistant 带 tool_calls...,
             {role:"tool", tool_call_id:"A", content:"Paris 18℃"},
             {role:"tool", tool_call_id:"B", content:"Tokyo 25℃"}]   ← 拆成两条
```

> 这个例子覆盖了：协议跨族（Messages→Chat）、并行工具、id 配对、结果拆分、stop_reason 映射——是 Adapter 最有代表性的验收用例。

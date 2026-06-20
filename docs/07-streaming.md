# 07 · Stream Event 设计

> 三家协议的流式格式完全不同。IR 采用**表达能力最强的 Anthropic 式「block 生命周期」事件语法**作为统一模型，所有协议的流都映射到它。

---

## 1. Unified Stream Event 模型

### 1.1 事件类型

```go
package ir

type StreamEventType string

const (
    EvStart        StreamEventType = "start"               // 流开始：模型/请求元信息
    EvBlockStart   StreamEventType = "content_block_start" // 一个内容块开始
    EvBlockDelta   StreamEventType = "content_block_delta" // 块内增量
    EvBlockStop    StreamEventType = "content_block_stop"  // 一个内容块结束
    EvMessageDelta StreamEventType = "message_delta"       // 消息级更新（usage/stop_reason）
    EvStop         StreamEventType = "stop"                // 流结束
    EvError        StreamEventType = "error"               // 错误事件
)

type StreamEvent struct {
    Type StreamEventType `json:"type"`

    // start
    ResponseID    string `json:"response_id,omitempty"`
    Model         string `json:"model,omitempty"`

    // block 生命周期
    Index int    `json:"index,omitempty"` // 块序号
    Block *Block `json:"block,omitempty"` // block_start 时的块骨架（type + id + name 等）

    // delta
    Delta *BlockDelta `json:"delta,omitempty"`

    // message_delta / stop
    StopReason StopReason `json:"stop_reason,omitempty"`
    Usage      *Usage     `json:"usage,omitempty"`

    // error
    Error *StreamError `json:"error,omitempty"`
}

// BlockDelta 块内增量
type BlockDelta struct {
    Type    BlockDeltaType `json:"type"`             // text | input_json | thinking | image_url ...
    Text    string         `json:"text,omitempty"`    // text/thinking 文本片段
    PartialJSON string     `json:"partial_json,omitempty"` // 工具入参的 JSON 片段
}

type BlockDeltaType string
const (
    DeltaText      BlockDeltaType = "text"
    DeltaInputJSON BlockDeltaType = "input_json" // 工具调用入参增量
    DeltaThinking  BlockDeltaType = "thinking"
)

type StreamError struct {
    Code    string `json:"code"`
    Message string `json:"message"`
    // 是否可重试（仅未首字节前的错误才可能被 Egress failover）
    Retryable bool `json:"retryable"`
}
```

### 1.2 事件语法（状态机）

```
start
  └─▶ ( content_block_start ─▶ content_block_delta* ─▶ content_block_stop )*   ← 0..N 个块
                                                                                   每块独立生命周期
        └─▶ message_delta (携带最终 usage / stop_reason)
              └─▶ stop
任意位置可被 error 打断（error 后流终止）
```

**为什么选这套语法**：
- 它是**块粒度**的，能无损表达「文本块 / 思考块 / 工具块」交替出现（Chat 的扁平 delta 做不到干净表达 thinking 与 tool 并存）。
- Anthropic 原生即此模型 → Messages Adapter 几乎 1:1。
- Chat / Responses 通过**状态机**向它对齐（见 §3）。

---

## 2. 三协议事件映射表

### 2.1 IR ↔ Anthropic Messages（最接近，近乎 1:1）

| IR Event | Anthropic Event |
|----------|-----------------|
| `start{Model}` | `message_start{message:{model,...}}` |
| `content_block_start{Index,Block}` | `content_block_start{index, content_block:{type,...}}` |
| `content_block_delta{Delta:text}` | `content_block_delta{index, delta:{type:"text_delta",text}}` |
| `content_block_delta{Delta:input_json}` | `content_block_delta{index, delta:{type:"input_json_delta",partial_json}}` |
| `content_block_delta{Delta:thinking}` | `content_block_delta{index, delta:{type:"thinking_delta",thinking}}` |
| `content_block_stop{Index}` | `content_block_stop{index}` |
| `message_delta{Usage,StopReason}` | `message_delta{delta:{stop_reason}, usage:{...}}` |
| `stop` | `message_stop` |
| `error` | `error{type,message}` |

### 2.2 IR ↔ OpenAI Chat（需状态机聚合/拆分）

**上游 Chat → IR（Decoder 状态机）**：
- 累积 `choices[0].delta`：见 `content` 文本 → 在当前文本块上发 `text` delta；
- 见 `delta.tool_calls[i]`：按 `index` 维护工具块，`function.name` 到达 → `content_block_start(tool_use)`；`function.arguments` 片段 → `input_json` delta；
- 见 `delta.reasoning_content` → 维护 thinking 块，发 `thinking` delta；
- `finish_reason` 到达 → `message_delta{StopReason}` + `stop`。
- `usage`（需 `stream_options:{include_usage:true}`）→ 合并进 `message_delta`。

**IR → 客户端 Chat（Encoder）**：
- `content_block_start(text)` 通常不产生显式帧（Chat 文本就是 delta 流），首条 `text` delta 直接输出 `choices[].delta.content`。
- `tool_use` 块：`content_block_start` → `delta.tool_calls[index].function.name` + `type:"function"`；`input_json` delta → `delta.tool_calls[index].function.arguments`。
- `message_delta{StopReason.tool_use}` → `finish_reason:"tool_calls"`。
- `stop` → 末尾发 `data: [DONE]`。

| StopReason | Chat finish_reason |
|------------|--------------------|
| `end_turn` | `stop` |
| `tool_use` | `tool_calls` |
| `max_tokens` | `length` |
| `stop_sequence` | `stop` |
| `content_filter` | `content_filter` |

### 2.3 IR ↔ OpenAI Responses（item 事件）

**上游 Responses → IR**：
- `response.created` → `start`。
- `response.output_item.added` (message item) → 准备文本块。
- `response.output_text.delta` → `text` delta。
- `response.output_item.added` (function_call) → `content_block_start(tool_use)`。
- `response.function_call_arguments.delta` → `input_json` delta。
- `response.reasoning.*.delta` → `thinking`/`reasoning` delta。
- `response.completed`（含 `usage`）→ `message_delta` + `stop`。

**IR → 客户端 Responses**：对应反向映射，`stop` → `response.completed`。

---

## 3. Decoder 状态机要点（实现关键）

```
┌─────────── 上游 SSE 字节流（帧边界不可信：会粘包/拆包）───────────┐
│                                                                  │
│  LineBuffer 累积 → 按 "\n\n" 切 SSE event → 解析 data: JSON       │
│                                                                  │
│  状态机变量:                                                      │
│    currentBlockIndex int        当前块序号                        │
│    blockTypePerIndex map[int]BlockType   各序号的块类型            │
│    pendingToolName   map[int]string      工具名（待入参到达）      │
│    accUsage          Usage               累积用量                 │
└──────────────────────────────────────────────────────────────────┘
```

**铁律**：
1. **不假设 chunk == 事件**。一个 `Feed(chunk)` 可能产生 0、1 或多个 IR 事件；一条 JSON 也可能跨多个 chunk。
2. **块类型切换 = 新块**。Chat 流里 `delta.content` 与 `delta.tool_calls` 交替出现时，需先 `content_block_stop` 旧块再 `content_block_start` 新块（IR 显式表达边界，Encoder 再按目标协议决定是否发出边界帧）。
3. **Usage 增量合并**。部分上游分多次给 usage（如 Anthropic 在 `message_start` 给 input、`message_delta` 给 output）；Decoder 累积，最终在 `message_delta` 一次性给出完整 `Usage`。

---

## 4. 失败与 failover 的流式约束

```
首字节前失败        → 可 failover 到下一 Channel（客户端无感知）
首字节后失败        → 不可 failover，发 EvError 终止（客户端已收到部分输出）
上游中途断流        → 发 EvError{Retryable:false}，记录 incomplete 日志
```

- Egress 在「向客户端写第一个字节」时设置 `committed=true`；此后任何错误都不再重试。
- `EvError` 按入口协议编码：Chat → 一条 `data: {error}` 后 `[DONE]`；Messages → `event: error data:{...}`；Responses → `response.failed`。

---

## 5. 端到端流式走查

**场景**：OpenAI Chat 上游（带工具 + thinking 的兼容服务）→ 客户端 Claude Code（Messages）。

```
上游 Chat SSE:
  data: {"choices":[{"delta":{"content":"Hello"}}]}
  data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"greet","arguments":"{\"n"}}]}}]}
  data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ame\":\"A\"}"}}]}}]}
  data: {"choices":[{"finish_reason":"tool_calls"}]}
  data: [DONE]

egressAdapter(Chat).Decoder 产出 IR 事件:
  content_block_start {index:0, block:{type:text}}
  content_block_delta {index:0, delta:{type:text, text:"Hello"}}
  content_block_stop  {index:0}
  content_block_start {index:1, block:{type:tool_use, id:"call_x", name:"greet"}}
  content_block_delta {index:1, delta:{type:input_json, partial_json:"{\"n"}}
  content_block_delta {index:1, delta:{type:input_json, partial_json:"ame\":\"A\"}"}}
  content_block_stop  {index:1}
  message_delta {stop_reason:tool_use}
  stop

ingressAdapter(Messages).Encoder 产出客户端 SSE:
  event: message_start          data: {...}
  event: content_block_start    data: {"index":0,"content_block":{"type":"text"}}
  event: content_block_delta    data: {"index":0,"delta":{"type":"text_delta","text":"Hello"}}
  event: content_block_stop     data: {"index":0}
  event: content_block_start    data: {"index":1,"content_block":{"type":"tool_use","id":"call_x","name":"greet"}}
  event: content_block_delta    data: {"index":1,"delta":{"type":"input_json_delta","partial_json":"{\"n"}}
  event: content_block_delta    data: {"index":1,"delta":{"type":"input_json_delta","partial_json":"ame\":\"A\"}"}}
  event: content_block_stop     data: {"index":1}
  event: message_delta          data: {"delta":{"stop_reason":"tool_use"}}
  event: message_stop           data: {}
```

> 这条走查是流式模块的代表性验收用例：覆盖文本块、工具块、增量入参拼接、stop_reason 映射、双协议事件语法。

---

## 6. 流式指标采样

流式请求在 Egress 侧额外采样：
- **TTFT**：从发出上游请求到收到首个 `content_block_delta`（或 `start`）的时间。
- **首块延迟 / 末块延迟 / 总时长**。
- **块数 / token 增量**：随 `message_delta` 的最终 usage 落盘。

这些写入 Redis 滑动窗口，供健康监控与 auto 路由使用（见 `02-modules.md`）。

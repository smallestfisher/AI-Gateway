// Package ir defines the Unified Intermediate Representation (IR) for the Agent Gateway.
//
// The IR is the hub of all protocol conversion. Every client protocol
// (OpenAI Chat / Responses, Anthropic Messages, Gemini, ...) is decoded into
// this representation on ingress, and every provider protocol is built from it
// on egress. The IR is intentionally more expressive than any single protocol
// so that conversion between any two is lossless (see docs/05-unified-protocol.md).
//
// Design rule: IR types carry NO network, DB, or config state. Adapters that
// decode/encode IR must be pure functions of their inputs.
package ir

import (
	"encoding/json"
)

// Role of a message participant.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// BlockType enumerates the kinds of content blocks.
type BlockType string

const (
	BlockText       BlockType = "text"        // plain text (or tool result text)
	BlockImage      BlockType = "image"       // image (URL or base64)
	BlockToolUse    BlockType = "tool_use"    // assistant requests a tool call
	BlockToolResult BlockType = "tool_result" // user returns a tool result
	BlockThinking   BlockType = "thinking"    // Claude thinking / DeepSeek reasoning_content
	BlockReasoning  BlockType = "reasoning"   // OpenAI o-series reasoning (carries signature)
	BlockAudio      BlockType = "audio"       // reserved
)

// Block is the atomic unit of content. Only the field matching Type is
// meaningful at any given time.
type Block struct {
	Type BlockType `json:"type"`

	// ID is stable within a request. Used for tool_use<->tool_result pairing
	// and stream block indexing. Adapters translate to/from per-protocol ids
	// (Anthropic block index, OpenAI tool_call_id / call_id).
	ID string `json:"id,omitempty"`

	// Text holds text/thinking/reasoning prose.
	Text string `json:"text,omitempty"`

	// Image for BlockImage.
	Image *ImageSource `json:"image,omitempty"`

	// ToolCall for BlockToolUse (assistant initiates a call).
	ToolCall *ToolCall `json:"tool_call,omitempty"`

	// ToolResult for BlockToolResult (user returns a result).
	ToolResult *ToolResult `json:"tool_result,omitempty"`

	// Signature carries reasoning signatures that MUST be echoed back across
	// tool-call turns (OpenAI reasoning.encrypted_content, Claude thinking
	// signature). See docs/05-unified-protocol.md §5.
	Signature string `json:"signature,omitempty"`

	// Cache hints prompt caching at this breakpoint. Egress translates to
	// per-provider mechanism (Anthropic cache_control, OpenAI implicit).
	Cache *CacheHint `json:"cache,omitempty"`
}

// ImageSource describes an image by URL or inline base64.
type ImageSource struct {
	URL       string `json:"url,omitempty"`
	Base64    string `json:"base64,omitempty"`
	MediaType string `json:"media_type,omitempty"` // image/png | image/jpeg | image/webp | image/gif
}

// ToolCall is an assistant-initiated tool invocation.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"` // structured args (JSON object)
}

// ToolResult is a user-returned tool result, addressed to a prior ToolCall.ID.
type ToolResult struct {
	ToolUseID string  `json:"tool_use_id"`
	Content   []Block `json:"content"` // a result may itself be multi-block (text+image)
	IsError   bool    `json:"is_error"`
}

// CacheHint marks a cache breakpoint.
type CacheHint struct {
	Strategy string `json:"strategy"` // currently only "ephemeral"
}

// Message is a role + an ordered list of content blocks.
// Multiple tool_use blocks in one assistant message == parallel tool calls.
type Message struct {
	Role   Role    `json:"role"`
	Blocks []Block `json:"blocks"`
}

// Tool describes a tool the model may call.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"` // JSON Schema (parameters)
	Kind        ToolKind        `json:"kind"`
	Builtin     string          `json:"builtin,omitempty"` // for Kind=Builtin (web_search, ...)
}

// ToolKind classifies a tool's origin.
type ToolKind string

const (
	ToolKindFunction ToolKind = "function"
	ToolKindBuiltin  ToolKind = "builtin"
	ToolKindMCP      ToolKind = "mcp"
)

// ToolChoice controls whether/how the model should call tools.
type ToolChoice struct {
	Mode ToolChoiceMode `json:"mode"`
	Name string         `json:"name,omitempty"` // when Mode == ToolChoiceSpecific
}

// ToolChoiceMode enumerates tool-choice strategies.
type ToolChoiceMode string

const (
	ToolChoiceAuto     ToolChoiceMode = "auto"
	ToolChoiceAny      ToolChoiceMode = "any" // maps to OpenAI "required"
	ToolChoiceNone     ToolChoiceMode = "none"
	ToolChoiceSpecific ToolChoiceMode = "specific"
)

// Thinking controls reasoning/thinking behavior across providers.
type Thinking struct {
	Enabled      bool   `json:"enabled"`
	BudgetTokens int    `json:"budget_tokens,omitempty"` // Claude budget_tokens
	Effort       string `json:"effort,omitempty"`        // OpenAI: low|medium|high
}

// ResponseFormat controls structured output.
type ResponseFormat struct {
	Type       ResponseFormatType `json:"type"`
	JSONSchema json.RawMessage    `json:"json_schema,omitempty"`
}

// ResponseFormatType enumerates response format modes.
type ResponseFormatType string

const (
	FormatText       ResponseFormatType = "text"
	FormatJSONObject ResponseFormatType = "json_object"
	FormatJSONSchema ResponseFormatType = "json_schema"
)

// ClientContext is the resolved caller context (filled after AuthN, not from wire).
type ClientContext struct {
	UserID   string `json:"user_id"`
	APIKeyID string `json:"api_key_id"`
}

// UnifiedRequest is the canonical request IR. Model is an alias resolved by the Router.
type UnifiedRequest struct {
	ID             string `json:"id"`
	ClientProtocol string `json:"client_protocol"`
	Model          string `json:"model"` // alias

	System   []Block   `json:"system,omitempty"`
	Messages []Message `json:"messages"`

	Tools          []Tool          `json:"tools,omitempty"`
	ToolChoice     *ToolChoice     `json:"tool_choice,omitempty"`
	Thinking       *Thinking       `json:"thinking,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`

	Stream      bool     `json:"stream,omitempty"`
	MaxTokens   int      `json:"max_tokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	Stop        []string `json:"stop,omitempty"`
	Seed        *int64   `json:"seed,omitempty"`

	Metadata map[string]any `json:"metadata,omitempty"`

	Client *ClientContext `json:"client,omitempty"`
}

// UnifiedResponse is the canonical response IR.
type UnifiedResponse struct {
	ID            string `json:"id"`
	Model         string `json:"model"`          // alias requested
	UpstreamModel string `json:"upstream_model"` // actual upstream model
	ProviderID    string `json:"provider_id"`

	Blocks     []Block    `json:"blocks"`
	StopReason StopReason `json:"stop_reason"`
	Usage      Usage      `json:"usage"`

	Stream bool `json:"stream"`
}

// StopReason is the normalized reason the model stopped generating.
type StopReason string

const (
	StopEndTurn       StopReason = "end_turn"
	StopToolUse       StopReason = "tool_use"
	StopMaxTokens     StopReason = "max_tokens"
	StopSequence      StopReason = "stop_sequence"
	StopContentFilter StopReason = "content_filter"
	StopError         StopReason = "error"
)

// Usage is the normalized token accounting.
type Usage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens"`
	CacheReadTokens     int `json:"cache_read_tokens"`
	ReasoningTokens     int `json:"reasoning_tokens"`
}

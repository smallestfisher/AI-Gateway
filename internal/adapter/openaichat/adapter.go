// Package openaichat implements the Adapter for the OpenAI Chat Completions
// protocol (POST /v1/chat/completions). Conversion is lossless w.r.t. the IR
// for everything Chat can express; reasoning signatures and per-block cache
// hints degrade gracefully (see docs/05-unified-protocol.md, docs/06-tool-calling.md).
package openaichat

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/aigateway/ai-hub/internal/adapter"
	"github.com/aigateway/ai-hub/internal/ir"
)

// New returns the Chat Completions adapter.
func New() *Adapter { return &Adapter{} }

func (a *Adapter) Protocol() adapter.Protocol { return adapter.ProtocolChat }

// ---------------------------------------------------------------------------
// Wire types (Chat Completions)
// ---------------------------------------------------------------------------

type request struct {
	Model          string          `json:"model"`
	Messages       []wireMessage   `json:"messages"`
	Tools          []wireTool      `json:"tools,omitempty"`
	ToolChoice     json.RawMessage `json:"tool_choice,omitempty"` // string | object
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Temperature    *float64        `json:"temperature,omitempty"`
	TopP           *float64        `json:"top_p,omitempty"`
	Stream         bool            `json:"stream,omitempty"`
	Stop           any             `json:"stop,omitempty"` // string | []string
	Seed           *int64          `json:"seed,omitempty"`
	ResponseFormat *wireRespFormat `json:"response_format,omitempty"`
	StreamOptions  *wireStreamOpts `json:"stream_options,omitempty"`
}

type wireStreamOpts struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type wireRespFormat struct {
	Type       string          `json:"type"`
	JSONSchema json.RawMessage `json:"json_schema,omitempty"`
}

// wireMessage.Content is either a JSON string or an array of content parts.
type wireMessage struct {
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content,omitempty"`
	ToolCalls        []wireToolCall  `json:"tool_calls,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	Name             string          `json:"name,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"` // DeepSeek-style
}

type wireContentPart struct {
	Type     string        `json:"type"` // "text" | "image_url"
	Text     string        `json:"text,omitempty"`
	ImageURL *wireImageURL `json:"image_url,omitempty"`
}

type wireImageURL struct {
	URL string `json:"url"`
}

type wireToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // "function"
	Index    int              `json:"index,omitempty"` // set in streaming deltas for parallel tools
	Function wireToolFunction `json:"function"`
}

type wireToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

type wireTool struct {
	Type     string      `json:"type"` // "function"
	Function wireToolDef `json:"function"`
}

type wireToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"` // JSON Schema
}

type completion struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []wireChoice `json:"choices"`
	Usage   *wireUsage   `json:"usage,omitempty"`
}

type wireChoice struct {
	Index        int         `json:"index"`
	Message      wireMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type wireUsage struct {
	PromptTokens            int               `json:"prompt_tokens"`
	CompletionTokens        int               `json:"completion_tokens"`
	TotalTokens             int               `json:"total_tokens"`
	PromptTokensDetails     *wireTokenDetails `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *wireTokenDetails `json:"completion_tokens_details,omitempty"`
}

type wireTokenDetails struct {
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	CachedTokens    int `json:"cached_tokens,omitempty"`
}

// ---------------------------------------------------------------------------
// Adapter
// ---------------------------------------------------------------------------

type Adapter struct{}

// DecodeRequest converts a Chat Completions request body into the IR.
func (a *Adapter) DecodeRequest(raw []byte, _ http.Header) (*ir.UnifiedRequest, error) {
	var w request
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, fmt.Errorf("openaichat: decode request: %w", err)
	}

	req := &ir.UnifiedRequest{
		ClientProtocol: string(adapter.ProtocolChat),
		Model:          w.Model,
		Stream:         w.Stream,
		MaxTokens:      w.MaxTokens,
		Temperature:    w.Temperature,
		TopP:           w.TopP,
		Seed:           w.Seed,
	}

	// messages: pull system out; group consecutive tool messages into user turns
	var pendingToolResults []ir.Block
	flushTools := func() {
		if len(pendingToolResults) > 0 {
			req.Messages = append(req.Messages, ir.Message{Role: ir.RoleUser, Blocks: pendingToolResults})
			pendingToolResults = nil
		}
	}
	for i := range w.Messages {
		wm := &w.Messages[i]
		switch wm.Role {
		case "system":
			flushTools()
			if txt := rawString(wm.Content); txt != "" || !isNullRaw(wm.Content) {
				req.System = append(req.System, ir.Block{Type: ir.BlockText, Text: txt})
			}
		case "tool":
			pendingToolResults = append(pendingToolResults, ir.Block{
				Type: ir.BlockToolResult,
				ToolResult: &ir.ToolResult{
					ToolUseID: wm.ToolCallID,
					Content:   []ir.Block{{Type: ir.BlockText, Text: rawString(wm.Content)}},
				},
			})
		case "user", "assistant":
			flushTools()
			msg := ir.Message{Role: ir.Role(wm.Role)}
			// content (string or parts)
			for _, b := range decodeContent(wm.Content) {
				msg.Blocks = append(msg.Blocks, b)
			}
			// assistant tool calls
			for _, tc := range wm.ToolCalls {
				input := json.RawMessage(tc.Function.Arguments)
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				msg.Blocks = append(msg.Blocks, ir.Block{
					Type: ir.BlockToolUse,
					ToolCall: &ir.ToolCall{
						ID:    tc.ID,
						Name:  tc.Function.Name,
						Input: input,
					},
				})
			}
			// thinking (DeepSeek reasoning_content) -> assistant thinking block
			if wm.ReasoningContent != "" {
				msg.Blocks = append(msg.Blocks, ir.Block{Type: ir.BlockThinking, Text: wm.ReasoningContent})
			}
			// keep messages that have no blocks (e.g. assistant with only tool_calls already added)
			if len(msg.Blocks) == 0 && wm.Role == "assistant" && len(wm.ToolCalls) == 0 {
				// preserve an explicit empty assistant turn
				msg.Blocks = []ir.Block{{Type: ir.BlockText, Text: ""}}
			}
			req.Messages = append(req.Messages, msg)
		default:
			flushTools()
			// unknown role: treat generically as its declared role with text content
			req.Messages = append(req.Messages, ir.Message{
				Role:   ir.Role(wm.Role),
				Blocks: decodeContent(wm.Content),
			})
		}
	}
	flushTools()

	// tools
	for _, t := range w.Tools {
		schema := t.Function.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		req.Tools = append(req.Tools, ir.Tool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: schema,
			Kind:        ir.ToolKindFunction,
		})
	}

	req.ToolChoice = decodeToolChoice(w.ToolChoice)
	req.ResponseFormat = decodeRespFormat(w.ResponseFormat)
	req.Stop = decodeStop(w.Stop)

	return req, nil
}

// BuildUpstream builds a Chat Completions request body for an upstream that
// speaks this protocol. The IR is the same shape DecodeRequest produces.
func (a *Adapter) BuildUpstream(req *ir.UnifiedRequest, upstreamModel string) (*adapter.UpstreamRequest, error) {
	out := request{Model: upstreamModel}

	out.Messages = buildMessages(req)

	for _, t := range req.Tools {
		out.Tools = append(out.Tools, wireTool{
			Type: "function",
			Function: wireToolDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  orDefaultSchema(t.InputSchema),
			},
		})
	}
	if req.ToolChoice != nil {
		out.ToolChoice = encodeToolChoice(req.ToolChoice)
	}
	out.MaxTokens = req.MaxTokens
	out.Temperature = req.Temperature
	out.TopP = req.TopP
	out.Stream = req.Stream
	if len(req.Stop) > 0 {
		out.Stop = req.Stop
	}
	out.Seed = req.Seed
	out.ResponseFormat = encodeRespFormat(req.ResponseFormat)
	if req.Stream {
		out.StreamOptions = &wireStreamOpts{IncludeUsage: true}
	}

	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("openaichat: build upstream: %w", err)
	}
	return &adapter.UpstreamRequest{Path: "/v1/chat/completions", Body: body}, nil
}

// DecodeUpstreamResponse parses an upstream Chat Completion response into IR.
func (a *Adapter) DecodeUpstreamResponse(raw []byte) (*ir.UnifiedResponse, error) {
	var c completion
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("openaichat: decode upstream response: %w", err)
	}
	resp := &ir.UnifiedResponse{
		ID:            c.ID,
		Model:         c.Model,
		UpstreamModel: c.Model,
		StopReason:    chatFinishToStop(c.finishReason()),
	}
	if len(c.Choices) > 0 {
		msg := c.Choices[0].Message
		for _, b := range decodeContent(msg.Content) {
			resp.Blocks = append(resp.Blocks, b)
		}
		for _, tc := range msg.ToolCalls {
			input := json.RawMessage(tc.Function.Arguments)
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			resp.Blocks = append(resp.Blocks, ir.Block{
				Type: ir.BlockToolUse,
				ToolCall: &ir.ToolCall{
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: input,
				},
			})
		}
		if msg.ReasoningContent != "" {
			resp.Blocks = append(resp.Blocks, ir.Block{Type: ir.BlockThinking, Text: msg.ReasoningContent})
		}
	}
	if c.Usage != nil {
		resp.Usage = chatUsageToIR(c.Usage)
	}
	return resp, nil
}

// EncodeResponse encodes an IR response into a Chat Completion JSON body.
func (a *Adapter) EncodeResponse(resp *ir.UnifiedResponse) ([]byte, error) {
	out := completion{
		ID:      orDefault(resp.ID, "chatcmpl-unknown"),
		Object:  "chat.completion",
		Created: nowUnix(),
		Model:   orDefault(resp.Model, resp.UpstreamModel),
		Choices: []wireChoice{{
			Index:        0,
			Message:      buildAssistantMessage(resp.Blocks),
			FinishReason: stopToChatFinish(resp.StopReason),
		}},
	}
	out.Usage = irUsageToChat(resp.Usage)
	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("openaichat: encode response: %w", err)
	}
	return body, nil
}

func (a *Adapter) NewStreamEncoder() adapter.StreamEncoder { return &streamEnc{} }
func (a *Adapter) NewStreamDecoder() adapter.StreamDecoder { return &streamDec{} }

// ---------------------------------------------------------------------------
// helpers: content
// ---------------------------------------------------------------------------

// decodeContent parses a Chat content field (string or array of parts) into blocks.
func decodeContent(raw json.RawMessage) []ir.Block {
	if len(raw) == 0 || isNullRaw(raw) {
		return nil
	}
	// string?
	if s, ok := parseStringRaw(raw); ok {
		if s == "" {
			return nil
		}
		return []ir.Block{{Type: ir.BlockText, Text: s}}
	}
	// array of parts
	var parts []wireContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil
	}
	var blocks []ir.Block
	for _, p := range parts {
		switch p.Type {
		case "text":
			blocks = append(blocks, ir.Block{Type: ir.BlockText, Text: p.Text})
		case "image_url":
			blocks = append(blocks, ir.Block{Type: ir.BlockImage, Image: &ir.ImageSource{URL: p.ImageURL.URL}})
		}
	}
	return blocks
}

// rawString returns the string value of a content field if it is a JSON string.
func rawString(raw json.RawMessage) string {
	if s, ok := parseStringRaw(raw); ok {
		return s
	}
	return ""
}

func parseStringRaw(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, true
	}
	return "", false
}

func isNullRaw(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}

// blocksToText joins text blocks into a single string.
func blocksToText(blocks []ir.Block) string {
	var sb strings.Builder
	for _, b := range blocks {
		if b.Type == ir.BlockText {
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// helpers: build chat messages from IR
// ---------------------------------------------------------------------------

func buildMessages(req *ir.UnifiedRequest) []wireMessage {
	var out []wireMessage
	if len(req.System) > 0 {
		out = append(out, wireMessage{Role: "system", Content: jsonString(blocksToText(req.System))})
	}
	for _, m := range req.Messages {
		out = append(out, messageToChat(m)...)
	}
	return out
}

// messageToChat converts one IR message into one or more Chat messages.
func messageToChat(m ir.Message) []wireMessage {
	role := string(m.Role)
	var toolMessages []wireMessage
	var contentBlocks []ir.Block
	var toolCalls []wireToolCall
	var reasoning string

	for _, b := range m.Blocks {
		switch b.Type {
		case ir.BlockToolResult:
			toolMessages = append(toolMessages, wireMessage{
				Role:       "tool",
				ToolCallID: b.ToolResult.ToolUseID,
				Content:    jsonString(blocksToText(b.ToolResult.Content)),
			})
		case ir.BlockToolUse:
			args := "{}"
			if len(b.ToolCall.Input) > 0 {
				args = string(b.ToolCall.Input)
			}
			toolCalls = append(toolCalls, wireToolCall{
				ID:       b.ToolCall.ID,
				Type:     "function",
				Function: wireToolFunction{Name: b.ToolCall.Name, Arguments: args},
			})
		case ir.BlockThinking:
			reasoning += b.Text
		case ir.BlockReasoning:
			// Chat has no native o-series reasoning; degrade to reasoning_content text.
			reasoning += b.Text
		default:
			contentBlocks = append(contentBlocks, b)
		}
	}

	// Build the primary message if it has content, tool_calls, or reasoning.
	// (A user turn that was only tool_results yields only tool messages.)
	hasPrimary := len(contentBlocks) > 0 || len(toolCalls) > 0 || reasoning != ""
	// assistant turns should always be emitted even if empty-ish, to preserve turn order
	if !hasPrimary && (role == "assistant" || role == "user") && len(toolMessages) == 0 {
		hasPrimary = true
	}

	var out []wireMessage
	if hasPrimary {
		wm := wireMessage{Role: role}
		if len(contentBlocks) > 0 {
			wm.Content = blocksToContent(contentBlocks)
		} else if len(toolCalls) > 0 || reasoning != "" {
			// assistant with tool calls / reasoning but no visible text
			wm.Content = jsonString("")
		}
		if len(toolCalls) > 0 {
			wm.ToolCalls = toolCalls
		}
		if reasoning != "" {
			wm.ReasoningContent = reasoning
		}
		out = append(out, wm)
	}
	out = append(out, toolMessages...)
	return out
}

// buildAssistantMessage builds the assistant message for an EncodeResponse.
func buildAssistantMessage(blocks []ir.Block) wireMessage {
	return firstOrEmpty(messageToChat(ir.Message{Role: ir.RoleAssistant, Blocks: blocks}))
}

func firstOrEmpty(msgs []wireMessage) wireMessage {
	if len(msgs) == 0 {
		return wireMessage{Role: "assistant", Content: jsonString("")}
	}
	return msgs[0]
}

// blocksToContent marshals blocks into a Chat content field: a plain JSON
// string when there's a single text block, otherwise an array of parts.
func blocksToContent(blocks []ir.Block) json.RawMessage {
	if len(blocks) == 1 && blocks[0].Type == ir.BlockText {
		return jsonString(blocks[0].Text)
	}
	parts := make([]wireContentPart, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case ir.BlockText:
			parts = append(parts, wireContentPart{Type: "text", Text: b.Text})
		case ir.BlockImage:
			url := ""
			if b.Image != nil {
				if b.Image.URL != "" {
					url = b.Image.URL
				} else if b.Image.Base64 != "" {
					url = "data:" + b.Image.MediaType + ";base64," + b.Image.Base64
				}
			}
			parts = append(parts, wireContentPart{Type: "image_url", ImageURL: &wireImageURL{URL: url}})
		}
	}
	b, _ := json.Marshal(parts)
	return b
}

func jsonString(s string) json.RawMessage { b, _ := json.Marshal(s); return b }

// ---------------------------------------------------------------------------
// helpers: tool choice / response format / stop / usage
// ---------------------------------------------------------------------------

func decodeToolChoice(raw json.RawMessage) *ir.ToolChoice {
	if len(raw) == 0 || isNullRaw(raw) {
		return nil
	}
	if s, ok := parseStringRaw(raw); ok {
		switch s {
		case "auto":
			return &ir.ToolChoice{Mode: ir.ToolChoiceAuto}
		case "required":
			return &ir.ToolChoice{Mode: ir.ToolChoiceAny}
		case "none":
			return &ir.ToolChoice{Mode: ir.ToolChoiceNone}
		}
		return nil
	}
	// object form {"type":"function","function":{"name":"x"}}
	var obj struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Type == "function" {
		return &ir.ToolChoice{Mode: ir.ToolChoiceSpecific, Name: obj.Function.Name}
	}
	return nil
}

func encodeToolChoice(tc *ir.ToolChoice) json.RawMessage {
	switch tc.Mode {
	case ir.ToolChoiceAuto:
		return jsonString("auto")
	case ir.ToolChoiceAny:
		return jsonString("required")
	case ir.ToolChoiceNone:
		return jsonString("none")
	case ir.ToolChoiceSpecific:
		b, _ := json.Marshal(map[string]any{
			"type":     "function",
			"function": map[string]any{"name": tc.Name},
		})
		return b
	}
	return jsonString("auto")
}

func decodeRespFormat(w *wireRespFormat) *ir.ResponseFormat {
	if w == nil {
		return nil
	}
	switch w.Type {
	case "json_object":
		return &ir.ResponseFormat{Type: ir.FormatJSONObject}
	case "json_schema":
		return &ir.ResponseFormat{Type: ir.FormatJSONSchema, JSONSchema: w.JSONSchema}
	}
	return nil
}

func encodeRespFormat(rf *ir.ResponseFormat) *wireRespFormat {
	if rf == nil {
		return nil
	}
	switch rf.Type {
	case ir.FormatJSONObject:
		return &wireRespFormat{Type: "json_object"}
	case ir.FormatJSONSchema:
		return &wireRespFormat{Type: "json_schema", JSONSchema: rf.JSONSchema}
	}
	return nil
}

func decodeStop(v any) []string {
	if v == nil {
		return nil
	}
	switch s := v.(type) {
	case string:
		return []string{s}
	case []any:
		out := make([]string, 0, len(s))
		for _, x := range s {
			if str, ok := x.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}

func orDefaultSchema(schema json.RawMessage) json.RawMessage {
	if len(schema) == 0 {
		return json.RawMessage(`{"type":"object"}`)
	}
	return schema
}

func chatUsageToIR(w *wireUsage) ir.Usage {
	u := ir.Usage{
		InputTokens:  w.PromptTokens,
		OutputTokens: w.CompletionTokens,
	}
	if w.PromptTokensDetails != nil {
		u.CacheReadTokens = w.PromptTokensDetails.CachedTokens
	}
	if w.CompletionTokensDetails != nil {
		u.ReasoningTokens = w.CompletionTokensDetails.ReasoningTokens
	}
	return u
}

func irUsageToChat(u ir.Usage) *wireUsage {
	total := u.InputTokens + u.OutputTokens
	out := &wireUsage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      total,
	}
	if u.CacheReadTokens > 0 {
		out.PromptTokensDetails = &wireTokenDetails{CachedTokens: u.CacheReadTokens}
	}
	if u.ReasoningTokens > 0 {
		out.CompletionTokensDetails = &wireTokenDetails{ReasoningTokens: u.ReasoningTokens}
	}
	return out
}

func chatFinishToStop(fr string) ir.StopReason {
	switch fr {
	case "stop", "":
		return ir.StopEndTurn
	case "tool_calls":
		return ir.StopToolUse
	case "length":
		return ir.StopMaxTokens
	case "content_filter":
		return ir.StopContentFilter
	case "stop_sequence":
		return ir.StopSequence
	default:
		return ir.StopEndTurn
	}
}

func stopToChatFinish(s ir.StopReason) string {
	switch s {
	case ir.StopEndTurn, ir.StopError:
		return "stop"
	case ir.StopToolUse:
		return "tool_calls"
	case ir.StopMaxTokens:
		return "length"
	case ir.StopContentFilter:
		return "content_filter"
	case ir.StopSequence:
		return "stop"
	default:
		return "stop"
	}
}

func (c *completion) finishReason() string {
	if len(c.Choices) == 0 {
		return ""
	}
	return c.Choices[0].FinishReason
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// errStreamingNotImpl is returned by the (Phase 2) streaming stubs.
var errStreamingNotImpl = errors.New("openaichat: streaming not implemented in this phase")

func nowUnix() int64 { return timeNowUnix() }

// Package anthropicmessages implements the Adapter for the Anthropic Messages
// protocol (POST /v1/messages). This protocol maps almost 1:1 onto the IR
// (the IR's block model is an Anthropic superset), so conversion is the most
// direct of the built-in adapters. See docs/05-unified-protocol.md §3.3.
package anthropicmessages

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/aigateway/ai-hub/internal/adapter"
	"github.com/aigateway/ai-hub/internal/ir"
)

// New returns the Anthropic Messages adapter.
func New() *Adapter { return &Adapter{} }

type Adapter struct{}

func (a *Adapter) Protocol() adapter.Protocol { return adapter.ProtocolMessages }

// ---------------------------------------------------------------------------
// Wire types (Anthropic Messages)
// ---------------------------------------------------------------------------

type request struct {
	Model         string          `json:"model"`
	System        json.RawMessage `json:"system,omitempty"` // string | []block
	Messages      []wireMessage   `json:"messages"`
	Tools         []wireTool      `json:"tools,omitempty"`
	ToolChoice    json.RawMessage `json:"tool_choice,omitempty"` // {type:...[,name]}
	MaxTokens     int             `json:"max_tokens,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Thinking      *wireThinking   `json:"thinking,omitempty"`
}

type wireThinking struct {
	Type         string `json:"type"` // "enabled" | "disabled"
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

// wireMessage.Content is either a JSON string or an array of content blocks.
type wireMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string | []block
}

// block covers all Anthropic content-block variants.
type block struct {
	Type string `json:"type"`

	// text
	Text string `json:"text,omitempty"`

	// image
	Source *imageSource `json:"source,omitempty"`

	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"` // string | []block
	IsError   bool            `json:"is_error,omitempty"`

	// thinking
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`

	// cache breakpoint
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type imageSource struct {
	Type      string `json:"type"` // base64 | url
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type cacheControl struct {
	Type string `json:"type"` // ephemeral
}

type wireTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type message struct {
	ID         string  `json:"id"`
	Type       string  `json:"type"` // "message"
	Role       string  `json:"role"` // "assistant"
	Model      string  `json:"model"`
	Content    []block `json:"content"`
	StopReason string  `json:"stop_reason"`
	Usage      *usage  `json:"usage,omitempty"`
}

type usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// ---------------------------------------------------------------------------
// DecodeRequest: Anthropic wire -> IR
// ---------------------------------------------------------------------------

func (a *Adapter) DecodeRequest(raw []byte, _ http.Header) (*ir.UnifiedRequest, error) {
	var w request
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, fmt.Errorf("anthropicmessages: decode request: %w", err)
	}
	req := &ir.UnifiedRequest{
		ClientProtocol: string(adapter.ProtocolMessages),
		Model:          w.Model,
		Stream:         w.Stream,
		MaxTokens:      w.MaxTokens,
		Temperature:    w.Temperature,
		TopP:           w.TopP,
		Stop:           w.StopSequences,
	}
	req.System = decodeSystem(w.System)
	for i := range w.Messages {
		wm := &w.Messages[i]
		req.Messages = append(req.Messages, ir.Message{
			Role:   ir.Role(wm.Role),
			Blocks: decodeContent(wm.Content),
		})
	}
	for _, t := range w.Tools {
		schema := t.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		req.Tools = append(req.Tools, ir.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
			Kind:        ir.ToolKindFunction,
		})
	}
	req.ToolChoice = decodeToolChoice(w.ToolChoice)
	if w.Thinking != nil && w.Thinking.Type == "enabled" {
		req.Thinking = &ir.Thinking{Enabled: true, BudgetTokens: w.Thinking.BudgetTokens}
	}
	return req, nil
}

// ---------------------------------------------------------------------------
// BuildUpstream: IR -> Anthropic wire
// ---------------------------------------------------------------------------

func (a *Adapter) BuildUpstream(req *ir.UnifiedRequest, upstreamModel string) (*adapter.UpstreamRequest, error) {
	out := request{
		Model:         upstreamModel,
		MaxTokens:     req.MaxTokens,
		Temperature:   req.Temperature,
		TopP:          req.TopP,
		Stream:        req.Stream,
		StopSequences: req.Stop,
	}
	out.System = encodeSystem(req.System)
	for _, m := range req.Messages {
		out.Messages = append(out.Messages, wireMessage{
			Role:    string(m.Role),
			Content: encodeContent(m.Blocks),
		})
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, wireTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: orDefaultSchema(t.InputSchema),
		})
	}
	if req.ToolChoice != nil {
		out.ToolChoice = encodeToolChoice(req.ToolChoice)
	}
	if req.Thinking != nil && req.Thinking.Enabled {
		out.Thinking = &wireThinking{Type: "enabled", BudgetTokens: req.Thinking.BudgetTokens}
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("anthropicmessages: build upstream: %w", err)
	}
	return &adapter.UpstreamRequest{Path: "/v1/messages", Body: body}, nil
}

// ---------------------------------------------------------------------------
// DecodeUpstreamResponse: Anthropic wire -> IR
// ---------------------------------------------------------------------------

func (a *Adapter) DecodeUpstreamResponse(raw []byte) (*ir.UnifiedResponse, error) {
	var m message
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("anthropicmessages: decode upstream response: %w", err)
	}
	resp := &ir.UnifiedResponse{
		ID:            m.ID,
		Model:         m.Model,
		UpstreamModel: m.Model,
		Blocks:        blocksToIR(m.Content),
		StopReason:    anthropicStopToIR(m.StopReason),
	}
	if m.Usage != nil {
		resp.Usage = ir.Usage{
			InputTokens:         m.Usage.InputTokens,
			OutputTokens:        m.Usage.OutputTokens,
			CacheCreationTokens: m.Usage.CacheCreationInputTokens,
			CacheReadTokens:     m.Usage.CacheReadInputTokens,
		}
	}
	return resp, nil
}

// ---------------------------------------------------------------------------
// EncodeResponse: IR -> Anthropic wire
// ---------------------------------------------------------------------------

func (a *Adapter) EncodeResponse(resp *ir.UnifiedResponse) ([]byte, error) {
	out := message{
		ID:         resp.ID,
		Type:       "message",
		Role:       "assistant",
		Model:      orDefault(resp.Model, resp.UpstreamModel),
		Content:    irBlocksToWire(resp.Blocks),
		StopReason: irStopToAnthropic(resp.StopReason),
	}
	if out.ID == "" {
		out.ID = "msg_unknown"
	}
	out.Usage = &usage{
		InputTokens:              resp.Usage.InputTokens,
		OutputTokens:             resp.Usage.OutputTokens,
		CacheCreationInputTokens: resp.Usage.CacheCreationTokens,
		CacheReadInputTokens:     resp.Usage.CacheReadTokens,
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("anthropicmessages: encode response: %w", err)
	}
	return body, nil
}

func (a *Adapter) NewStreamEncoder() adapter.StreamEncoder { return &streamEnc{} }
func (a *Adapter) NewStreamDecoder() adapter.StreamDecoder { return &streamDec{} }

// ---------------------------------------------------------------------------
// helpers: content / system
// ---------------------------------------------------------------------------

func decodeSystem(raw json.RawMessage) []ir.Block {
	if len(raw) == 0 || isNull(raw) {
		return nil
	}
	if s, ok := parseString(raw); ok {
		if s == "" {
			return nil
		}
		return []ir.Block{{Type: ir.BlockText, Text: s}}
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	return blocksToIR(blocks)
}

func encodeSystem(blocks []ir.Block) json.RawMessage {
	if len(blocks) == 0 {
		return nil
	}
	// Single text block with no cache hint -> emit as a plain string.
	if len(blocks) == 1 && blocks[0].Type == ir.BlockText && blocks[0].Cache == nil {
		b, _ := json.Marshal(blocks[0].Text)
		return b
	}
	wire := irBlocksToWire(blocks)
	b, _ := json.Marshal(wire)
	return b
}

// decodeContent parses a message content field (string or []block) into IR blocks.
func decodeContent(raw json.RawMessage) []ir.Block {
	if len(raw) == 0 || isNull(raw) {
		return nil
	}
	if s, ok := parseString(raw); ok {
		return []ir.Block{{Type: ir.BlockText, Text: s}}
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	return blocksToIR(blocks)
}

// encodeContent marshals IR blocks into a content field: a plain string when
// there's a single text block, otherwise an array of wire blocks.
func encodeContent(blocks []ir.Block) json.RawMessage {
	if len(blocks) == 0 {
		return jsonString("")
	}
	if len(blocks) == 1 && blocks[0].Type == ir.BlockText && blocks[0].Cache == nil {
		return jsonString(blocks[0].Text)
	}
	b, _ := json.Marshal(irBlocksToWire(blocks))
	return b
}

// blocksToIR converts Anthropic wire blocks to IR blocks.
func blocksToIR(blocks []block) []ir.Block {
	out := make([]ir.Block, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			out = append(out, ir.Block{Type: ir.BlockText, Text: b.Text, Cache: decodeCache(b.CacheControl)})
		case "image":
			img := &ir.ImageSource{}
			if b.Source != nil {
				if b.Source.Type == "url" {
					img.URL = b.Source.URL
				} else {
					img.Base64 = b.Source.Data
					img.MediaType = b.Source.MediaType
				}
			}
			out = append(out, ir.Block{Type: ir.BlockImage, Image: img})
		case "tool_use":
			input := b.Input
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			out = append(out, ir.Block{
				Type:     ir.BlockToolUse,
				ToolCall: &ir.ToolCall{ID: b.ID, Name: b.Name, Input: input},
			})
		case "tool_result":
			res := &ir.ToolResult{ToolUseID: b.ToolUseID, IsError: b.IsError}
			res.Content = decodeContent(b.Content) // string | []block -> IR blocks
			out = append(out, ir.Block{Type: ir.BlockToolResult, ToolResult: res})
		case "thinking":
			out = append(out, ir.Block{Type: ir.BlockThinking, Text: b.Thinking, Signature: b.Signature})
		case "redacted_thinking":
			// preserve as opaque thinking with signature only
			out = append(out, ir.Block{Type: ir.BlockThinking, Signature: b.Signature})
		}
	}
	return out
}

// irBlocksToWire converts IR blocks to Anthropic wire blocks.
func irBlocksToWire(blocks []ir.Block) []block {
	out := make([]block, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case ir.BlockText:
			out = append(out, block{Type: "text", Text: b.Text, CacheControl: encodeCache(b.Cache)})
		case ir.BlockImage:
			wb := block{Type: "image"}
			if b.Image != nil {
				if b.Image.URL != "" {
					wb.Source = &imageSource{Type: "url", URL: b.Image.URL}
				} else {
					wb.Source = &imageSource{Type: "base64", MediaType: b.Image.MediaType, Data: b.Image.Base64}
				}
			}
			out = append(out, wb)
		case ir.BlockToolUse:
			input := b.ToolCall.Input
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			out = append(out, block{Type: "tool_use", ID: b.ToolCall.ID, Name: b.ToolCall.Name, Input: input})
		case ir.BlockToolResult:
			wb := block{Type: "tool_result", ToolUseID: b.ToolResult.ToolUseID, IsError: b.ToolResult.IsError}
			wb.Content = encodeContent(b.ToolResult.Content)
			out = append(out, wb)
		case ir.BlockThinking, ir.BlockReasoning:
			text := b.Text
			if b.Type == ir.BlockReasoning {
				text = b.Text // o-series reasoning carried as thinking on Anthropic egress
			}
			out = append(out, block{Type: "thinking", Thinking: text, Signature: b.Signature})
		}
	}
	return out
}

func decodeCache(cc *cacheControl) *ir.CacheHint {
	if cc == nil {
		return nil
	}
	return &ir.CacheHint{Strategy: orDefault(cc.Type, "ephemeral")}
}

func encodeCache(c *ir.CacheHint) *cacheControl {
	if c == nil {
		return nil
	}
	return &cacheControl{Type: orDefault(c.Strategy, "ephemeral")}
}

// ---------------------------------------------------------------------------
// helpers: tool choice / stop / usage
// ---------------------------------------------------------------------------

func decodeToolChoice(raw json.RawMessage) *ir.ToolChoice {
	if len(raw) == 0 || isNull(raw) {
		return nil
	}
	var obj struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &obj) != nil {
		return nil
	}
	switch obj.Type {
	case "auto":
		return &ir.ToolChoice{Mode: ir.ToolChoiceAuto}
	case "any":
		return &ir.ToolChoice{Mode: ir.ToolChoiceAny}
	case "none":
		return &ir.ToolChoice{Mode: ir.ToolChoiceNone}
	case "tool":
		return &ir.ToolChoice{Mode: ir.ToolChoiceSpecific, Name: obj.Name}
	}
	return nil
}

func encodeToolChoice(tc *ir.ToolChoice) json.RawMessage {
	var obj map[string]string
	switch tc.Mode {
	case ir.ToolChoiceAuto:
		obj = map[string]string{"type": "auto"}
	case ir.ToolChoiceAny:
		obj = map[string]string{"type": "any"}
	case ir.ToolChoiceNone:
		obj = map[string]string{"type": "none"}
	case ir.ToolChoiceSpecific:
		obj = map[string]string{"type": "tool", "name": tc.Name}
	default:
		obj = map[string]string{"type": "auto"}
	}
	b, _ := json.Marshal(obj)
	return b
}

func anthropicStopToIR(s string) ir.StopReason {
	switch s {
	case "end_turn", "":
		return ir.StopEndTurn
	case "tool_use":
		return ir.StopToolUse
	case "max_tokens", "model_length":
		return ir.StopMaxTokens
	case "stop_sequence":
		return ir.StopSequence
	case "refusal", "pause_turn":
		return ir.StopEndTurn // no exact IR equivalent; degrade to end_turn
	default:
		return ir.StopEndTurn
	}
}

func irStopToAnthropic(s ir.StopReason) string {
	switch s {
	case ir.StopEndTurn, ir.StopError, ir.StopContentFilter:
		return "end_turn"
	case ir.StopToolUse:
		return "tool_use"
	case ir.StopMaxTokens:
		return "max_tokens"
	case ir.StopSequence:
		return "stop_sequence"
	default:
		return "end_turn"
	}
}

func orDefaultSchema(schema json.RawMessage) json.RawMessage {
	if len(schema) == 0 {
		return json.RawMessage(`{"type":"object"}`)
	}
	return schema
}

func parseString(raw json.RawMessage) (string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, true
	}
	return "", false
}

func isNull(raw json.RawMessage) bool { return len(raw) == 0 || string(raw) == "null" }

func jsonString(s string) json.RawMessage { b, _ := json.Marshal(s); return b }

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// Package openairesponses implements the Adapter for the OpenAI Responses API
// (POST /v1/responses), used by Codex CLI and the OpenAI Agents SDK. Unlike
// Chat/Messages, Responses models conversation as a flat list of typed "items"
// (message / function_call / function_call_output / reasoning) rather than a
// message array. The IR's block model is a superset, so conversion is lossless
// for the non-streaming path implemented here; streaming arrives in a follow-up.
// See docs/05-unified-protocol.md §3.2, docs/06-tool-calling.md.
package openairesponses

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/aigateway/ai-hub/internal/adapter"
	"github.com/aigateway/ai-hub/internal/ir"
)

// New returns the Responses adapter.
func New() *Adapter { return &Adapter{} }

type Adapter struct{}

func (a *Adapter) Protocol() adapter.Protocol { return adapter.ProtocolResponses }

// ---------------------------------------------------------------------------
// Wire types (Responses API)
// ---------------------------------------------------------------------------

type request struct {
	Model              string          `json:"model"`
	Input              json.RawMessage `json:"input"` // string | []item
	Instructions       string          `json:"instructions,omitempty"`
	Stream             bool            `json:"stream,omitempty"`
	Tools              []wireTool      `json:"tools,omitempty"`
	ToolChoice         json.RawMessage `json:"tool_choice,omitempty"` // string | object
	Reasoning          *wireReasoning  `json:"reasoning,omitempty"`
	MaxOutputTokens    int             `json:"max_output_tokens,omitempty"`
	Temperature        *float64        `json:"temperature,omitempty"`
	TopP               *float64        `json:"top_p,omitempty"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
}

type wireReasoning struct {
	Effort  string          `json:"effort,omitempty"`
	Summary json.RawMessage `json:"summary,omitempty"`
}

// item is one Responses input/output item.
type item struct {
	Type             string          `json:"type,omitempty"` // message | function_call | function_call_output | reasoning
	Role             string          `json:"role,omitempty"` // message
	ID               string          `json:"id,omitempty"`
	CallID           string          `json:"call_id,omitempty"`
	Name             string          `json:"name,omitempty"`
	Arguments        string          `json:"arguments,omitempty"`         // function_call (string)
	Output           json.RawMessage `json:"output,omitempty"`            // function_call_output (string | object)
	Content          json.RawMessage `json:"content,omitempty"`           // message (string | []part)
	EncryptedContent string          `json:"encrypted_content,omitempty"` // reasoning signature
	Status           string          `json:"status,omitempty"`
}

type part struct {
	Type     string `json:"type"` // input_text | output_text | input_image | refusal
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type wireTool struct {
	Type        string          `json:"type"` // function | web_search_preview | ...
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"` // function
}

type response struct {
	ID                string         `json:"id"`
	Object            string         `json:"object"` // "response"
	Model             string         `json:"model"`
	Output            []item         `json:"output"`
	Status            string         `json:"status"` // completed | incomplete | in_progress
	IncompleteDetails *incompleteDet `json:"incomplete_details,omitempty"`
	Usage             *wireUsage     `json:"usage,omitempty"`
}

type incompleteDet struct {
	Reason string `json:"reason,omitempty"` // max_output_tokens | content_filter
}

type wireUsage struct {
	InputTokens         int           `json:"input_tokens"`
	OutputTokens        int           `json:"output_tokens"`
	TotalTokens         int           `json:"total_tokens"`
	InputTokensDetails  *tokenDetails `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *tokenDetails `json:"output_tokens_details,omitempty"`
}

type tokenDetails struct {
	CachedTokens    int `json:"cached_tokens,omitempty"`
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

// ---------------------------------------------------------------------------
// DecodeRequest: Responses wire -> IR
// ---------------------------------------------------------------------------

func (a *Adapter) DecodeRequest(raw []byte, _ http.Header) (*ir.UnifiedRequest, error) {
	var w request
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, err
	}
	req := &ir.UnifiedRequest{
		ClientProtocol: string(adapter.ProtocolResponses),
		Model:          w.Model,
		Stream:         w.Stream,
		MaxTokens:      w.MaxOutputTokens,
		Temperature:    w.Temperature,
		TopP:           w.TopP,
	}
	if w.Instructions != "" {
		req.System = []ir.Block{{Type: ir.BlockText, Text: w.Instructions}}
	}

	items := decodeInput(w.Input)
	var msgs []ir.Message
	ensureLast := func(role ir.Role) {
		if len(msgs) > 0 && msgs[len(msgs)-1].Role == role {
			return
		}
		msgs = append(msgs, ir.Message{Role: role})
	}
	addBlock := func(role ir.Role, b ir.Block) {
		ensureLast(role)
		msgs[len(msgs)-1].Blocks = append(msgs[len(msgs)-1].Blocks, b)
	}

	for _, it := range items {
		switch it.Type {
		case "function_call":
			input := json.RawMessage(it.Arguments)
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			addBlock(ir.RoleAssistant, ir.Block{Type: ir.BlockToolUse, ToolCall: &ir.ToolCall{
				ID: it.CallID, Name: it.Name, Input: input,
			}})
		case "function_call_output":
			addBlock(ir.RoleUser, ir.Block{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
				ToolUseID: it.CallID, Content: []ir.Block{{Type: ir.BlockText, Text: rawToText(it.Output)}},
			}})
		case "reasoning":
			addBlock(ir.RoleAssistant, ir.Block{Type: ir.BlockReasoning, Text: summaryText(it.EncryptedContent), Signature: it.EncryptedContent})
		default: // message (type=="message" or shorthand with role)
			role := roleOf(it.Role)
			blocks := contentToBlocks(it.Content, role)
			if role == ir.RoleSystem {
				req.System = append(req.System, blocks...)
				continue
			}
			for _, b := range blocks {
				addBlock(role, b)
			}
		}
	}
	req.Messages = msgs

	for _, t := range w.Tools {
		if t.Type == "function" {
			schema := t.Parameters
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object"}`)
			}
			req.Tools = append(req.Tools, ir.Tool{
				Name: t.Name, Description: t.Description, InputSchema: schema, Kind: ir.ToolKindFunction,
			})
		}
		// built-in tools (web_search, etc.) are dropped here; passthrough TBD.
	}
	req.ToolChoice = decodeToolChoice(w.ToolChoice)
	if w.Reasoning != nil && w.Reasoning.Effort != "" {
		req.Thinking = &ir.Thinking{Enabled: true, Effort: w.Reasoning.Effort}
	}
	return req, nil
}

// ---------------------------------------------------------------------------
// BuildUpstream: IR -> Responses wire
// ---------------------------------------------------------------------------

func (a *Adapter) BuildUpstream(req *ir.UnifiedRequest, upstreamModel string) (*adapter.UpstreamRequest, error) {
	out := request{
		Model:           upstreamModel,
		Stream:          req.Stream,
		MaxOutputTokens: req.MaxTokens,
		Temperature:     req.Temperature,
		TopP:            req.TopP,
	}
	if len(req.System) > 0 {
		out.Instructions = blocksToText(req.System)
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, wireTool{
			Type: "function", Name: t.Name, Description: t.Description,
			Parameters: orDefaultSchema(t.InputSchema),
		})
	}
	if req.ToolChoice != nil {
		out.ToolChoice = encodeToolChoice(req.ToolChoice)
	}
	if req.Thinking != nil && req.Thinking.Enabled && req.Thinking.Effort != "" {
		out.Reasoning = &wireReasoning{Effort: req.Thinking.Effort}
	}

	var items []item
	for _, m := range req.Messages {
		switch m.Role {
		case ir.RoleUser:
			for _, b := range m.Blocks {
				if b.Type == ir.BlockToolResult {
					items = append(items, item{
						Type: "function_call_output", CallID: b.ToolResult.ToolUseID,
						Output: json.RawMessage(`"` + jsonEscape(blocksToText(b.ToolResult.Content)) + `"`),
					})
				}
			}
			// non-tool user content -> a message item
			if parts := blocksToUserParts(m.Blocks); len(parts) > 0 {
				items = append(items, item{Type: "message", Role: "user", Content: partsRaw(parts)})
			}
		case ir.RoleAssistant:
			// reasoning, then text, then function calls
			for _, b := range m.Blocks {
				if b.Type == ir.BlockReasoning {
					items = append(items, item{Type: "reasoning", EncryptedContent: b.Signature, Status: "complete"})
				}
			}
			if tp := blocksToOutputParts(m.Blocks); len(tp) > 0 {
				items = append(items, item{Type: "message", Role: "assistant", Content: partsRaw(tp)})
			}
			for _, b := range m.Blocks {
				if b.Type == ir.BlockToolUse {
					args := "{}"
					if len(b.ToolCall.Input) > 0 {
						args = string(b.ToolCall.Input)
					}
					items = append(items, item{
						Type: "function_call", CallID: b.ToolCall.ID, Name: b.ToolCall.Name,
						Arguments: args, Status: "completed",
					})
				}
			}
		}
	}
	out.Input = mustMarshal(items)
	body, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return &adapter.UpstreamRequest{Path: "/v1/responses", Body: body}, nil
}

// ---------------------------------------------------------------------------
// DecodeUpstreamResponse: Responses wire -> IR
// ---------------------------------------------------------------------------

func (a *Adapter) DecodeUpstreamResponse(raw []byte) (*ir.UnifiedResponse, error) {
	var r response
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	resp := &ir.UnifiedResponse{
		ID:            r.ID,
		Model:         r.Model,
		UpstreamModel: r.Model,
		StopReason:    inferStop(r),
	}
	for _, it := range r.Output {
		switch it.Type {
		case "message":
			for _, b := range contentToBlocks(it.Content, ir.RoleAssistant) {
				resp.Blocks = append(resp.Blocks, b)
			}
		case "function_call":
			input := json.RawMessage(it.Arguments)
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			resp.Blocks = append(resp.Blocks, ir.Block{Type: ir.BlockToolUse, ToolCall: &ir.ToolCall{
				ID: it.CallID, Name: it.Name, Input: input,
			}})
		case "reasoning":
			resp.Blocks = append(resp.Blocks, ir.Block{Type: ir.BlockReasoning, Signature: it.EncryptedContent})
		}
	}
	if r.Usage != nil {
		resp.Usage = ir.Usage{
			InputTokens:     r.Usage.InputTokens,
			OutputTokens:    r.Usage.OutputTokens,
			CacheReadTokens: tokenDetail(r.Usage.InputTokensDetails, "cached"),
			ReasoningTokens: tokenDetail(r.Usage.OutputTokensDetails, "reasoning"),
		}
	}
	return resp, nil
}

// ---------------------------------------------------------------------------
// EncodeResponse: IR -> Responses wire
// ---------------------------------------------------------------------------

func (a *Adapter) EncodeResponse(resp *ir.UnifiedResponse) ([]byte, error) {
	out := response{
		ID: orDefault(resp.ID, "resp_"+randHex(12)), Object: "response",
		Model: orDefault(resp.Model, resp.UpstreamModel), Status: "completed",
	}
	if resp.StopReason == ir.StopMaxTokens {
		out.Status = "incomplete"
		out.IncompleteDetails = &incompleteDet{Reason: "max_output_tokens"}
	}
	for _, b := range resp.Blocks {
		switch b.Type {
		case ir.BlockText:
			out.Output = append(out.Output, item{Type: "message", Role: "assistant",
				Content: partsRaw([]part{{Type: "output_text", Text: b.Text}})})
		case ir.BlockToolUse:
			args := "{}"
			if len(b.ToolCall.Input) > 0 {
				args = string(b.ToolCall.Input)
			}
			out.Output = append(out.Output, item{
				Type: "function_call", CallID: b.ToolCall.ID, Name: b.ToolCall.Name,
				Arguments: args, Status: "completed",
			})
		case ir.BlockReasoning:
			out.Output = append(out.Output, item{Type: "reasoning", EncryptedContent: b.Signature, Status: "complete"})
		}
	}
	out.Usage = &wireUsage{
		InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens,
		TotalTokens: resp.Usage.InputTokens + resp.Usage.OutputTokens,
	}
	if resp.Usage.CacheReadTokens > 0 {
		out.Usage.InputTokensDetails = &tokenDetails{CachedTokens: resp.Usage.CacheReadTokens}
	}
	if resp.Usage.ReasoningTokens > 0 {
		out.Usage.OutputTokensDetails = &tokenDetails{ReasoningTokens: resp.Usage.ReasoningTokens}
	}
	return json.Marshal(out)
}

func (a *Adapter) NewStreamEncoder() adapter.StreamEncoder { return &streamEnc{} }
func (a *Adapter) NewStreamDecoder() adapter.StreamDecoder { return &streamDec{} }

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func inferStop(r response) ir.StopReason {
	for _, it := range r.Output {
		if it.Type == "function_call" {
			return ir.StopToolUse
		}
	}
	if r.Status == "incomplete" {
		return ir.StopMaxTokens
	}
	return ir.StopEndTurn
}

// decodeInput accepts a string shorthand or an items array.
func decodeInput(raw json.RawMessage) []item {
	if len(raw) == 0 || isNull(raw) {
		return nil
	}
	if s, ok := parseString(raw); ok {
		return []item{{Type: "message", Role: "user", Content: jsonString(s)}}
	}
	var items []item
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	return items
}

func roleOf(r string) ir.Role {
	switch r {
	case "system", "developer":
		return ir.RoleSystem
	case "assistant":
		return ir.RoleAssistant
	default:
		return ir.RoleUser
	}
}

// contentToBlocks parses a message content field (string | []part) into IR blocks.
func contentToBlocks(raw json.RawMessage, role ir.Role) []ir.Block {
	if len(raw) == 0 || isNull(raw) {
		return nil
	}
	if s, ok := parseString(raw); ok {
		return []ir.Block{{Type: ir.BlockText, Text: s}}
	}
	var parts []part
	if json.Unmarshal(raw, &parts) != nil {
		return nil
	}
	var blocks []ir.Block
	for _, p := range parts {
		switch p.Type {
		case "input_text", "output_text", "text", "refusal":
			blocks = append(blocks, ir.Block{Type: ir.BlockText, Text: p.Text})
		case "input_image":
			blocks = append(blocks, ir.Block{Type: ir.BlockImage, Image: &ir.ImageSource{URL: p.ImageURL}})
		}
	}
	return blocks
}

func blocksToUserParts(blocks []ir.Block) []part {
	var out []part
	for _, b := range blocks {
		if b.Type == ir.BlockToolResult {
			continue
		}
		switch b.Type {
		case ir.BlockText:
			out = append(out, part{Type: "input_text", Text: b.Text})
		case ir.BlockImage:
			url := ""
			if b.Image != nil {
				url = b.Image.URL
			}
			out = append(out, part{Type: "input_image", ImageURL: url})
		}
	}
	return out
}

func blocksToOutputParts(blocks []ir.Block) []part {
	var out []part
	for _, b := range blocks {
		if b.Type == ir.BlockText {
			out = append(out, part{Type: "output_text", Text: b.Text})
		}
	}
	return out
}

func partsRaw(parts []part) json.RawMessage {
	b, _ := json.Marshal(parts)
	return b
}

func blocksToText(blocks []ir.Block) string {
	var sb strings.Builder
	for _, b := range blocks {
		if b.Type == ir.BlockText {
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// rawToText turns a function_call_output output (string | object) into text.
func rawToText(raw json.RawMessage) string {
	if s, ok := parseString(raw); ok {
		return s
	}
	if len(raw) == 0 || isNull(raw) {
		return ""
	}
	return string(raw) // object: pass the JSON as-is text (good enough for tool results)
}

func summaryText(s string) string { return s } // reasoning visible text is opaque; signature carries it

func decodeToolChoice(raw json.RawMessage) *ir.ToolChoice {
	if len(raw) == 0 || isNull(raw) {
		return nil
	}
	if s, ok := parseString(raw); ok {
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
	var obj struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Type == "function" {
		return &ir.ToolChoice{Mode: ir.ToolChoiceSpecific, Name: obj.Name}
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
		b, _ := json.Marshal(map[string]string{"type": "function", "name": tc.Name})
		return b
	}
	return jsonString("auto")
}

func tokenDetail(d *tokenDetails, kind string) int {
	if d == nil {
		return 0
	}
	if kind == "cached" {
		return d.CachedTokens
	}
	return d.ReasoningTokens
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
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
func jsonString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}
func mustMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

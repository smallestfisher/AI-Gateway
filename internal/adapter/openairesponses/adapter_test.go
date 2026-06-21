package openairesponses

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/aigateway/ai-hub/internal/ir"
)

var decodeHdr = http.Header{}

// ---------------------------------------------------------------------------
// DecodeRequest
// ---------------------------------------------------------------------------

// The string shorthand for `input` ("hi") must decode to a single user message.
func TestDecodeRequest_StringShorthand(t *testing.T) {
	raw := mustJ(t, map[string]any{"model": "gpt-4o", "input": "hi"})
	req, err := New().DecodeRequest(raw, decodeHdr)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != ir.RoleUser {
		t.Fatalf("messages = %+v", req.Messages)
	}
	if len(req.Messages[0].Blocks) != 1 || req.Messages[0].Blocks[0].Text != "hi" {
		t.Errorf("user text lost: %+v", req.Messages[0].Blocks)
	}
}

// Reasoning encrypted_content (the signature that MUST survive a tool-call turn,
// docs/05 §5) must round-trip: decode -> IR BlockReasoning(Signature) -> build
// upstream -> decode back, signature intact.
func TestDecodeRequest_ReasoningSignatureRoundTrip(t *testing.T) {
	sig := "ABCDEF_encrypted_blob_123"
	raw := mustJ(t, map[string]any{
		"model": "o3",
		"input": []map[string]any{
			{"type": "reasoning", "id": "rs_1", "encrypted_content": sig},
			{"type": "message", "role": "assistant", "content": []map[string]any{{"type": "output_text", "text": "ok"}}},
		},
	})
	req, err := New().DecodeRequest(raw, decodeHdr)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var rs *ir.Block
	for _, m := range req.Messages {
		for i := range m.Blocks {
			if m.Blocks[i].Type == ir.BlockReasoning {
				rs = &m.Blocks[i]
			}
		}
	}
	if rs == nil || rs.Signature != sig {
		t.Fatalf("reasoning signature not decoded: %+v", rs)
	}

	// build upstream body and decode it back: signature must survive the hop
	up, err := New().BuildUpstream(req, "o3")
	if err != nil {
		t.Fatalf("build upstream: %v", err)
	}
	back, err := New().DecodeRequest(up.Body, decodeHdr)
	if err != nil {
		t.Fatalf("decode back: %v", err)
	}
	var rs2 *ir.Block
	for _, m := range back.Messages {
		for i := range m.Blocks {
			if m.Blocks[i].Type == ir.BlockReasoning {
				rs2 = &m.Blocks[i]
			}
		}
	}
	if rs2 == nil || rs2.Signature != sig {
		t.Errorf("reasoning signature lost on upstream hop: %+v", rs2)
	}
}

// reasoning.effort -> Thinking.Enabled+Effort; tool_choice variants map through.
func TestDecodeRequest_ReasoningEffortAndToolChoice(t *testing.T) {
	raw := mustJ(t, map[string]any{
		"model": "o3",
		"input": "hi",
		"reasoning": map[string]any{"effort": "high"},
		"tool_choice": map[string]any{"type": "function", "name": "search"},
	})
	req, err := New().DecodeRequest(raw, decodeHdr)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req.Thinking == nil || !req.Thinking.Enabled || req.Thinking.Effort != "high" {
		t.Errorf("thinking = %+v", req.Thinking)
	}
	if req.ToolChoice == nil || req.ToolChoice.Mode != ir.ToolChoiceSpecific || req.ToolChoice.Name != "search" {
		t.Errorf("tool_choice = %+v", req.ToolChoice)
	}
}

// temperature/top_p/max_output_tokens are carried through to the upstream body.
func TestBuildUpstream_CarriesSamplingParams(t *testing.T) {
	temp, topP := 0.7, 0.9
	req := &ir.UnifiedRequest{
		Model: "gpt-4o",
		Messages: []ir.Message{{Role: ir.RoleUser, Blocks: []ir.Block{{Type: ir.BlockText, Text: "hi"}}}},
		MaxTokens: 512, Temperature: &temp, TopP: &topP,
	}
	up, err := New().BuildUpstream(req, "gpt-4o-mini")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var got struct {
		Model           string   `json:"model"`
		MaxOutputTokens int      `json:"max_output_tokens"`
		Temperature     *float64 `json:"temperature"`
		TopP            *float64 `json:"top_p"`
		Stream          bool     `json:"stream"`
	}
	if err := json.Unmarshal(up.Body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Model != "gpt-4o-mini" || got.MaxOutputTokens != 512 {
		t.Errorf("model/tokens = %+v", got)
	}
	if got.Temperature == nil || *got.Temperature != 0.7 || got.TopP == nil || *got.TopP != 0.9 {
		t.Errorf("temp/topP = %+v", got)
	}
}

// ---------------------------------------------------------------------------
// EncodeResponse / DecodeUpstreamResponse
// ---------------------------------------------------------------------------

// A max_tokens stop reason must encode as status "incomplete" with
// incomplete_details.reason = max_output_tokens.
func TestEncodeResponse_MaxTokensIsIncomplete(t *testing.T) {
	resp := &ir.UnifiedResponse{
		ID: "resp_1", Model: "gpt-4o", StopReason: ir.StopMaxTokens,
		Blocks: []ir.Block{{Type: ir.BlockText, Text: "cut off"}},
		Usage:  ir.Usage{InputTokens: 1, OutputTokens: 1},
	}
	body, err := New().EncodeResponse(resp)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var got struct {
		Status            string `json:"status"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Status != "incomplete" {
		t.Errorf("status = %q want incomplete", got.Status)
	}
	if got.IncompleteDetails == nil || got.IncompleteDetails.Reason != "max_output_tokens" {
		t.Errorf("incomplete_details = %+v", got.IncompleteDetails)
	}
}

// An upstream Responses response with reasoning + cached/reasoning token
// details decodes into IR with those usage fields populated.
func TestDecodeUpstreamResponse_ReasoningAndUsageDetails(t *testing.T) {
	raw := mustJ(t, map[string]any{
		"id": "resp_9", "object": "response", "model": "o3", "status": "completed",
		"output": []map[string]any{
			{"type": "reasoning", "id": "rs_1", "encrypted_content": "SIG", "summary": []any{}},
			{"type": "message", "role": "assistant", "content": []map[string]any{{"type": "output_text", "text": "ans"}}},
		},
		"usage": map[string]any{
			"input_tokens": 10, "output_tokens": 5, "total_tokens": 15,
			"input_tokens_details":  map[string]any{"cached_tokens": 4},
			"output_tokens_details": map[string]any{"reasoning_tokens": 3},
		},
	})
	resp, err := New().DecodeUpstreamResponse(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 {
		t.Errorf("usage tokens = %+v", resp.Usage)
	}
	if resp.Usage.CacheReadTokens != 4 {
		t.Errorf("cached tokens = %d want 4", resp.Usage.CacheReadTokens)
	}
	if resp.Usage.ReasoningTokens != 3 {
		t.Errorf("reasoning tokens = %d want 3", resp.Usage.ReasoningTokens)
	}
	var rs *ir.Block
	for i := range resp.Blocks {
		if resp.Blocks[i].Type == ir.BlockReasoning {
			rs = &resp.Blocks[i]
		}
	}
	if rs == nil || rs.Signature != "SIG" {
		t.Errorf("reasoning signature lost: %+v", rs)
	}
}

// EncodeResponse output must be valid, parseable JSON (sanity).
func TestEncodeResponse_ValidJSON(t *testing.T) {
	resp := &ir.UnifiedResponse{ID: "resp_x", Model: "gpt-4o", StopReason: ir.StopEndTurn,
		Blocks: []ir.Block{{Type: ir.BlockText, Text: "hello"}}}
	body, err := New().EncodeResponse(resp)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !bytes.HasPrefix(bytes.TrimSpace(body), []byte("{")) {
		t.Errorf("encode did not produce a JSON object: %s", body)
	}
}

func mustJ(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

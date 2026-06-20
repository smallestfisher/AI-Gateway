package anthropicmessages

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/aigateway/ai-hub/internal/ir"
)

func TestDecodeRequest_BasicSystemAndText(t *testing.T) {
	body := mustJSON(t, map[string]any{
		"model":      "claude-sonnet",
		"max_tokens": 1024,
		"system":     "be concise",
		"messages": []map[string]any{
			{"role": "user", "content": "hi"},
		},
	})
	req, err := New().DecodeRequest(body, http.Header{})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(req.System) != 1 || req.System[0].Text != "be concise" {
		t.Errorf("system = %+v", req.System)
	}
	if len(req.Messages) != 1 || req.Messages[0].Blocks[0].Text != "hi" {
		t.Errorf("messages = %+v", req.Messages)
	}
	if req.MaxTokens != 1024 {
		t.Errorf("max_tokens = %d", req.MaxTokens)
	}
}

func TestDecodeRequest_ToolUseAndResultsAndThinking(t *testing.T) {
	body := mustJSON(t, map[string]any{
		"model":      "claude-sonnet",
		"max_tokens": 2048,
		"tools": []map[string]any{{
			"name": "get_weather", "description": "weather",
			"input_schema": map[string]any{"type": "object"},
		}},
		"tool_choice": map[string]any{"type": "any"},
		"thinking":    map[string]any{"type": "enabled", "budget_tokens": 1024},
		"messages": []map[string]any{
			{"role": "user", "content": "weather in Paris and Tokyo?"},
			{"role": "assistant", "content": []map[string]any{
				{"type": "thinking", "thinking": "let me reason", "signature": "sig1"},
				{"type": "tool_use", "id": "u_A", "name": "get_weather", "input": map[string]any{"city": "Paris"}},
				{"type": "tool_use", "id": "u_B", "name": "get_weather", "input": map[string]any{"city": "Tokyo"}},
			}},
			{"role": "user", "content": []map[string]any{
				{"type": "tool_result", "tool_use_id": "u_A", "content": "18C"},
				{"type": "tool_result", "tool_use_id": "u_B", "content": "25C"},
			}},
		},
	})
	req, err := New().DecodeRequest(body, http.Header{})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req.ToolChoice.Mode != ir.ToolChoiceAny {
		t.Errorf("tool_choice = %+v", req.ToolChoice)
	}
	if req.Thinking == nil || !req.Thinking.Enabled || req.Thinking.BudgetTokens != 1024 {
		t.Errorf("thinking = %+v", req.Thinking)
	}
	asst := req.Messages[1]
	// thinking + 2 parallel tool_use
	if len(asst.Blocks) != 3 {
		t.Fatalf("assistant blocks = %+v", asst.Blocks)
	}
	if asst.Blocks[0].Type != ir.BlockThinking || asst.Blocks[0].Signature != "sig1" {
		t.Errorf("thinking block = %+v", asst.Blocks[0])
	}
	uses := []ir.Block{asst.Blocks[1], asst.Blocks[2]}
	if uses[0].Type != ir.BlockToolUse || uses[1].Type != ir.BlockToolUse {
		t.Fatalf("tool_use blocks = %+v", uses)
	}
	if uses[0].ToolCall.ID != "u_A" || uses[1].ToolCall.ID != "u_B" {
		t.Errorf("tool ids = %s %s", uses[0].ToolCall.ID, uses[1].ToolCall.ID)
	}
	// results: one user message with 2 tool_result blocks
	res := req.Messages[2]
	if len(res.Blocks) != 2 || res.Blocks[0].ToolResult.ToolUseID != "u_A" {
		t.Errorf("results = %+v", res.Blocks)
	}
}

func TestRoundTrip_RequestLossless(t *testing.T) {
	orig := mustJSON(t, map[string]any{
		"model":      "claude-sonnet",
		"max_tokens": 512,
		"system":     "sys",
		"messages": []map[string]any{
			{"role": "user", "content": "q"},
			{"role": "assistant", "content": []map[string]any{
				{"type": "tool_use", "id": "x1", "name": "f", "input": map[string]any{"a": 1}},
			}},
			{"role": "user", "content": []map[string]any{
				{"type": "tool_result", "tool_use_id": "x1", "content": "ok"},
			}},
		},
	})
	a := New()
	req, err := a.DecodeRequest(orig, http.Header{})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	up, err := a.BuildUpstream(req, "claude-real")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if up.Path != "/v1/messages" {
		t.Errorf("path = %q", up.Path)
	}
	// decode the upstream body back and compare semantically
	req2, err := a.DecodeRequest(up.Body, http.Header{})
	if err != nil {
		t.Fatalf("decode upstream: %v", err)
	}
	if req2.Model != "claude-real" {
		t.Errorf("model = %q", req2.Model)
	}
	if len(req2.Messages) != len(req.Messages) {
		t.Fatalf("message count differs: %d vs %d", len(req2.Messages), len(req.Messages))
	}
	// assistant tool_use preserved
	asst := req2.Messages[1].Blocks
	if len(asst) != 1 || asst[0].ToolCall.Name != "f" || asst[0].ToolCall.ID != "x1" {
		t.Errorf("assistant block = %+v", asst)
	}
}

func TestDecodeUpstreamResponse_TextAndUsage(t *testing.T) {
	body := mustJSON(t, map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-sonnet",
		"content":     []map[string]any{{"type": "text", "text": "hi there"}},
		"stop_reason": "end_turn",
		"usage": map[string]any{
			"input_tokens": 11, "output_tokens": 4,
			"cache_creation_input_tokens": 6, "cache_read_input_tokens": 2,
		},
	})
	resp, err := New().DecodeUpstreamResponse(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.StopReason != ir.StopEndTurn {
		t.Errorf("stop = %s", resp.StopReason)
	}
	if len(resp.Blocks) != 1 || resp.Blocks[0].Text != "hi there" {
		t.Errorf("blocks = %+v", resp.Blocks)
	}
	if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 4 ||
		resp.Usage.CacheCreationTokens != 6 || resp.Usage.CacheReadTokens != 2 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestEncodeResponse_ToolUseRoundTrip(t *testing.T) {
	resp := &ir.UnifiedResponse{
		ID: "msg_2", Model: "claude-sonnet",
		Blocks: []ir.Block{
			{Type: ir.BlockText, Text: "calling"},
			{Type: ir.BlockToolUse, ToolCall: &ir.ToolCall{
				ID: "t1", Name: "f", Input: json.RawMessage(`{"x":1}`)}},
		},
		StopReason: ir.StopToolUse,
		Usage:      ir.Usage{InputTokens: 1, OutputTokens: 2},
	}
	out, err := New().EncodeResponse(resp)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	back, err := New().DecodeUpstreamResponse(out)
	if err != nil {
		t.Fatalf("decode back: %v", err)
	}
	if back.StopReason != ir.StopToolUse {
		t.Errorf("stop = %s", back.StopReason)
	}
	if len(back.Blocks) != 2 || back.Blocks[1].ToolCall.Name != "f" {
		t.Errorf("blocks = %+v", back.Blocks)
	}
	if back.Usage.InputTokens != 1 || back.Usage.OutputTokens != 2 {
		t.Errorf("usage = %+v", back.Usage)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

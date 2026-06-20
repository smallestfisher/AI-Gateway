package openaichat

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/aigateway/ai-hub/internal/ir"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestDecodeRequest_TextAndSystem(t *testing.T) {
	body := mustJSON(t, map[string]any{
		"model": "gpt-4o",
		"messages": []map[string]any{
			{"role": "system", "content": "you are helpful"},
			{"role": "user", "content": "hello"},
		},
	})
	a := New()
	req, err := a.DecodeRequest(body, http.Header{})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req.Model != "gpt-4o" {
		t.Errorf("model = %q", req.Model)
	}
	if len(req.System) != 1 || req.System[0].Text != "you are helpful" {
		t.Errorf("system = %+v", req.System)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != ir.RoleUser {
		t.Errorf("messages = %+v", req.Messages)
	}
	if got := req.Messages[0].Blocks[0].Text; got != "hello" {
		t.Errorf("text = %q", got)
	}
}

func TestDecodeRequest_MultimodalContent(t *testing.T) {
	body := mustJSON(t, map[string]any{
		"model": "gpt-4o",
		"messages": []map[string]any{
			{"role": "user", "content": []map[string]any{
				{"type": "text", "text": "what's this?"},
				{"type": "image_url", "image_url": map[string]any{"url": "https://x/a.png"}},
			}},
		},
	})
	req, err := New().DecodeRequest(body, http.Header{})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	blocks := req.Messages[0].Blocks
	if len(blocks) != 2 || blocks[0].Type != ir.BlockText || blocks[1].Type != ir.BlockImage {
		t.Fatalf("blocks = %+v", blocks)
	}
	if blocks[1].Image.URL != "https://x/a.png" {
		t.Errorf("image url = %q", blocks[1].Image.URL)
	}
}

func TestDecodeRequest_ToolCallsAndResults(t *testing.T) {
	body := mustJSON(t, map[string]any{
		"model": "gpt-4o",
		"messages": []map[string]any{
			{"role": "user", "content": "weather in Paris and Tokyo"},
			{"role": "assistant", "tool_calls": []map[string]any{
				{"id": "call_A", "type": "function", "function": map[string]any{
					"name": "get_weather", "arguments": `{"city":"Paris"}`}},
				{"id": "call_B", "type": "function", "function": map[string]any{
					"name": "get_weather", "arguments": `{"city":"Tokyo"}`}},
			}},
			{"role": "tool", "tool_call_id": "call_A", "content": "18C"},
			{"role": "tool", "tool_call_id": "call_B", "content": "25C"},
		},
		"tools": []map[string]any{
			{"type": "function", "function": map[string]any{
				"name": "get_weather", "parameters": map[string]any{"type": "object"}}},
		},
		"tool_choice": "required",
	})
	req, err := New().DecodeRequest(body, http.Header{})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	// tools
	if len(req.Tools) != 1 || req.Tools[0].Name != "get_weather" {
		t.Fatalf("tools = %+v", req.Tools)
	}
	if req.ToolChoice == nil || req.ToolChoice.Mode != ir.ToolChoiceAny {
		t.Errorf("tool_choice = %+v", req.ToolChoice)
	}
	// assistant message has 2 parallel tool_use blocks
	asst := req.Messages[1]
	if asst.Role != ir.RoleAssistant || len(asst.Blocks) != 2 {
		t.Fatalf("assistant = %+v", asst)
	}
	for _, b := range asst.Blocks {
		if b.Type != ir.BlockToolUse {
			t.Errorf("expected tool_use, got %s", b.Type)
		}
	}
	// the two tool messages collapse into ONE user message with 2 tool_result blocks
	results := req.Messages[2]
	if results.Role != ir.RoleUser || len(results.Blocks) != 2 {
		t.Fatalf("results = %+v", results)
	}
	if results.Blocks[0].ToolResult.ToolUseID != "call_A" {
		t.Errorf("result id = %q", results.Blocks[0].ToolResult.ToolUseID)
	}
}

func TestBuildUpstream_RoundTripPreservesSemantics(t *testing.T) {
	orig := mustJSON(t, map[string]any{
		"model": "gpt-4o",
		"messages": []map[string]any{
			{"role": "system", "content": "sys"},
			{"role": "user", "content": "hi"},
			{"role": "assistant", "tool_calls": []map[string]any{
				{"id": "c1", "type": "function", "function": map[string]any{"name": "f", "arguments": `{"x":1}`}},
			}},
			{"role": "tool", "tool_call_id": "c1", "content": "done"},
		},
	})
	a := New()
	req, err := a.DecodeRequest(orig, http.Header{})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	up, err := a.BuildUpstream(req, "gpt-4o-rewritten")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if up.Path != "/v1/chat/completions" {
		t.Errorf("path = %q", up.Path)
	}
	var got request
	if err := json.Unmarshal(up.Body, &got); err != nil {
		t.Fatalf("unmarshal upstream: %v", err)
	}
	if got.Model != "gpt-4o-rewritten" {
		t.Errorf("model not rewritten: %q", got.Model)
	}
	// system + user + assistant(tool_calls) + tool(result)
	roles := []string{got.Messages[0].Role, got.Messages[1].Role, got.Messages[2].Role, got.Messages[3].Role}
	want := []string{"system", "user", "assistant", "tool"}
	for i := range want {
		if roles[i] != want[i] {
			t.Errorf("msg[%d] role = %q want %q (all=%v)", i, roles[i], want[i], roles)
		}
	}
	if len(got.Messages[2].ToolCalls) != 1 || got.Messages[2].ToolCalls[0].Function.Name != "f" {
		t.Errorf("assistant tool_calls = %+v", got.Messages[2].ToolCalls)
	}
	if got.Messages[3].ToolCallID != "c1" || rawString(got.Messages[3].Content) != "done" {
		t.Errorf("tool message = %+v", got.Messages[3])
	}
}

func TestDecodeUpstreamResponse_ToolCalls(t *testing.T) {
	body := mustJSON(t, map[string]any{
		"id": "chatcmpl-1", "object": "chat.completion", "model": "gpt-4o",
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role": "assistant",
				"tool_calls": []map[string]any{
					{"id": "call_1", "type": "function", "function": map[string]any{
						"name": "get_weather", "arguments": `{"city":"Paris"}`}},
				},
			},
			"finish_reason": "tool_calls",
		}},
		"usage": map[string]any{
			"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15,
			"prompt_tokens_details":     map[string]any{"cached_tokens": 4},
			"completion_tokens_details": map[string]any{"reasoning_tokens": 2},
		},
	})
	resp, err := New().DecodeUpstreamResponse(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.StopReason != ir.StopToolUse {
		t.Errorf("stop = %s", resp.StopReason)
	}
	if len(resp.Blocks) != 1 || resp.Blocks[0].Type != ir.BlockToolUse {
		t.Fatalf("blocks = %+v", resp.Blocks)
	}
	tc := resp.Blocks[0].ToolCall
	if tc.ID != "call_1" || tc.Name != "get_weather" {
		t.Errorf("toolcall = %+v", tc)
	}
	var args map[string]any
	_ = json.Unmarshal(tc.Input, &args)
	if args["city"] != "Paris" {
		t.Errorf("input = %s", tc.Input)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 ||
		resp.Usage.CacheReadTokens != 4 || resp.Usage.ReasoningTokens != 2 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestEncodeResponse_LosslessWithIR(t *testing.T) {
	resp := &ir.UnifiedResponse{
		ID: "chatcmpl-9", Model: "claude-sonnet", UpstreamModel: "gpt-4o",
		Blocks: []ir.Block{
			{Type: ir.BlockText, Text: "Hello"},
			{Type: ir.BlockToolUse, ToolCall: &ir.ToolCall{
				ID: "c1", Name: "f", Input: json.RawMessage(`{"a":1}`)}},
		},
		StopReason: ir.StopToolUse,
		Usage:      ir.Usage{InputTokens: 3, OutputTokens: 7},
	}
	out, err := New().EncodeResponse(resp)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	// decode it back through the same adapter and compare semantics
	back, err := New().DecodeUpstreamResponse(out)
	if err != nil {
		t.Fatalf("decode back: %v", err)
	}
	if back.StopReason != ir.StopToolUse {
		t.Errorf("stop = %s", back.StopReason)
	}
	if len(back.Blocks) != 2 {
		t.Fatalf("blocks = %+v", back.Blocks)
	}
	if back.Blocks[0].Text != "Hello" {
		t.Errorf("text = %q", back.Blocks[0].Text)
	}
	if back.Blocks[1].ToolCall.Name != "f" {
		t.Errorf("tool = %+v", back.Blocks[1].ToolCall)
	}
	if back.Usage.InputTokens != 3 || back.Usage.OutputTokens != 7 {
		t.Errorf("usage = %+v", back.Usage)
	}
}

// Package crossprotocoltest validates that the IR is a lossless hub: a request
// or response decoded in one protocol and re-encoded in another preserves
// semantics. These are the architecture's most important tests.
package crossprotocoltest

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/aigateway/ai-hub/internal/adapter"
	"github.com/aigateway/ai-hub/internal/adapter/anthropicmessages"
	"github.com/aigateway/ai-hub/internal/adapter/openaichat"
	"github.com/aigateway/ai-hub/internal/adapter/openairesponses"
	"github.com/aigateway/ai-hub/internal/ir"
)

var (
	chat = openaichat.New()
	msg  = anthropicmessages.New()
	resp = openairesponses.New()
	hdr  = http.Header{}
)

// Messages -> IR -> Chat upstream: a Claude Code request (system + parallel
// tool_use + tool results) must survive translation into a Chat request body.
// This is the docs/06-tool-calling.md §7 scenario in the ingress->egress direction.
func TestMessagesRequestToChatUpstream(t *testing.T) {
	messagesReq := mustJSON(t, map[string]any{
		"model":      "claude-sonnet",
		"max_tokens": 1024,
		"system":     "你是助手",
		"tools": []map[string]any{{
			"name": "get_weather", "description": "weather",
			"input_schema": map[string]any{"type": "object",
				"properties": map[string]any{"city": map[string]any{"type": "string"}}},
		}},
		"messages": []map[string]any{
			{"role": "user", "content": "巴黎和东京的天气？"},
			{"role": "assistant", "content": []map[string]any{
				{"type": "tool_use", "id": "u_A", "name": "get_weather", "input": map[string]any{"city": "Paris"}},
				{"type": "tool_use", "id": "u_B", "name": "get_weather", "input": map[string]any{"city": "Tokyo"}},
			}},
			{"role": "user", "content": []map[string]any{
				{"type": "tool_result", "tool_use_id": "u_A", "content": "Paris 18C"},
				{"type": "tool_result", "tool_use_id": "u_B", "content": "Tokyo 25C"},
			}},
		},
	})

	// ingress: Messages -> IR
	ireq, err := msg.DecodeRequest(messagesReq, hdr)
	if err != nil {
		t.Fatalf("messages decode: %v", err)
	}
	if ireq.Model != "claude-sonnet" {
		t.Errorf("model = %q", ireq.Model)
	}
	if len(ireq.System) != 1 || ireq.System[0].Text != "你是助手" {
		t.Errorf("system lost: %+v", ireq.System)
	}
	// 2 parallel tool_use preserved
	asst := ireq.Messages[1]
	if len(asst.Blocks) != 2 || asst.Blocks[0].Type != ir.BlockToolUse {
		t.Fatalf("parallel tool_use lost: %+v", asst.Blocks)
	}
	// results: 1 user message, 2 tool_result blocks
	res := ireq.Messages[2]
	if len(res.Blocks) != 2 || res.Blocks[0].Type != ir.BlockToolResult {
		t.Fatalf("tool_result grouping lost: %+v", res)
	}

	// egress: IR -> Chat upstream body
	up, err := chat.BuildUpstream(ireq, "gpt-4o")
	if err != nil {
		t.Fatalf("chat build: %v", err)
	}

	// decode the produced Chat body back with the Chat adapter and assert
	// semantics survived the cross-protocol hop.
	back, err := chat.DecodeRequest(up.Body, hdr)
	if err != nil {
		t.Fatalf("chat decode of upstream body: %v", err)
	}
	if back.Model != "gpt-4o" {
		t.Errorf("upstream model = %q", back.Model)
	}
	// system survived as System (chat decode pulls system message back out)
	if len(back.System) != 1 || back.System[0].Text != "你是助手" {
		t.Errorf("system lost across hop: %+v", back.System)
	}
	// assistant parallel tool calls survived
	bAsst := back.Messages[1]
	if bAsst.Role != ir.RoleAssistant || len(bAsst.Blocks) != 2 {
		t.Fatalf("assistant tool calls lost: %+v", bAsst)
	}
	for _, b := range bAsst.Blocks {
		if b.Type != ir.BlockToolUse {
			t.Errorf("expected tool_use, got %s", b.Type)
		}
	}
	// tool results survived (chat groups tool messages into a user turn)
	bRes := back.Messages[2]
	if bRes.Role != ir.RoleUser || len(bRes.Blocks) != 2 {
		t.Fatalf("tool results lost: %+v", bRes)
	}
	// id pairing preserved
	ids := map[string]bool{bRes.Blocks[0].ToolResult.ToolUseID: true, bRes.Blocks[1].ToolResult.ToolUseID: true}
	if !ids["u_A"] || !ids["u_B"] {
		t.Errorf("tool result id pairing lost: %+v", bRes.Blocks)
	}
}

// Chat -> IR -> Messages upstream: reverse direction.
func TestChatRequestToMessagesUpstream(t *testing.T) {
	chatReq := mustJSON(t, map[string]any{
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
	ireq, err := chat.DecodeRequest(chatReq, hdr)
	if err != nil {
		t.Fatalf("chat decode: %v", err)
	}
	up, err := msg.BuildUpstream(ireq, "claude-real")
	if err != nil {
		t.Fatalf("messages build: %v", err)
	}
	back, err := msg.DecodeRequest(up.Body, hdr)
	if err != nil {
		t.Fatalf("messages decode of upstream body: %v", err)
	}
	if back.Model != "claude-real" {
		t.Errorf("model = %q", back.Model)
	}
	if len(back.System) != 1 || back.System[0].Text != "sys" {
		t.Errorf("system lost: %+v", back.System)
	}
	// assistant tool_use
	asst := back.Messages[1]
	if len(asst.Blocks) != 1 || asst.Blocks[0].Type != ir.BlockToolUse ||
		asst.Blocks[0].ToolCall.ID != "c1" || asst.Blocks[0].ToolCall.Name != "f" {
		t.Errorf("assistant tool_use lost: %+v", asst)
	}
	// tool result
	res := back.Messages[2]
	if len(res.Blocks) != 1 || res.Blocks[0].Type != ir.BlockToolResult ||
		res.Blocks[0].ToolResult.ToolUseID != "c1" {
		t.Errorf("tool result lost: %+v", res)
	}
}

// Response cross-protocol: an Anthropic upstream tool_use response, delivered
// to a Chat client, must surface as tool_calls with finish_reason tool_calls.
func TestAnthropicResponseToChatClient(t *testing.T) {
	anthropicResp := mustJSON(t, map[string]any{
		"id": "msg_x", "type": "message", "role": "assistant", "model": "claude-sonnet",
		"content": []map[string]any{
			{"type": "text", "text": "let me check"},
			{"type": "tool_use", "id": "t1", "name": "get_weather", "input": map[string]any{"city": "Paris"}},
		},
		"stop_reason": "tool_use",
		"usage":       map[string]any{"input_tokens": 5, "output_tokens": 3},
	})
	// upstream decode (messages)
	iresp, err := msg.DecodeUpstreamResponse(anthropicResp)
	if err != nil {
		t.Fatalf("messages decode response: %v", err)
	}
	// ingress encode to chat client
	chatBody, err := chat.EncodeResponse(iresp)
	if err != nil {
		t.Fatalf("chat encode response: %v", err)
	}

	// decode the chat body the client would receive and assert tool_calls shape
	var got struct {
		Choices []struct {
			Message struct {
				Role      string `json:"role"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(chatBody, &got); err != nil {
		t.Fatalf("unmarshal chat body: %v", err)
	}
	if len(got.Choices) != 1 {
		t.Fatalf("choices = %+v", got.Choices)
	}
	ch := got.Choices[0]
	if ch.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q want tool_calls", ch.FinishReason)
	}
	if ch.Message.Content != "let me check" {
		t.Errorf("content = %q", ch.Message.Content)
	}
	if len(ch.Message.ToolCalls) != 1 || ch.Message.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("tool_calls = %+v", ch.Message.ToolCalls)
	}
	if ch.Message.ToolCalls[0].ID != "t1" {
		t.Errorf("tool call id = %q", ch.Message.ToolCalls[0].ID)
	}
}

// Chat upstream response -> Anthropic client: tool_calls must become tool_use blocks.
func TestChatResponseToAnthropicClient(t *testing.T) {
	chatResp := mustJSON(t, map[string]any{
		"id": "cc-1", "object": "chat.completion", "model": "gpt-4o",
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role": "assistant",
				"tool_calls": []map[string]any{
					{"id": "cx", "type": "function", "function": map[string]any{
						"name": "f", "arguments": `{"a":1}`}},
				},
			},
			"finish_reason": "tool_calls",
		}},
		"usage": map[string]any{"prompt_tokens": 2, "completion_tokens": 1, "total_tokens": 3},
	})
	iresp, err := chat.DecodeUpstreamResponse(chatResp)
	if err != nil {
		t.Fatalf("chat decode response: %v", err)
	}
	if iresp.StopReason != ir.StopToolUse {
		t.Fatalf("stop = %s", iresp.StopReason)
	}
	msgBody, err := msg.EncodeResponse(iresp)
	if err != nil {
		t.Fatalf("messages encode response: %v", err)
	}
	// decode the messages body back
	back, err := msg.DecodeUpstreamResponse(msgBody)
	if err != nil {
		t.Fatalf("messages decode back: %v", err)
	}
	if back.StopReason != ir.StopToolUse {
		t.Errorf("stop = %s", back.StopReason)
	}
	if len(back.Blocks) != 1 || back.Blocks[0].Type != ir.BlockToolUse ||
		back.Blocks[0].ToolCall.ID != "cx" || back.Blocks[0].ToolCall.Name != "f" {
		t.Errorf("tool_use block lost: %+v", back.Blocks)
	}
	if back.Usage.InputTokens != 2 || back.Usage.OutputTokens != 1 {
		t.Errorf("usage = %+v", back.Usage)
	}
}

// Responses request -> IR -> Messages upstream: a Codex-style Responses
// request (instructions + user message + assistant function_call + tool output)
// must survive translation into an Anthropic Messages body, with the tool-call
// id pairing and system prompt intact.
func TestResponsesRequestToMessagesUpstream(t *testing.T) {
	responsesReq := mustJSON(t, map[string]any{
		"model":        "gpt-4o",
		"instructions": "you are helpful",
		"input": []map[string]any{
			{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": "weather?"}}},
			{"type": "function_call", "call_id": "c1", "name": "get_weather", "arguments": `{"city":"Paris"}`},
			{"type": "function_call_output", "call_id": "c1", "output": "Paris 18C"},
		},
		"tools": []map[string]any{{"type": "function", "name": "get_weather", "parameters": map[string]any{"type": "object"}}},
	})

	ireq, err := resp.DecodeRequest(responsesReq, hdr)
	if err != nil {
		t.Fatalf("responses decode: %v", err)
	}
	if ireq.Model != "gpt-4o" {
		t.Errorf("model = %q", ireq.Model)
	}
	if len(ireq.System) != 1 || ireq.System[0].Text != "you are helpful" {
		t.Errorf("instructions -> system lost: %+v", ireq.System)
	}

	up, err := msg.BuildUpstream(ireq, "claude-real")
	if err != nil {
		t.Fatalf("messages build: %v", err)
	}
	back, err := msg.DecodeRequest(up.Body, hdr)
	if err != nil {
		t.Fatalf("messages decode of upstream body: %v", err)
	}
	if back.Model != "claude-real" {
		t.Errorf("model = %q", back.Model)
	}
	if len(back.System) != 1 || back.System[0].Text != "you are helpful" {
		t.Errorf("system lost across hop: %+v", back.System)
	}
	// assistant tool_use survived with id pairing
	var asst, res *ir.Message
	for i := range back.Messages {
		switch back.Messages[i].Role {
		case ir.RoleAssistant:
			asst = &back.Messages[i]
		case ir.RoleUser:
			if len(back.Messages[i].Blocks) > 0 && back.Messages[i].Blocks[0].Type == ir.BlockToolResult {
				res = &back.Messages[i]
			}
		}
	}
	if asst == nil || len(asst.Blocks) != 1 || asst.Blocks[0].Type != ir.BlockToolUse ||
		asst.Blocks[0].ToolCall.ID != "c1" || asst.Blocks[0].ToolCall.Name != "get_weather" {
		t.Errorf("assistant tool_use lost: %+v", asst)
	}
	if res == nil || len(res.Blocks) != 1 || res.Blocks[0].Type != ir.BlockToolResult ||
		res.Blocks[0].ToolResult.ToolUseID != "c1" {
		t.Errorf("tool result lost: %+v", res)
	}
}

// Chat upstream response -> Responses client: tool_calls must become a
// function_call output item on a Responses response object.
func TestChatResponseToResponsesClient(t *testing.T) {
	chatResp := mustJSON(t, map[string]any{
		"id": "cc-1", "object": "chat.completion", "model": "gpt-4o",
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role": "assistant",
				"content": "let me check",
				"tool_calls": []map[string]any{
					{"id": "cx", "type": "function", "function": map[string]any{"name": "f", "arguments": `{"a":1}`}},
				},
			},
			"finish_reason": "tool_calls",
		}},
		"usage": map[string]any{"prompt_tokens": 2, "completion_tokens": 1, "total_tokens": 3},
	})
	iresp, err := chat.DecodeUpstreamResponse(chatResp)
	if err != nil {
		t.Fatalf("chat decode response: %v", err)
	}
	if iresp.StopReason != ir.StopToolUse {
		t.Fatalf("stop = %s", iresp.StopReason)
	}
	respBody, err := resp.EncodeResponse(iresp)
	if err != nil {
		t.Fatalf("responses encode response: %v", err)
	}
	// decode the responses body back and assert the function_call item survived
	back, err := resp.DecodeUpstreamResponse(respBody)
	if err != nil {
		t.Fatalf("responses decode back: %v", err)
	}
	if back.StopReason != ir.StopToolUse {
		t.Errorf("stop = %s", back.StopReason)
	}
	var fc, txt *ir.Block
	for i := range back.Blocks {
		switch back.Blocks[i].Type {
		case ir.BlockToolUse:
			fc = &back.Blocks[i]
		case ir.BlockText:
			txt = &back.Blocks[i]
		}
	}
	if txt == nil || txt.Text != "let me check" {
		t.Errorf("assistant text lost: %+v", txt)
	}
	if fc == nil || fc.ToolCall.ID != "cx" || fc.ToolCall.Name != "f" {
		t.Errorf("function_call item lost: %+v", fc)
	}
	if back.Usage.InputTokens != 2 || back.Usage.OutputTokens != 1 {
		t.Errorf("usage = %+v", back.Usage)
	}
}

// Responses within-protocol round trip: decode a Responses request, build a
// Responses upstream body, decode it again — system/tools/tool-call pairing
// must be lossless (the "no information lost on the hop" property, §3.2 table).
func TestResponsesRequestRoundTripLossless(t *testing.T) {
	responsesReq := mustJSON(t, map[string]any{
		"model": "gpt-4o",
		"input": []map[string]any{
			{"type": "message", "role": "user", "content": "hi"},
			{"type": "function_call", "call_id": "k1", "name": "search", "arguments": `{"q":"x"}`},
			{"type": "function_call_output", "call_id": "k1", "output": "found"},
		},
		"tools": []map[string]any{{"type": "function", "name": "search", "parameters": map[string]any{"type": "object"}}},
	})
	ireq, err := resp.DecodeRequest(responsesReq, hdr)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	up, err := resp.BuildUpstream(ireq, "gpt-4o-mini")
	if err != nil {
		t.Fatalf("build upstream: %v", err)
	}
	if up.Path != "/v1/responses" {
		t.Errorf("upstream path = %q", up.Path)
	}
	back, err := resp.DecodeRequest(up.Body, hdr)
	if err != nil {
		t.Fatalf("decode back: %v", err)
	}
	if back.Model != "gpt-4o-mini" {
		t.Errorf("model = %q", back.Model)
	}
	if len(back.Tools) != 1 || back.Tools[0].Name != "search" {
		t.Errorf("tools lost: %+v", back.Tools)
	}
	// user message + assistant function_call + user function_call_output survive
	var hasUserText, hasToolUse, hasToolResult bool
	for _, m := range back.Messages {
		for _, b := range m.Blocks {
			switch b.Type {
			case ir.BlockText:
				if m.Role == ir.RoleUser {
					hasUserText = true
				}
			case ir.BlockToolUse:
				hasToolUse = b.ToolCall.ID == "k1" && b.ToolCall.Name == "search"
			case ir.BlockToolResult:
				hasToolResult = b.ToolResult.ToolUseID == "k1"
			}
		}
	}
	if !hasUserText {
		t.Error("user text lost on round trip")
	}
	if !hasToolUse {
		t.Error("function_call lost on round trip")
	}
	if !hasToolResult {
		t.Error("function_call_output lost on round trip")
	}
}

// Registry wires the adapters and resolves by path (sanity for the dispatch layer).
func TestRegistryByPath(t *testing.T) {
	r := adapter.NewRegistry(chat, msg, resp)
	for _, tc := range []struct {
		path string
		want adapter.Protocol
	}{
		{"/v1/chat/completions", adapter.ProtocolChat},
		{"/v1/messages", adapter.ProtocolMessages},
		{"/v1/messages?beta=true", adapter.ProtocolMessages},
		{"/v1/responses", adapter.ProtocolResponses},
	} {
		got, ok := adapter.ByPath(tc.path)
		if !ok || got != tc.want {
			t.Errorf("ByPath(%q) = %q,%v want %q", tc.path, got, ok, tc.want)
		}
	}
	for _, p := range []adapter.Protocol{adapter.ProtocolChat, adapter.ProtocolMessages, adapter.ProtocolResponses} {
		if _, ok := r.Get(p); !ok {
			t.Errorf("adapter %q not registered", p)
		}
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

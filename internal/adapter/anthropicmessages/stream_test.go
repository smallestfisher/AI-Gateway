package anthropicmessages

import (
	"strings"
	"testing"

	"github.com/aigateway/ai-hub/internal/ir"
)

func collect(t *testing.T, enc *streamEnc, evs ...ir.StreamEvent) string {
	t.Helper()
	var sb strings.Builder
	for _, e := range evs {
		b, err := enc.Encode(e)
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(b)
	}
	return sb.String()
}

func TestStreamEncoder_TextAndStop(t *testing.T) {
	out := collect(t, &streamEnc{},
		ir.StreamEvent{Type: ir.EvStart, ResponseID: "msg_1", Model: "claude"},
		ir.StreamEvent{Type: ir.EvBlockStart, Index: 0, Block: &ir.Block{Type: ir.BlockText}},
		ir.StreamEvent{Type: ir.EvBlockDelta, Index: 0, Delta: &ir.BlockDelta{Type: ir.DeltaText, Text: "Hi"}},
		ir.StreamEvent{Type: ir.EvBlockStop, Index: 0},
		ir.StreamEvent{Type: ir.EvMessageDelta, StopReason: ir.StopEndTurn, Usage: &ir.Usage{OutputTokens: 2}},
		ir.StreamEvent{Type: ir.EvStop},
	)
	mustContain := []string{
		"event: message_start",
		`"id":"msg_1"`,
		"event: content_block_start",
		"event: content_block_delta",
		`"text_delta"`,
		`"text":"Hi"`,
		"event: content_block_stop",
		"event: message_delta",
		`"stop_reason":"end_turn"`,
		`"output_tokens":2`,
		"event: message_stop",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q in output:\n%s", s, out)
		}
	}
}

func TestStreamEncoder_ToolUse(t *testing.T) {
	out := collect(t, &streamEnc{},
		ir.StreamEvent{Type: ir.EvStart, Model: "claude"},
		ir.StreamEvent{Type: ir.EvBlockStart, Index: 0, Block: &ir.Block{Type: ir.BlockToolUse,
			ToolCall: &ir.ToolCall{ID: "t1", Name: "f"}}},
		ir.StreamEvent{Type: ir.EvBlockDelta, Index: 0, Delta: &ir.BlockDelta{Type: ir.DeltaInputJSON, PartialJSON: `{"a":1}`}},
		ir.StreamEvent{Type: ir.EvBlockStop, Index: 0},
		ir.StreamEvent{Type: ir.EvMessageDelta, StopReason: ir.StopToolUse},
		ir.StreamEvent{Type: ir.EvStop},
	)
	if !strings.Contains(out, `"type":"tool_use"`) {
		t.Errorf("missing tool_use block start:\n%s", out)
	}
	if !strings.Contains(out, `"input_json_delta"`) || !strings.Contains(out, `\"a\":1`) {
		t.Errorf("missing input_json_delta (partial_json is JSON-escaped):\n%s", out)
	}
	if !strings.Contains(out, `"stop_reason":"tool_use"`) {
		t.Errorf("missing stop_reason tool_use:\n%s", out)
	}
}

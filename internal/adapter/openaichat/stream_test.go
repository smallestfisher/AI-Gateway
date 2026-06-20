package openaichat

import (
	"encoding/json"
	"testing"

	"github.com/aigateway/ai-hub/internal/ir"
)

func chunkJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Feed a sequence of Chat SSE payloads through the decoder and collect events.
func feedAll(t *testing.T, dec *streamDec, payloads ...[]byte) []ir.StreamEvent {
	t.Helper()
	var out []ir.StreamEvent
	for _, p := range payloads {
		evs, err := dec.Feed(p)
		if err != nil {
			t.Fatalf("feed: %v", err)
		}
		out = append(out, evs...)
	}
	return out
}

func TestStreamDecoder_TextOnly(t *testing.T) {
	dec := &streamDec{}
	evs := feedAll(t, dec,
		chunkJSON(t, map[string]any{
			"id": "c1", "model": "gpt-4o",
			"choices": []map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant"}}},
		}),
		chunkJSON(t, map[string]any{"choices": []map[string]any{{"delta": map[string]any{"content": "Hel"}}}}),
		chunkJSON(t, map[string]any{"choices": []map[string]any{{"delta": map[string]any{"content": "lo"}}}}),
		chunkJSON(t, map[string]any{"choices": []map[string]any{{"delta": map[string]any{}, "finish_reason": "stop"}}}),
	)
	final, err := dec.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	evs = append(evs, final...)

	// expect: start, block_start(text), 2x text delta, block_stop, message_delta(end_turn), stop
	if evs[0].Type != ir.EvStart || evs[0].Model != "gpt-4o" {
		t.Errorf("start = %+v", evs[0])
	}
	// gather text
	var text string
	for _, e := range evs {
		if e.Type == ir.EvBlockDelta && e.Delta.Type == ir.DeltaText {
			text += e.Delta.Text
		}
	}
	if text != "Hello" {
		t.Errorf("text = %q", text)
	}
	// last two: message_delta + stop
	if evs[len(evs)-2].Type != ir.EvMessageDelta || evs[len(evs)-2].StopReason != ir.StopEndTurn {
		t.Errorf("message_delta = %+v", evs[len(evs)-2])
	}
	if evs[len(evs)-1].Type != ir.EvStop {
		t.Errorf("last = %+v", evs[len(evs)-1])
	}
}

func TestStreamDecoder_ToolCallInputJSON(t *testing.T) {
	dec := &streamDec{}
	evs := feedAll(t, dec,
		chunkJSON(t, map[string]any{"model": "gpt-4o", "choices": []map[string]any{{"delta": map[string]any{"role": "assistant"}}}}),
		chunkJSON(t, map[string]any{"choices": []map[string]any{{"delta": map[string]any{
			"tool_calls": []map[string]any{{"index": 0, "id": "call_1", "type": "function",
				"function": map[string]any{"name": "get_weather", "arguments": ""}}}}}}}),
		chunkJSON(t, map[string]any{"choices": []map[string]any{{"delta": map[string]any{
			"tool_calls": []map[string]any{{"index": 0, "function": map[string]any{"arguments": `{"city":"Paris"}`}}}}}}}),
		chunkJSON(t, map[string]any{"choices": []map[string]any{{"delta": map[string]any{}, "finish_reason": "tool_calls"}}}),
		chunkJSON(t, map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 1, "total_tokens": 4}}),
	)
	final, _ := dec.Finalize()
	evs = append(evs, final...)

	// find tool_use block_start
	var foundStart bool
	var partial string
	for _, e := range evs {
		if e.Type == ir.EvBlockStart && e.Block != nil && e.Block.Type == ir.BlockToolUse {
			foundStart = true
			if e.Block.ToolCall.Name != "get_weather" || e.Block.ToolCall.ID != "call_1" {
				t.Errorf("tool block = %+v", e.Block)
			}
		}
		if e.Type == ir.EvBlockDelta && e.Delta != nil && e.Delta.Type == ir.DeltaInputJSON {
			partial += e.Delta.PartialJSON
		}
	}
	if !foundStart {
		t.Error("no tool_use block_start")
	}
	if partial != `{"city":"Paris"}` {
		t.Errorf("input_json = %q", partial)
	}
	// stop reason tool_use + usage
	var msgDelta *ir.StreamEvent
	for i := range evs {
		if evs[i].Type == ir.EvMessageDelta {
			msgDelta = &evs[i]
		}
	}
	if msgDelta == nil || msgDelta.StopReason != ir.StopToolUse {
		t.Errorf("message_delta = %+v", msgDelta)
	}
	if msgDelta.Usage == nil || msgDelta.Usage.InputTokens != 3 {
		t.Errorf("usage = %+v", msgDelta.Usage)
	}
}

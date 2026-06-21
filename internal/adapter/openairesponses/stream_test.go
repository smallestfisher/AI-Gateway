package openairesponses

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/aigateway/ai-hub/internal/ir"
)

// feed feeds each JSON payload (one per upstream SSE event, as the egress
// line-buffer delivers them) into the decoder and returns the accumulated IR
// events. Finalize is NOT called — tests call it explicitly when needed.
func feed(t *testing.T, dec interface {
	Feed([]byte) ([]ir.StreamEvent, error)
}, payloads ...string,
) []ir.StreamEvent {
	t.Helper()
	var out []ir.StreamEvent
	for _, p := range payloads {
		evs, err := dec.Feed([]byte(p))
		if err != nil {
			t.Fatalf("Feed error on %s: %v", p, err)
		}
		out = append(out, evs...)
	}
	return out
}

// ---------------------------------------------------------------------------
// Decoder: upstream Responses SSE -> IR events
// ---------------------------------------------------------------------------

// Text-only stream: created -> message item -> two text deltas -> item done
// -> completed. Must yield a clean text-block lifecycle + end_turn + usage.
func TestStreamDec_TextOnly(t *testing.T) {
	dec := New().NewStreamDecoder()
	evs := feed(t, dec,
		`{"type":"response.created","response":{"id":"resp_1","model":"gpt-4o","status":"in_progress"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","status":"in_progress","role":"assistant","content":[]}}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"Hello"}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":" world"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello world"}]}}`,
		`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-4o","status":"completed","usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}}`,
	)
	fin, err := dec.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	evs = append(evs, fin...)

	want := []ir.StreamEvent{
		{Type: ir.EvStart, ResponseID: "resp_1", Model: "gpt-4o"},
		{Type: ir.EvBlockStart, Index: 0, Block: &ir.Block{Type: ir.BlockText}},
		{Type: ir.EvBlockDelta, Index: 0, Delta: &ir.BlockDelta{Type: ir.DeltaText, Text: "Hello"}},
		{Type: ir.EvBlockDelta, Index: 0, Delta: &ir.BlockDelta{Type: ir.DeltaText, Text: " world"}},
		{Type: ir.EvBlockStop, Index: 0},
		{Type: ir.EvMessageDelta, StopReason: ir.StopEndTurn, Usage: &ir.Usage{InputTokens: 5, OutputTokens: 2}},
		{Type: ir.EvStop},
	}
	assertEvents(t, evs, want)
}

// Function call with arguments split across two deltas must reassemble into
// input_json deltas on a tool_use block, and stop_reason must be tool_use.
func TestStreamDec_FunctionCallSplitArgs(t *testing.T) {
	dec := New().NewStreamDecoder()
	evs := feed(t, dec,
		`{"type":"response.created","response":{"id":"resp_2","model":"gpt-4o","status":"in_progress"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","status":"in_progress","name":"get_weather","call_id":"call_abc","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_1","delta":"{\"city\""}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_1","delta":"\":\"Paris\"}"}`,
		`{"type":"response.function_call_arguments.done","output_index":0,"item_id":"fc_1","arguments":"{\"city\":\"Paris\"}"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"fc_1","type":"function_call","status":"completed","name":"get_weather","call_id":"call_abc","arguments":"{\"city\":\"Paris\"}"}}`,
		`{"type":"response.completed","response":{"id":"resp_2","model":"gpt-4o","status":"completed","usage":{"input_tokens":3,"output_tokens":1}}}`,
	)
	fin, _ := dec.Finalize()
	evs = append(evs, fin...)

	want := []ir.StreamEvent{
		{Type: ir.EvStart, ResponseID: "resp_2", Model: "gpt-4o"},
		{Type: ir.EvBlockStart, Index: 0, Block: &ir.Block{Type: ir.BlockToolUse, ID: "call_abc", ToolCall: &ir.ToolCall{ID: "call_abc", Name: "get_weather"}}},
		{Type: ir.EvBlockDelta, Index: 0, Delta: &ir.BlockDelta{Type: ir.DeltaInputJSON, PartialJSON: `{"city"`}},
		{Type: ir.EvBlockDelta, Index: 0, Delta: &ir.BlockDelta{Type: ir.DeltaInputJSON, PartialJSON: `":"Paris"}`}},
		{Type: ir.EvBlockStop, Index: 0},
		{Type: ir.EvMessageDelta, StopReason: ir.StopToolUse, Usage: &ir.Usage{InputTokens: 3, OutputTokens: 1}},
		{Type: ir.EvStop},
	}
	assertEvents(t, evs, want)
}

// Text then tool call across two output items: indices must be sequential and
// the text block closed before the tool block opens.
func TestStreamDec_TextThenToolCall(t *testing.T) {
	dec := New().NewStreamDecoder()
	evs := feed(t, dec,
		`{"type":"response.created","response":{"id":"resp_3","model":"gpt-4o","status":"in_progress"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","status":"in_progress","role":"assistant","content":[]}}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"let me check"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"let me check"}]}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"id":"fc_1","type":"function_call","status":"in_progress","name":"search","call_id":"call_xyz","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"item_id":"fc_1","delta":"{\"q\":\"x\"}"}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"id":"fc_1","type":"function_call","status":"completed","name":"search","call_id":"call_xyz","arguments":"{\"q\":\"x\"}"}}`,
		`{"type":"response.completed","response":{"id":"resp_3","model":"gpt-4o","status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`,
	)
	fin, _ := dec.Finalize()
	evs = append(evs, fin...)

	want := []ir.StreamEvent{
		{Type: ir.EvStart, ResponseID: "resp_3", Model: "gpt-4o"},
		{Type: ir.EvBlockStart, Index: 0, Block: &ir.Block{Type: ir.BlockText}},
		{Type: ir.EvBlockDelta, Index: 0, Delta: &ir.BlockDelta{Type: ir.DeltaText, Text: "let me check"}},
		{Type: ir.EvBlockStop, Index: 0},
		{Type: ir.EvBlockStart, Index: 1, Block: &ir.Block{Type: ir.BlockToolUse, ID: "call_xyz", ToolCall: &ir.ToolCall{ID: "call_xyz", Name: "search"}}},
		{Type: ir.EvBlockDelta, Index: 1, Delta: &ir.BlockDelta{Type: ir.DeltaInputJSON, PartialJSON: `{"q":"x"}`}},
		{Type: ir.EvBlockStop, Index: 1},
		{Type: ir.EvMessageDelta, StopReason: ir.StopToolUse, Usage: &ir.Usage{InputTokens: 1, OutputTokens: 1}},
		{Type: ir.EvStop},
	}
	assertEvents(t, evs, want)
}

// Reasoning summary streaming must map to a reasoning block with text deltas.
func TestStreamDec_Reasoning(t *testing.T) {
	dec := New().NewStreamDecoder()
	evs := feed(t, dec,
		`{"type":"response.created","response":{"id":"resp_4","model":"o3","status":"in_progress"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"reasoning","status":"in_progress","summary":[]}}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"item_id":"rs_1","delta":"Thinking"}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"item_id":"rs_1","delta":"..."}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"rs_1","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":"Thinking..."}]}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"id":"msg_1","type":"message","status":"in_progress","role":"assistant","content":[]}}`,
		`{"type":"response.output_text.delta","output_index":1,"content_index":0,"delta":"answer"}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"answer"}]}}`,
		`{"type":"response.completed","response":{"id":"resp_4","model":"o3","status":"completed","usage":{"input_tokens":2,"output_tokens":2,"output_tokens_details":{"reasoning_tokens":3}}}}`,
	)
	fin, _ := dec.Finalize()
	evs = append(evs, fin...)

	want := []ir.StreamEvent{
		{Type: ir.EvStart, ResponseID: "resp_4", Model: "o3"},
		{Type: ir.EvBlockStart, Index: 0, Block: &ir.Block{Type: ir.BlockReasoning}},
		{Type: ir.EvBlockDelta, Index: 0, Delta: &ir.BlockDelta{Type: ir.DeltaText, Text: "Thinking"}},
		{Type: ir.EvBlockDelta, Index: 0, Delta: &ir.BlockDelta{Type: ir.DeltaText, Text: "..."}},
		{Type: ir.EvBlockStop, Index: 0},
		{Type: ir.EvBlockStart, Index: 1, Block: &ir.Block{Type: ir.BlockText}},
		{Type: ir.EvBlockDelta, Index: 1, Delta: &ir.BlockDelta{Type: ir.DeltaText, Text: "answer"}},
		{Type: ir.EvBlockStop, Index: 1},
		{Type: ir.EvMessageDelta, StopReason: ir.StopEndTurn, Usage: &ir.Usage{InputTokens: 2, OutputTokens: 2, ReasoningTokens: 3}},
		{Type: ir.EvStop},
	}
	assertEvents(t, evs, want)
}

// An incomplete response (status incomplete / max_output_tokens) maps to
// StopMaxTokens, even with a trailing text item.
func TestStreamDec_IncompleteToMaxTokens(t *testing.T) {
	dec := New().NewStreamDecoder()
	evs := feed(t, dec,
		`{"type":"response.created","response":{"id":"resp_5","model":"gpt-4o","status":"in_progress"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","status":"in_progress","role":"assistant","content":[]}}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"cut off"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","status":"incomplete","role":"assistant","content":[{"type":"output_text","text":"cut off"}]}}`,
		`{"type":"response.completed","response":{"id":"resp_5","model":"gpt-4o","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":1,"output_tokens":1}}}`,
	)
	fin, _ := dec.Finalize()
	evs = append(evs, fin...)

	// find the message_delta and assert its stop reason
	var stop ir.StopReason
	for _, e := range evs {
		if e.Type == ir.EvMessageDelta {
			stop = e.StopReason
		}
	}
	if stop != ir.StopMaxTokens {
		t.Errorf("stop = %s, want max_tokens", stop)
	}
	// ends with stop
	if len(evs) == 0 || evs[len(evs)-1].Type != ir.EvStop {
		t.Errorf("stream did not end with EvStop: %+v", evs)
	}
}

// A response.failed event surfaces as an EvError.
func TestStreamDec_Failure(t *testing.T) {
	dec := New().NewStreamDecoder()
	evs := feed(t, dec,
		`{"type":"response.created","response":{"id":"resp_6","model":"gpt-4o","status":"in_progress"}}`,
		`{"type":"response.failed","response":{"id":"resp_6","status":"failed","error":{"code":"rate_limit_exceeded","message":"slow down"}}}`,
	)
	var gotErr *ir.StreamError
	for _, e := range evs {
		if e.Type == ir.EvError {
			gotErr = e.Error
		}
	}
	if gotErr == nil {
		t.Fatalf("no EvError emitted: %+v", evs)
	}
	if gotErr.Code != "rate_limit_exceeded" || gotErr.Message != "slow down" {
		t.Errorf("error = %+v", gotErr)
	}
}

// assertEvents compares event slices by value. Deltas/Blocks are pointers; we
// deref-compare so tests read as plain structs.
func assertEvents(t *testing.T, got, want []ir.StreamEvent) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("event count = %d, want %d\nGOT:\n%s\nWANT:\n%s", len(got), len(want), dumpEvents(got), dumpEvents(want))
	}
	for i := range got {
		if !sameEvent(got[i], want[i]) {
			t.Errorf("event %d mismatch:\n GOT: %s\nWANT: %s", i, dumpEvent(got[i]), dumpEvent(want[i]))
		}
	}
}

func sameEvent(a, b ir.StreamEvent) bool {
	return reflect.DeepEqual(derefEvent(a), derefEvent(b))
}

// derefEvent returns a copy with pointer fields dereferenced for stable
// DeepEqual (nil vs zero-value distinction is irrelevant for these tests).
func derefEvent(e ir.StreamEvent) ir.StreamEvent {
	if e.Block != nil {
		b := *e.Block
		e.Block = &b
	}
	if e.Delta != nil {
		d := *e.Delta
		e.Delta = &d
	}
	if e.Usage != nil {
		u := *e.Usage
		e.Usage = &u
	}
	return e
}

func dumpEvents(evs []ir.StreamEvent) string {
	var s string
	for _, e := range evs {
		s += "  " + dumpEvent(e) + "\n"
	}
	return s
}

func dumpEvent(e ir.StreamEvent) string {
	d := derefEvent(e)
	return string(d.Type) + " idx=" + itoa(e.Index) +
		" rid=" + e.ResponseID + " model=" + e.Model +
		" stop=" + string(e.StopReason) +
		blkStr(e.Block) + deltaStr(e.Delta) + usageStr(e.Usage) + errStr(e.Error)
}

func blkStr(b *ir.Block) string {
	if b == nil {
		return ""
	}
	s := " blk{type=" + string(b.Type)
	if b.ToolCall != nil {
		s += " tool=" + b.ToolCall.ID + "/" + b.ToolCall.Name
	}
	return s + "}"
}
func deltaStr(d *ir.BlockDelta) string {
	if d == nil {
		return ""
	}
	return " delta{" + string(d.Type) + "=" + d.Text + d.PartialJSON + "}"
}
func usageStr(u *ir.Usage) string {
	if u == nil {
		return ""
	}
	return " usage{in=" + itoa(u.InputTokens) + ",out=" + itoa(u.OutputTokens) + ",reason=" + itoa(u.ReasoningTokens) + "}"
}
func errStr(e *ir.StreamError) string {
	if e == nil {
		return ""
	}
	return " err{" + e.Code + ":" + e.Message + "}"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// ---------------------------------------------------------------------------
// Encoder: IR events -> client Responses SSE
// ---------------------------------------------------------------------------

// encodeAll runs each event through the encoder (plus Flush) and returns the
// parsed JSON payload of every emitted SSE frame, in order.
func encodeAll(t *testing.T, enc interface {
	Encode(ir.StreamEvent) ([]byte, error)
	Flush() ([]byte, error)
}, evs ...ir.StreamEvent,
) []map[string]any {
	t.Helper()
	var raw []byte
	for _, ev := range evs {
		b, err := enc.Encode(ev)
		if err != nil {
			t.Fatalf("Encode(%s): %v", ev.Type, err)
		}
		raw = append(raw, b...)
	}
	if b, err := enc.Flush(); err == nil {
		raw = append(raw, b...)
	}
	var frames []map[string]any
	for _, part := range strings.Split(string(raw), "\n\n") {
		for _, line := range strings.Split(part, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" || payload == "[DONE]" {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(payload), &m); err != nil {
				t.Fatalf("invalid frame JSON %q: %v", payload, err)
			}
			frames = append(frames, m)
		}
	}
	return frames
}

func frameTypes(frames []map[string]any) []string {
	out := make([]string, 0, len(frames))
	for _, f := range frames {
		out = append(out, f["type"].(string))
	}
	return out
}

func byType(frames []map[string]any, typ string) []map[string]any {
	var out []map[string]any
	for _, f := range frames {
		if f["type"] == typ {
			out = append(out, f)
		}
	}
	return out
}

func assertStringSlice(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("frame types:\n GOT: %v\nWANT: %v", got, want)
	}
}

// Text-only: start -> text block (2 deltas) -> stop must produce the full
// Responses lifecycle, with response.completed carrying usage.
func TestStreamEnc_TextOnly(t *testing.T) {
	enc := New().NewStreamEncoder()
	frames := encodeAll(t, enc,
		ir.StreamEvent{Type: ir.EvStart, ResponseID: "resp_1", Model: "gpt-4o"},
		ir.StreamEvent{Type: ir.EvBlockStart, Index: 0, Block: &ir.Block{Type: ir.BlockText}},
		ir.StreamEvent{Type: ir.EvBlockDelta, Index: 0, Delta: &ir.BlockDelta{Type: ir.DeltaText, Text: "Hello"}},
		ir.StreamEvent{Type: ir.EvBlockDelta, Index: 0, Delta: &ir.BlockDelta{Type: ir.DeltaText, Text: " world"}},
		ir.StreamEvent{Type: ir.EvBlockStop, Index: 0},
		ir.StreamEvent{Type: ir.EvMessageDelta, StopReason: ir.StopEndTurn, Usage: &ir.Usage{InputTokens: 5, OutputTokens: 2}},
		ir.StreamEvent{Type: ir.EvStop},
	)
	assertStringSlice(t, frameTypes(frames), []string{
		"response.created",
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
		"response.output_text.delta",
		"response.output_text.done",
		"response.content_part.done",
		"response.output_item.done",
		"response.completed",
	})
	deltas := byType(frames, "response.output_text.delta")
	if len(deltas) != 2 || deltas[0]["delta"] != "Hello" || deltas[1]["delta"] != " world" {
		t.Errorf("text deltas = %+v", deltas)
	}
	done := byType(frames, "response.output_text.done")
	if len(done) != 1 || done[0]["text"] != "Hello world" {
		t.Errorf("output_text.done = %+v", done)
	}
	comp := byType(frames, "response.completed")[0]
	resp := comp["response"].(map[string]any)
	if resp["status"] != "completed" {
		t.Errorf("status = %v", resp["status"])
	}
	usage := resp["usage"].(map[string]any)
	if usage["input_tokens"] != float64(5) || usage["output_tokens"] != float64(2) {
		t.Errorf("usage = %+v", usage)
	}
}

// Tool call: arguments split across deltas must reassemble and the item.done
// item carries the full arguments. Stop maps the response into a function_call.
func TestStreamEnc_FunctionCall(t *testing.T) {
	enc := New().NewStreamEncoder()
	frames := encodeAll(t, enc,
		ir.StreamEvent{Type: ir.EvStart, ResponseID: "resp_2", Model: "gpt-4o"},
		ir.StreamEvent{Type: ir.EvBlockStart, Index: 0, Block: &ir.Block{Type: ir.BlockToolUse, ID: "call_abc", ToolCall: &ir.ToolCall{ID: "call_abc", Name: "get_weather"}}},
		ir.StreamEvent{Type: ir.EvBlockDelta, Index: 0, Delta: &ir.BlockDelta{Type: ir.DeltaInputJSON, PartialJSON: `{"city":`}},
		ir.StreamEvent{Type: ir.EvBlockDelta, Index: 0, Delta: &ir.BlockDelta{Type: ir.DeltaInputJSON, PartialJSON: `"Paris"}`}},
		ir.StreamEvent{Type: ir.EvBlockStop, Index: 0},
		ir.StreamEvent{Type: ir.EvMessageDelta, StopReason: ir.StopToolUse, Usage: &ir.Usage{InputTokens: 3, OutputTokens: 1}},
		ir.StreamEvent{Type: ir.EvStop},
	)
	assertStringSlice(t, frameTypes(frames), []string{
		"response.created",
		"response.output_item.added",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.output_item.done",
		"response.completed",
	})
	added := byType(frames, "response.output_item.added")[0]
	item := added["item"].(map[string]any)
	if item["type"] != "function_call" || item["name"] != "get_weather" || item["call_id"] != "call_abc" {
		t.Errorf("added item = %+v", item)
	}
	argDone := byType(frames, "response.function_call_arguments.done")[0]
	if argDone["arguments"] != `{"city":"Paris"}` {
		t.Errorf("arguments = %v", argDone["arguments"])
	}
}

// Round-trip: feed the decoder a realistic mixed stream, pipe its IR events
// straight into the encoder, and assert the reconstructed response carries the
// text + tool items and usage. This is the streaming analogue of the
// cross-protocol lossless-round-trip property.
func TestStreamRoundTrip_DecoderToEncoder(t *testing.T) {
	dec := New().NewStreamDecoder()
	var evs []ir.StreamEvent
	for _, p := range []string{
		`{"type":"response.created","response":{"id":"resp_rt","model":"gpt-4o","status":"in_progress"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","status":"in_progress","role":"assistant","content":[]}}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"Hi "}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"there"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hi there"}]}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"id":"fc_1","type":"function_call","status":"in_progress","name":"run","call_id":"call_z","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"item_id":"fc_1","delta":"{\"x\":1}"}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"id":"fc_1","type":"function_call","status":"completed","name":"run","call_id":"call_z","arguments":"{\"x\":1}"}}`,
		`{"type":"response.completed","response":{"id":"resp_rt","model":"gpt-4o","status":"completed","usage":{"input_tokens":4,"output_tokens":2}}}`,
	} {
		out, err := dec.Feed([]byte(p))
		if err != nil {
			t.Fatalf("Feed: %v", err)
		}
		evs = append(evs, out...)
	}
	fin, _ := dec.Finalize()
	evs = append(evs, fin...)

	enc := New().NewStreamEncoder()
	frames := encodeAll(t, enc, evs...)
	comp := byType(frames, "response.completed")
	if len(comp) != 1 {
		t.Fatalf("want 1 response.completed, got %d", len(comp))
	}
	resp := comp[0]["response"].(map[string]any)
	out := resp["output"].([]any)
	if len(out) != 2 {
		t.Fatalf("output items = %+v", out)
	}
	msg := out[0].(map[string]any)
	if msg["type"] != "message" {
		t.Errorf("item0 type = %v", msg["type"])
	}
	content := msg["content"].([]any)
	if content[0].(map[string]any)["text"] != "Hi there" {
		t.Errorf("message text lost: %+v", content)
	}
	fc := out[1].(map[string]any)
	if fc["type"] != "function_call" || fc["name"] != "run" || fc["call_id"] != "call_z" || fc["arguments"] != `{"x":1}` {
		t.Errorf("function_call item = %+v", fc)
	}
	usage := resp["usage"].(map[string]any)
	if usage["input_tokens"] != float64(4) || usage["output_tokens"] != float64(2) {
		t.Errorf("usage = %+v", usage)
	}
}

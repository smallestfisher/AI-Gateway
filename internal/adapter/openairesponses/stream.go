package openairesponses

import (
	crand "crypto/rand"
	"encoding/json"
	"sort"
	"strings"

	"github.com/aigateway/ai-hub/internal/ir"
)

// randHex returns n lowercase hex chars.
func randHex(n int) string {
	b := make([]byte, (n+1)/2)
	_, _ = crand.Read(b)
	const hexd = "0123456789abcdef"
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		v := b[i/2]
		if i%2 == 0 {
			out[i] = hexd[v>>4]
		} else {
			out[i] = hexd[v&0x0f]
		}
	}
	return string(out)
}

// ---------------------------------------------------------------------------
// Encoder: IR events -> client Responses SSE (docs/07-streaming.md §2.3)
//
// Mirrors the decoder: IR block-lifecycle events are expanded into Responses'
// item-oriented stream. The server drives the encoder via Encode(ev) and never
// calls Flush, so response.completed (the terminator) is emitted on EvStop.
// ---------------------------------------------------------------------------

// sseFrame emits one Responses SSE frame: an `event:` line plus the JSON
// `data:` payload (which always echoes `type`, the field clients key off).
func sseFrame(eventType string, payload any) ([]byte, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	out := append([]byte("event: "), []byte(eventType)...)
	out = append(out, '\n')
	out = append(out, []byte("data: ")...)
	out = append(out, b...)
	out = append(out, '\n', '\n')
	return out, nil
}

type encItem struct {
	outputIndex int
	typ         string // message | function_call | reasoning
	itemID      string // msg_/fc_/rs_ + hex
	callID      string // function_call only
	name        string // function_call only
	acc         strings.Builder
}

type streamEnc struct {
	respID  string
	model   string
	status  string
	nextOut int
	blocks  map[int]*encItem // IR block index -> item
	usage   *ir.Usage
}

func (e *streamEnc) Encode(ev ir.StreamEvent) ([]byte, error) {
	if e.blocks == nil {
		e.blocks = map[int]*encItem{}
	}
	switch ev.Type {
	case ir.EvStart:
		e.respID = orDefault(ev.ResponseID, "resp_"+randHex(12))
		e.model = orDefault(ev.Model, "")
		e.status = "in_progress"
		return sseFrame("response.created", map[string]any{
			"type": "response.created",
			"response": map[string]any{
				"id": e.respID, "object": "response", "status": "in_progress",
				"model": e.model, "output": []any{},
			},
		})
	case ir.EvBlockStart:
		return e.blockStart(ev)
	case ir.EvBlockDelta:
		return e.blockDelta(ev)
	case ir.EvBlockStop:
		return e.blockStop(ev)
	case ir.EvMessageDelta:
		if ev.Usage != nil {
			u := *ev.Usage
			e.usage = &u
		}
		e.status = "completed"
		if ev.StopReason == ir.StopMaxTokens {
			e.status = "incomplete"
		}
		return nil, nil // Responses has no mid-stream message_delta; usage rides on completed
	case ir.EvStop:
		return e.emitCompleted()
	case ir.EvError:
		return e.emitFailed(ev)
	}
	return nil, nil
}

func (e *streamEnc) blockStart(ev ir.StreamEvent) ([]byte, error) {
	if ev.Block == nil {
		return nil, nil
	}
	idx := e.nextOut
	e.nextOut++
	switch ev.Block.Type {
	case ir.BlockToolUse:
		callID, name := "", ""
		if ev.Block.ToolCall != nil {
			callID, name = ev.Block.ToolCall.ID, ev.Block.ToolCall.Name
		}
		if callID == "" {
			callID = "call_" + randHex(12)
		}
		it := &encItem{outputIndex: idx, typ: "function_call", itemID: "fc_" + randHex(12), callID: callID, name: name}
		e.blocks[ev.Index] = it
		return sseFrame("response.output_item.added", map[string]any{
			"type": "response.output_item.added", "output_index": idx,
			"item": map[string]any{
				"id": it.itemID, "type": "function_call", "status": "in_progress",
				"name": name, "call_id": callID, "arguments": "",
			},
		})
	case ir.BlockReasoning:
		it := &encItem{outputIndex: idx, typ: "reasoning", itemID: "rs_" + randHex(12)}
		e.blocks[ev.Index] = it
		return sseFrame("response.output_item.added", map[string]any{
			"type": "response.output_item.added", "output_index": idx,
			"item": map[string]any{"id": it.itemID, "type": "reasoning", "status": "in_progress", "summary": []any{}},
		})
	default: // BlockText
		it := &encItem{outputIndex: idx, typ: "message", itemID: "msg_" + randHex(12)}
		e.blocks[ev.Index] = it
		added, _ := sseFrame("response.output_item.added", map[string]any{
			"type": "response.output_item.added", "output_index": idx,
			"item": map[string]any{"id": it.itemID, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}},
		})
		part, _ := sseFrame("response.content_part.added", map[string]any{
			"type": "response.content_part.added", "output_index": idx, "content_index": 0,
			"item_id": it.itemID, "part": map[string]any{"type": "output_text", "text": ""},
		})
		return append(added, part...), nil
	}
}

func (e *streamEnc) blockDelta(ev ir.StreamEvent) ([]byte, error) {
	if ev.Delta == nil {
		return nil, nil
	}
	it := e.blocks[ev.Index]
	if it == nil {
		return nil, nil
	}
	switch it.typ {
	case "function_call":
		if ev.Delta.Type != ir.DeltaInputJSON || ev.Delta.PartialJSON == "" {
			return nil, nil
		}
		it.acc.WriteString(ev.Delta.PartialJSON)
		return sseFrame("response.function_call_arguments.delta", map[string]any{
			"type": "response.function_call_arguments.delta", "output_index": it.outputIndex, "item_id": it.itemID, "delta": ev.Delta.PartialJSON,
		})
	case "reasoning":
		if ev.Delta.Text == "" {
			return nil, nil
		}
		it.acc.WriteString(ev.Delta.Text)
		return sseFrame("response.reasoning_summary_text.delta", map[string]any{
			"type": "response.reasoning_summary_text.delta", "output_index": it.outputIndex, "item_id": it.itemID, "delta": ev.Delta.Text,
		})
	default: // message
		if ev.Delta.Type != ir.DeltaText || ev.Delta.Text == "" {
			return nil, nil
		}
		it.acc.WriteString(ev.Delta.Text)
		return sseFrame("response.output_text.delta", map[string]any{
			"type": "response.output_text.delta", "output_index": it.outputIndex, "content_index": 0, "delta": ev.Delta.Text,
		})
	}
}

func (e *streamEnc) blockStop(ev ir.StreamEvent) ([]byte, error) {
	it := e.blocks[ev.Index]
	if it == nil {
		return nil, nil
	}
	text := it.acc.String()
	switch it.typ {
	case "function_call":
		done, _ := sseFrame("response.function_call_arguments.done", map[string]any{
			"type": "response.function_call_arguments.done", "output_index": it.outputIndex, "item_id": it.itemID, "arguments": text,
		})
		itemDone, _ := sseFrame("response.output_item.done", map[string]any{
			"type": "response.output_item.done", "output_index": it.outputIndex,
			"item": map[string]any{"id": it.itemID, "type": "function_call", "status": "completed", "name": it.name, "call_id": it.callID, "arguments": text},
		})
		return append(done, itemDone...), nil
	case "reasoning":
		return sseFrame("response.output_item.done", map[string]any{
			"type": "response.output_item.done", "output_index": it.outputIndex,
			"item": map[string]any{"id": it.itemID, "type": "reasoning", "status": "completed",
				"summary": []any{map[string]any{"type": "summary_text", "text": text}}},
		})
	default: // message
		textDone, _ := sseFrame("response.output_text.done", map[string]any{
			"type": "response.output_text.done", "output_index": it.outputIndex, "content_index": 0, "text": text,
		})
		partDone, _ := sseFrame("response.content_part.done", map[string]any{
			"type": "response.content_part.done", "output_index": it.outputIndex, "content_index": 0,
			"item_id": it.itemID, "part": map[string]any{"type": "output_text", "text": text},
		})
		itemDone, _ := sseFrame("response.output_item.done", map[string]any{
			"type": "response.output_item.done", "output_index": it.outputIndex,
			"item": map[string]any{"id": it.itemID, "type": "message", "status": "completed", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": text}}},
		})
		return append(append(textDone, partDone...), itemDone...), nil
	}
}

// emitCompleted assembles the final response object (replaying output items in
// order) and emits response.completed — the stream terminator.
func (e *streamEnc) emitCompleted() ([]byte, error) {
	status := e.status
	if status == "" {
		status = "completed"
	}
	items := make([]*encItem, 0, len(e.blocks))
	for _, it := range e.blocks {
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].outputIndex < items[j].outputIndex })

	output := make([]any, 0, len(items))
	for _, it := range items {
		switch it.typ {
		case "function_call":
			output = append(output, map[string]any{"id": it.itemID, "type": "function_call", "status": "completed",
				"name": it.name, "call_id": it.callID, "arguments": it.acc.String()})
		case "reasoning":
			output = append(output, map[string]any{"id": it.itemID, "type": "reasoning", "status": "completed",
				"summary": []any{map[string]any{"type": "summary_text", "text": it.acc.String()}}})
		default:
			output = append(output, map[string]any{"id": it.itemID, "type": "message", "status": "completed", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": it.acc.String()}}})
		}
	}
	resp := map[string]any{"id": e.respID, "object": "response", "status": status, "model": e.model, "output": output}
	if e.usage != nil {
		resp["usage"] = map[string]any{
			"input_tokens": e.usage.InputTokens, "output_tokens": e.usage.OutputTokens,
			"total_tokens": e.usage.InputTokens + e.usage.OutputTokens,
		}
	}
	return sseFrame("response.completed", map[string]any{"type": "response.completed", "response": resp})
}

func (e *streamEnc) emitFailed(ev ir.StreamEvent) ([]byte, error) {
	code, msg := "stream_error", "stream error"
	if ev.Error != nil {
		code, msg = ev.Error.Code, ev.Error.Message
	}
	return sseFrame("response.failed", map[string]any{
		"type": "response.failed",
		"response": map[string]any{"id": e.respID, "status": "failed",
			"error": map[string]any{"code": code, "message": msg}},
	})
}

// Flush is a no-op: the terminator (response.completed) is emitted on EvStop,
// which is the path the server actually drives. Kept to satisfy the interface.
func (e *streamEnc) Flush() ([]byte, error) { return nil, nil }

// ---------------------------------------------------------------------------
// Decoder: upstream Responses SSE -> IR events (docs/07-streaming.md §2.3)
//
// The Responses streaming protocol is item-oriented: a response accumulates
// typed output items (message / function_call / reasoning), each streamed via
// output_item.added -> per-type deltas -> output_item.done, then the whole
// response terminates with response.completed (carrying usage). The egress
// line-buffer delivers one SSE data payload per Feed() call, so we dispatch on
// the payload's `type` field and reconcile item lifecycles onto the IR
// block-lifecycle grammar.
// ---------------------------------------------------------------------------

// ssePayload is the union of Responses streaming event shapes. Only the fields
// relevant to a given `type` are populated.
type ssePayload struct {
	Type        string       `json:"type"`
	OutputIndex int          `json:"output_index"`
	Delta       string       `json:"delta"`     // output_text/function_call_arguments/reasoning delta
	Item        *sseItem     `json:"item"`      // output_item.added / .done
	Response    *sseResponse `json:"response"`  // created / completed / failed
}

type sseItem struct {
	Type   string `json:"type"` // message | function_call | reasoning
	ID     string `json:"id"`
	Role   string `json:"role"`
	Name   string `json:"name"`
	CallID string `json:"call_id"`
	Status string `json:"status"`
}

type sseResponse struct {
	ID     string          `json:"id"`
	Model  string          `json:"model"`
	Status string          `json:"status"` // in_progress | completed | incomplete | failed
	Usage  *wireUsage      `json:"usage"`
	Error  *sseRespError   `json:"error"`
}

type sseRespError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type streamDec struct {
	started     bool
	nextIdx     int            // next IR block index
	items       []*streamItem  // indexed by Responses output_index
	hadToolCall bool
	status      string         // completed | incomplete | failed (from response.completed)
	stop        ir.StopReason
	usage       ir.Usage
	hasUsage    bool
}

// streamItem tracks one Responses output item's lifecycle on the IR side.
type streamItem struct {
	irIndex int
	typ     string // message | function_call | reasoning
	open    bool
}

func (s *streamDec) ensureItem(outputIndex int) *streamItem {
	for len(s.items) <= outputIndex {
		s.items = append(s.items, nil)
	}
	it := s.items[outputIndex]
	if it == nil {
		it = &streamItem{irIndex: s.nextIdx}
		s.nextIdx++
		s.items[outputIndex] = it
	}
	return it
}

// Feed parses one Responses SSE data payload into IR events.
func (s *streamDec) Feed(payload []byte) ([]ir.StreamEvent, error) {
	if len(payload) == 0 {
		return nil, nil
	}
	var p ssePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, err
	}
	switch {
	case p.Type == "response.created" || p.Type == "response.in_progress":
		return s.handleStart(&p), nil
	case p.Type == "response.output_item.added":
		return s.handleItemAdded(&p), nil
	case p.Type == "response.output_text.delta":
		return s.handleTextDelta(&p), nil
	case p.Type == "response.function_call_arguments.delta":
		return s.handleToolDelta(&p), nil
	case p.Type == "response.reasoning_summary_text.delta" || p.Type == "response.reasoning_text.delta":
		return s.handleReasoningDelta(&p), nil
	case p.Type == "response.output_item.done":
		return s.handleItemDone(&p), nil
	case p.Type == "response.completed":
		return s.handleCompleted(&p), nil
	case p.Type == "response.failed" || p.Type == "response.error":
		return s.handleFailed(&p), nil
	}
	return nil, nil // ignore progress/done/auxiliary events we don't model
}

func (s *streamDec) handleStart(p *ssePayload) []ir.StreamEvent {
	if s.started {
		return nil
	}
	s.started = true
	ev := ir.StreamEvent{Type: ir.EvStart}
	if p.Response != nil {
		ev.ResponseID = p.Response.ID
		ev.Model = p.Response.Model
	}
	return []ir.StreamEvent{ev}
}

func (s *streamDec) handleItemAdded(p *ssePayload) []ir.StreamEvent {
	if p.Item == nil {
		return nil
	}
	it := s.ensureItem(p.OutputIndex)
	it.typ = p.Item.Type
	it.open = true
	if p.Item.Type == "function_call" {
		s.hadToolCall = true
		return []ir.StreamEvent{{Type: ir.EvBlockStart, Index: it.irIndex,
			Block: &ir.Block{Type: ir.BlockToolUse, ID: p.Item.CallID, ToolCall: &ir.ToolCall{ID: p.Item.CallID, Name: p.Item.Name}}}}
	}
	bt := ir.BlockText
	if p.Item.Type == "reasoning" {
		bt = ir.BlockReasoning
	}
	return []ir.StreamEvent{{Type: ir.EvBlockStart, Index: it.irIndex, Block: &ir.Block{Type: bt}}}
}

func (s *streamDec) handleTextDelta(p *ssePayload) []ir.StreamEvent {
	return s.deltaFor(p.OutputIndex, ir.BlockDelta{Type: ir.DeltaText, Text: p.Delta})
}

func (s *streamDec) handleToolDelta(p *ssePayload) []ir.StreamEvent {
	return s.deltaFor(p.OutputIndex, ir.BlockDelta{Type: ir.DeltaInputJSON, PartialJSON: p.Delta})
}

func (s *streamDec) handleReasoningDelta(p *ssePayload) []ir.StreamEvent {
	return s.deltaFor(p.OutputIndex, ir.BlockDelta{Type: ir.DeltaText, Text: p.Delta})
}

func (s *streamDec) deltaFor(outputIndex int, d ir.BlockDelta) []ir.StreamEvent {
	if d.Type == ir.DeltaText && d.Text == "" {
		return nil
	}
	if d.Type == ir.DeltaInputJSON && d.PartialJSON == "" {
		return nil
	}
	if outputIndex >= len(s.items) || s.items[outputIndex] == nil {
		return nil
	}
	return []ir.StreamEvent{{Type: ir.EvBlockDelta, Index: s.items[outputIndex].irIndex, Delta: &d}}
}

func (s *streamDec) handleItemDone(p *ssePayload) []ir.StreamEvent {
	if p.OutputIndex >= len(s.items) || s.items[p.OutputIndex] == nil {
		return nil
	}
	it := s.items[p.OutputIndex]
	if !it.open {
		return nil
	}
	it.open = false
	return []ir.StreamEvent{{Type: ir.EvBlockStop, Index: it.irIndex}}
}

func (s *streamDec) handleCompleted(p *ssePayload) []ir.StreamEvent {
	if p.Response != nil {
		s.status = p.Response.Status
		if p.Response.Usage != nil {
			u := p.Response.Usage
			s.usage = ir.Usage{
				InputTokens:     u.InputTokens,
				OutputTokens:    u.OutputTokens,
				CacheReadTokens: tokenDetail(u.InputTokensDetails, "cached"),
				ReasoningTokens: tokenDetail(u.OutputTokensDetails, "reasoning"),
			}
			s.hasUsage = true
		}
	}
	s.stop = s.inferStop()
	return nil // emitted at Finalize, matching the IR single-message_delta grammar
}

func (s *streamDec) handleFailed(p *ssePayload) []ir.StreamEvent {
	code, msg := "upstream_error", "upstream error"
	if p.Response != nil && p.Response.Error != nil {
		code = p.Response.Error.Code
		msg = p.Response.Error.Message
	}
	return []ir.StreamEvent{{Type: ir.EvError, Error: &ir.StreamError{Code: code, Message: msg, Retryable: false}}}
}

func (s *streamDec) inferStop() ir.StopReason {
	if s.hadToolCall {
		return ir.StopToolUse
	}
	for _, it := range s.items {
		if it != nil && it.typ == "function_call" {
			return ir.StopToolUse
		}
	}
	if s.status == "incomplete" {
		return ir.StopMaxTokens
	}
	return ir.StopEndTurn
}

// Finalize emits the terminal message_delta (usage/stop_reason) and stop. Any
// still-open blocks are closed first (defensive — some streams omit item.done).
func (s *streamDec) Finalize() ([]ir.StreamEvent, error) {
	var out []ir.StreamEvent
	for _, it := range s.items {
		if it != nil && it.open {
			it.open = false
			out = append(out, ir.StreamEvent{Type: ir.EvBlockStop, Index: it.irIndex})
		}
	}
	stop := s.stop
	if stop == "" {
		stop = ir.StopEndTurn
	}
	ev := ir.StreamEvent{Type: ir.EvMessageDelta, StopReason: stop}
	if s.hasUsage {
		u := s.usage
		ev.Usage = &u
	}
	out = append(out, ev)
	out = append(out, ir.StreamEvent{Type: ir.EvStop})
	return out, nil
}

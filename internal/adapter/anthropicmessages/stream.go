package anthropicmessages

import (
	"encoding/json"

	"github.com/aigateway/ai-hub/internal/ir"
)

// ---------------------------------------------------------------------------
// Streaming (Phase 2). Implements StreamEncoder and StreamDecoder per
// docs/07-streaming.md §2.1. Anthropic's native block-lifecycle grammar maps
// almost 1:1 onto the IR stream model.
// ---------------------------------------------------------------------------

// ===== Decoder: upstream Anthropic SSE -> IR events =====

type ssePayload struct {
	Type         string          `json:"type"`
	Message      *sseMessage     `json:"message,omitempty"`
	Index        int             `json:"index,omitempty"`
	ContentBlock *block          `json:"content_block,omitempty"`
	Delta        json.RawMessage `json:"delta,omitempty"`
	Usage        *usage          `json:"usage,omitempty"`
	Error        *sseError       `json:"error,omitempty"`
}

type sseMessage struct {
	ID    string `json:"id"`
	Model string `json:"model"`
	Usage *usage `json:"usage,omitempty"`
}

type sseError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type streamDec struct {
	started  bool
	stop     ir.StopReason
	usage    ir.Usage
	hasUsage bool
}

type deltaPayload struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	Signature   string `json:"signature,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
}

// Feed parses one Anthropic SSE data payload (one JSON object) into IR events.
func (s *streamDec) Feed(payload []byte) ([]ir.StreamEvent, error) {
	if len(payload) == 0 {
		return nil, nil
	}
	var p ssePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, err
	}
	switch p.Type {
	case "message_start":
		s.started = true
		ev := ir.StreamEvent{Type: ir.EvStart}
		if p.Message != nil {
			ev.ResponseID = p.Message.ID
			ev.Model = p.Message.Model
			if p.Message.Usage != nil {
				s.usage.InputTokens = p.Message.Usage.InputTokens
				s.usage.CacheCreationTokens = p.Message.Usage.CacheCreationInputTokens
				s.usage.CacheReadTokens = p.Message.Usage.CacheReadInputTokens
				s.hasUsage = true
			}
		}
		return []ir.StreamEvent{ev}, nil

	case "content_block_start":
		if p.ContentBlock == nil {
			return nil, nil
		}
		blks := blocksToIR([]block{*p.ContentBlock})
		if len(blks) == 0 {
			return nil, nil
		}
		return []ir.StreamEvent{{Type: ir.EvBlockStart, Index: p.Index, Block: &blks[0]}}, nil

	case "content_block_delta":
		var d deltaPayload
		if err := json.Unmarshal(p.Delta, &d); err != nil {
			return nil, err
		}
		var dt ir.BlockDeltaType
		switch d.Type {
		case "text_delta":
			dt = ir.DeltaText
		case "input_json_delta":
			dt = ir.DeltaInputJSON
		case "thinking_delta":
			dt = ir.DeltaThinking
		case "signature_delta":
			return nil, nil // signature fidelity deferred (see docs/05 §5 note)
		default:
			return nil, nil
		}
		bd := &ir.BlockDelta{Type: dt}
		switch dt {
		case ir.DeltaText:
			bd.Text = d.Text
		case ir.DeltaInputJSON:
			bd.PartialJSON = d.PartialJSON
		case ir.DeltaThinking:
			bd.Text = d.Thinking
		}
		return []ir.StreamEvent{{Type: ir.EvBlockDelta, Index: p.Index, Delta: bd}}, nil

	case "content_block_stop":
		return []ir.StreamEvent{{Type: ir.EvBlockStop, Index: p.Index}}, nil

	case "message_delta":
		var d deltaPayload
		_ = json.Unmarshal(p.Delta, &d)
		if d.StopReason != "" {
			s.stop = anthropicStopToIR(d.StopReason)
		}
		if p.Usage != nil {
			s.usage.OutputTokens = p.Usage.OutputTokens
			s.usage.CacheCreationTokens += p.Usage.CacheCreationInputTokens
			s.usage.CacheReadTokens += p.Usage.CacheReadInputTokens
			s.hasUsage = true
		}
		return nil, nil // emitted at Finalize to match IR's single message_delta

	case "message_stop", "ping":
		return nil, nil

	case "error":
		msg := "upstream error"
		code := "upstream_error"
		if p.Error != nil {
			msg = p.Error.Message
			code = p.Error.Type
		}
		return []ir.StreamEvent{{Type: ir.EvError, Error: &ir.StreamError{Code: code, Message: msg, Retryable: false}}}, nil
	}
	return nil, nil
}

// Finalize emits the terminal message_delta (usage/stop_reason) and stop.
func (s *streamDec) Finalize() ([]ir.StreamEvent, error) {
	stop := s.stop
	if stop == "" {
		stop = ir.StopEndTurn
	}
	ev := ir.StreamEvent{Type: ir.EvMessageDelta, StopReason: stop}
	if s.hasUsage {
		u := s.usage
		ev.Usage = &u
	}
	return []ir.StreamEvent{ev, {Type: ir.EvStop}}, nil
}

// ===== Encoder: IR events -> Anthropic client SSE =====

type streamEnc struct{}

func sseEvent(eventType string, payload any) ([]byte, error) {
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

func (e *streamEnc) Encode(ev ir.StreamEvent) ([]byte, error) {
	switch ev.Type {
	case ir.EvStart:
		return sseEvent("message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id": ev.ResponseID, "type": "message", "role": "assistant",
				"model": orDefault(ev.Model, ""), "content": []any{},
				"stop_reason": nil, "stop_sequence": nil,
				"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
			},
		})
	case ir.EvBlockStart:
		if ev.Block == nil {
			return nil, nil
		}
		wb := irBlocksToWire([]ir.Block{*ev.Block})
		if len(wb) == 0 {
			return nil, nil
		}
		return sseEvent("content_block_start", map[string]any{
			"type": "content_block_start", "index": ev.Index, "content_block": wb[0],
		})
	case ir.EvBlockDelta:
		if ev.Delta == nil {
			return nil, nil
		}
		var d map[string]string
		switch ev.Delta.Type {
		case ir.DeltaText:
			d = map[string]string{"type": "text_delta", "text": ev.Delta.Text}
		case ir.DeltaInputJSON:
			d = map[string]string{"type": "input_json_delta", "partial_json": ev.Delta.PartialJSON}
		case ir.DeltaThinking:
			d = map[string]string{"type": "thinking_delta", "thinking": ev.Delta.Text}
		default:
			return nil, nil
		}
		return sseEvent("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": ev.Index, "delta": d,
		})
	case ir.EvBlockStop:
		return sseEvent("content_block_stop", map[string]any{
			"type": "content_block_stop", "index": ev.Index,
		})
	case ir.EvMessageDelta:
		u := map[string]any{"output_tokens": 0}
		if ev.Usage != nil {
			u["output_tokens"] = ev.Usage.OutputTokens
			if ev.Usage.InputTokens > 0 {
				u["input_tokens"] = ev.Usage.InputTokens
			}
		}
		return sseEvent("message_delta", map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": irStopToAnthropic(ev.StopReason), "stop_sequence": nil},
			"usage": u,
		})
	case ir.EvStop:
		return sseEvent("message_stop", map[string]any{"type": "message_stop"})
	case ir.EvError:
		msg := "stream error"
		code := "stream_error"
		if ev.Error != nil {
			msg = ev.Error.Message
			code = ev.Error.Code
		}
		return sseEvent("error", map[string]any{
			"type":  "error",
			"error": map[string]any{"type": code, "message": msg},
		})
	}
	return nil, nil
}

// Flush is a no-op for Anthropic (message_stop is the terminator, emitted on EvStop).
func (e *streamEnc) Flush() ([]byte, error) { return nil, nil }

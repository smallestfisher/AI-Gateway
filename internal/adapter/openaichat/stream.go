package openaichat

import (
	crand "crypto/rand"
	"encoding/json"
	"time"

	"github.com/aigateway/ai-hub/internal/ir"
)

// timeNowUnix is a thin wrapper so tests can stub time if needed.
func timeNowUnix() int64 { return time.Now().Unix() }

// ---------------------------------------------------------------------------
// Streaming (Phase 2). Implements StreamEncoder and StreamDecoder per
// docs/07-streaming.md §2.2. Chat's flat delta stream is reconciled with the
// IR block-lifecycle model via state machines on both sides.
// ---------------------------------------------------------------------------

// ===== Decoder: upstream Chat SSE -> IR events =====

type streamChunk struct {
	ID      string         `json:"id"`
	Model   string         `json:"model"`
	Choices []streamChoice `json:"choices"`
	Usage   *wireUsage     `json:"usage"`
}

type streamChoice struct {
	Delta        streamDelta `json:"delta"`
	FinishReason string      `json:"finish_reason"`
}

type streamDelta struct {
	Role             string           `json:"role"`
	Content          string           `json:"content"`
	ReasoningContent string           `json:"reasoning_content"`
	ToolCalls        []streamToolCall `json:"tool_calls"`
}

type streamToolCall struct {
	Index    int          `json:"index"`
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function streamFunc   `json:"function"`
}

type streamFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type streamDec struct {
	started   bool
	nextIndex int
	textIdx   int
	textOpen  bool
	thinkOpen bool
	thinkIdx  int
	tools     map[int]*openTool // by upstream tool index
	stop      ir.StopReason
	finished  bool
	usage     ir.Usage
	hasUsage  bool
}

type openTool struct {
	irIndex int
	started bool
	name    string
}

func (s *streamDec) ensureStarted(model, id string) []ir.StreamEvent {
	if s.started {
		return nil
	}
	s.started = true
	return []ir.StreamEvent{{Type: ir.EvStart, Model: model, ResponseID: id}}
}

func (s *streamDec) closeText() []ir.StreamEvent {
	if s.textOpen {
		s.textOpen = false
		return []ir.StreamEvent{{Type: ir.EvBlockStop, Index: s.textIdx}}
	}
	return nil
}

func (s *streamDec) closeThinking() []ir.StreamEvent {
	if s.thinkOpen {
		s.thinkOpen = false
		return []ir.StreamEvent{{Type: ir.EvBlockStop, Index: s.thinkIdx}}
	}
	return nil
}

// Feed parses one Chat SSE data payload (one chunk JSON) into IR events.
func (s *streamDec) Feed(payload []byte) ([]ir.StreamEvent, error) {
	if s.tools == nil {
		s.tools = map[int]*openTool{}
	}
	// tolerate a stray [DONE] reaching Feed
	if len(payload) == 0 {
		return nil, nil
	}
	var c streamChunk
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, err
	}
	var out []ir.StreamEvent
	out = append(out, s.ensureStarted(c.Model, c.ID)...)

	if c.Usage != nil {
		s.usage = chatUsageToIR(c.Usage)
		s.hasUsage = true
	}
	if len(c.Choices) == 0 {
		return out, nil
	}
	ch := c.Choices[0]

	// text content
	if ch.Delta.Content != "" {
		if !s.textOpen {
			out = append(out, s.closeThinking()...)
			s.textOpen = true
			s.textIdx = s.nextIndex
			s.nextIndex++
			out = append(out, ir.StreamEvent{Type: ir.EvBlockStart, Index: s.textIdx, Block: &ir.Block{Type: ir.BlockText}})
		}
		out = append(out, ir.StreamEvent{Type: ir.EvBlockDelta, Index: s.textIdx, Delta: &ir.BlockDelta{Type: ir.DeltaText, Text: ch.Delta.Content}})
	}

	// reasoning content (DeepSeek-style)
	if ch.Delta.ReasoningContent != "" {
		if !s.thinkOpen {
			out = append(out, s.closeText()...)
			s.thinkOpen = true
			s.thinkIdx = s.nextIndex
			s.nextIndex++
			out = append(out, ir.StreamEvent{Type: ir.EvBlockStart, Index: s.thinkIdx, Block: &ir.Block{Type: ir.BlockThinking}})
		}
		out = append(out, ir.StreamEvent{Type: ir.EvBlockDelta, Index: s.thinkIdx, Delta: &ir.BlockDelta{Type: ir.DeltaThinking, Text: ch.Delta.ReasoningContent}})
	}

	// tool calls
	for _, tc := range ch.Delta.ToolCalls {
		ot := s.tools[tc.Index]
		if ot == nil {
			ot = &openTool{}
			s.tools[tc.Index] = ot
		}
		if !ot.started {
			// a tool block begins; close text/thinking first
			out = append(out, s.closeText()...)
			out = append(out, s.closeThinking()...)
			ot.started = true
			ot.irIndex = s.nextIndex
			ot.name = tc.Function.Name
			s.nextIndex++
			out = append(out, ir.StreamEvent{
				Type:  ir.EvBlockStart,
				Index: ot.irIndex,
				Block: &ir.Block{Type: ir.BlockToolUse, ID: tc.ID, ToolCall: &ir.ToolCall{ID: tc.ID, Name: tc.Function.Name}},
			})
		}
		if tc.Function.Arguments != "" {
			out = append(out, ir.StreamEvent{Type: ir.EvBlockDelta, Index: ot.irIndex, Delta: &ir.BlockDelta{Type: ir.DeltaInputJSON, PartialJSON: tc.Function.Arguments}})
		}
	}

	// finish
	if ch.FinishReason != "" {
		out = append(out, s.closeText()...)
		out = append(out, s.closeThinking()...)
		for _, ot := range s.tools {
			if ot.started {
				out = append(out, ir.StreamEvent{Type: ir.EvBlockStop, Index: ot.irIndex})
				ot.started = false
			}
		}
		s.stop = chatFinishToStop(ch.FinishReason)
		s.finished = true
	}
	return out, nil
}

// Finalize emits the terminal message_delta (usage/stop_reason) and stop.
func (s *streamDec) Finalize() ([]ir.StreamEvent, error) {
	var out []ir.StreamEvent
	out = append(out, s.closeText()...)
	out = append(out, s.closeThinking()...)
	ev := ir.StreamEvent{Type: ir.EvMessageDelta, StopReason: s.stop}
	if s.stop == "" {
		ev.StopReason = ir.StopEndTurn
	}
	if s.hasUsage {
		u := s.usage
		ev.Usage = &u
	}
	out = append(out, ev)
	out = append(out, ir.StreamEvent{Type: ir.EvStop})
	return out, nil
}

// ===== Encoder: IR events -> Chat client SSE =====

type outChunk struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Choices []outChoice `json:"choices"`
	Usage   *wireUsage  `json:"usage,omitempty"`
}

type outChoice struct {
	Index        int      `json:"index"`
	Delta        outDelta `json:"delta"`
	FinishReason string   `json:"finish_reason,omitempty"`
}

type outDelta struct {
	Role             string         `json:"role,omitempty"`
	Content          string         `json:"content,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []wireToolCall `json:"tool_calls,omitempty"`
}

type streamEnc struct {
	id              string
	model           string
	started         bool
	toolIndexByIR   map[int]int // IR block index -> chat tool index
	nextToolIndex   int
	finishEmitted   bool
	usage           *ir.Usage
}

func sseJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := append([]byte("data: "), b...)
	out = append(out, '\n', '\n')
	return out, nil
}

func (e *streamEnc) chunk(d outDelta, finish string, withUsage bool) ([]byte, error) {
	oc := outChunk{
		ID: e.id, Object: "chat.completion.chunk", Created: nowUnix(), Model: e.model,
		Choices: []outChoice{{Index: 0, Delta: d, FinishReason: finish}},
	}
	if withUsage && e.usage != nil {
		oc.Usage = irUsageToChat(*e.usage)
		// OpenAI sends usage in a separate chunk with empty choices; emulate that.
		oc.Choices = []outChoice{}
	}
	return sseJSON(oc)
}

func (e *streamEnc) Encode(ev ir.StreamEvent) ([]byte, error) {
	if e.toolIndexByIR == nil {
		e.toolIndexByIR = map[int]int{}
	}
	switch ev.Type {
	case ir.EvStart:
		e.id = orDefault(ev.ResponseID, "chatcmpl-"+randHex(12))
		e.model = orDefault(ev.Model, "gpt")
		e.started = true
		// initial role chunk
		return e.chunk(outDelta{Role: "assistant"}, "", false)
	case ir.EvBlockStart:
		if ev.Block != nil && ev.Block.Type == ir.BlockToolUse {
			k := e.nextToolIndex
			e.nextToolIndex++
			if ev.Block.ToolCall != nil {
				e.toolIndexByIR[ev.Index] = k
			}
			tc := wireToolCall{ID: "", Type: "function", Function: wireToolFunction{}}
			if ev.Block.ToolCall != nil {
				tc.ID = ev.Block.ToolCall.ID
				tc.Function.Name = ev.Block.ToolCall.Name
			}
			return e.chunk(outDelta{ToolCalls: []wireToolCall{tc}}, "", false)
		}
		return nil, nil
	case ir.EvBlockDelta:
		if ev.Delta == nil {
			return nil, nil
		}
		switch ev.Delta.Type {
		case ir.DeltaText:
			return e.chunk(outDelta{Content: ev.Delta.Text}, "", false)
		case ir.DeltaThinking:
			return e.chunk(outDelta{ReasoningContent: ev.Delta.Text}, "", false)
		case ir.DeltaInputJSON:
			k := e.toolIndexByIR[ev.Index]
			return e.chunk(outDelta{ToolCalls: []wireToolCall{{
				Index: k, Type: "function", Function: wireToolFunction{Arguments: ev.Delta.PartialJSON},
			}}}, "", false)
		}
	case ir.EvBlockStop:
		return nil, nil
	case ir.EvMessageDelta:
		if ev.Usage != nil {
			e.usage = ev.Usage
		}
		e.finishEmitted = true
		// emit finish chunk (no usage here; usage goes in a separate chunk at stop)
		return e.chunk(outDelta{}, stopToChatFinish(ev.StopReason), false)
	case ir.EvStop:
		// emit the usage chunk (empty choices) if we have usage
		if e.usage != nil {
			return e.chunk(outDelta{}, "", true)
		}
		return nil, nil
	case ir.EvError:
		return nil, nil
	}
	return nil, nil
}

// Flush emits the terminating [DONE] frame.
func (e *streamEnc) Flush() ([]byte, error) {
	return []byte("data: [DONE]\n\n"), nil
}

// randHex returns n lowercase hex chars for stream ids.
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

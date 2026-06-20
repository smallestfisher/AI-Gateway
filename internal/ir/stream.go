package ir

// Stream event model. The IR adopts the most expressive grammar
// (Anthropic-style block lifecycle) so all protocols map cleanly to it.
// See docs/07-streaming.md.

// StreamEventType enumerates unified stream event kinds.
type StreamEventType string

const (
	EvStart        StreamEventType = "start"               // stream start (model/request metadata)
	EvBlockStart   StreamEventType = "content_block_start" // a content block begins
	EvBlockDelta   StreamEventType = "content_block_delta" // incremental update inside a block
	EvBlockStop    StreamEventType = "content_block_stop"  // a content block ends
	EvMessageDelta StreamEventType = "message_delta"       // message-level update (usage/stop_reason)
	EvStop         StreamEventType = "stop"                // stream end
	EvError        StreamEventType = "error"               // error event
)

// StreamEvent is one unified stream event.
type StreamEvent struct {
	Type StreamEventType `json:"type"`

	// start
	ResponseID string `json:"response_id,omitempty"`
	Model      string `json:"model,omitempty"`

	// block lifecycle
	Index int    `json:"index,omitempty"`
	Block *Block `json:"block,omitempty"` // block_start: block skeleton (type+id+name...)

	// delta
	Delta *BlockDelta `json:"delta,omitempty"`

	// message_delta / stop
	StopReason StopReason `json:"stop_reason,omitempty"`
	Usage      *Usage     `json:"usage,omitempty"`

	// error
	Error *StreamError `json:"error,omitempty"`
}

// BlockDelta is an incremental update within a content block.
type BlockDelta struct {
	Type        BlockDeltaType `json:"type"`
	Text        string         `json:"text,omitempty"`         // text/thinking fragment
	PartialJSON string         `json:"partial_json,omitempty"` // tool-input JSON fragment
}

// BlockDeltaType enumerates delta kinds.
type BlockDeltaType string

const (
	DeltaText      BlockDeltaType = "text"
	DeltaInputJSON BlockDeltaType = "input_json" // tool call args increment
	DeltaThinking  BlockDeltaType = "thinking"
)

// StreamError carries an error mid-stream. Retryable is honored only for
// pre-first-byte errors (egress failover window).
type StreamError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

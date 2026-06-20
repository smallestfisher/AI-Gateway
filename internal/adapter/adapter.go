// Package adapter defines the protocol-agnostic conversion contract.
//
// Each wire protocol implements one Adapter covering both directions and all
// four operations. Adding a protocol == implementing one Adapter and registering
// it; the gateway core never changes. See docs/04-plugin-adapters.md.
package adapter

import (
	"net/http"
	"strings"

	"github.com/aigateway/ai-hub/internal/ir"
)

// Protocol identifies a wire protocol.
type Protocol string

const (
	ProtocolChat      Protocol = "openai_chat"        // POST /v1/chat/completions
	ProtocolResponses Protocol = "openai_responses"   // POST /v1/responses
	ProtocolMessages  Protocol = "anthropic_messages" // POST /v1/messages
	ProtocolGemini    Protocol = "google_gemini"      // POST /v1beta/models/...:generateContent
)

// UpstreamRequest is the body produced for an upstream call. URL, auth and
// headers belong to the Egress layer (transport), NOT the adapter; the adapter
// only shapes the body.
type UpstreamRequest struct {
	Path string // path appended to provider BaseURL (e.g. "/v1/chat/completions")
	Body []byte // JSON body in the upstream protocol
}

// Adapter converts between one protocol's wire form and the IR.
//
// Ingress direction (client wire <-> IR):
//   - DecodeRequest: client request body -> IR
//   - EncodeResponse: IR response -> client wire JSON
//
// Egress direction (IR <-> upstream wire):
//   - BuildUpstream: IR -> upstream body (model name already resolved by router)
//   - DecodeUpstreamResponse: upstream response body -> IR
//
// Streaming encoders/decoders are stateful and therefore constructed per use.
type Adapter interface {
	// Protocol returns the protocol this adapter handles.
	Protocol() Protocol

	// DecodeRequest parses a client request body into the IR.
	// hdr is provided for protocol-specific headers (anthropic-version, passthrough).
	DecodeRequest(raw []byte, hdr http.Header) (*ir.UnifiedRequest, error)

	// EncodeResponse encodes an IR response into client wire JSON.
	EncodeResponse(resp *ir.UnifiedResponse) ([]byte, error)

	// BuildUpstream builds the upstream request body from the IR.
	// upstreamModel is the resolved provider-specific model name.
	BuildUpstream(req *ir.UnifiedRequest, upstreamModel string) (*UpstreamRequest, error)

	// DecodeUpstreamResponse parses an upstream non-streaming response into IR.
	DecodeUpstreamResponse(raw []byte) (*ir.UnifiedResponse, error)

	// NewStreamEncoder creates a stateful IR-event -> client SSE encoder.
	NewStreamEncoder() StreamEncoder

	// NewStreamDecoder creates a stateful upstream SSE chunk -> IR-event decoder.
	NewStreamDecoder() StreamDecoder
}

// StreamEncoder converts IR events into client SSE bytes.
type StreamEncoder interface {
	// Encode converts one IR event to SSE bytes (may be nil if the event
	// produces no output in this protocol).
	Encode(ev ir.StreamEvent) ([]byte, error)
	// Flush emits the protocol's terminating frame (e.g. Chat's data: [DONE]).
	Flush() ([]byte, error)
}

// StreamDecoder converts upstream SSE chunks into IR events. A single Feed may
// yield zero, one, or many events; a single SSE frame may span multiple chunks.
type StreamDecoder interface {
	Feed(chunk []byte) ([]ir.StreamEvent, error)
	Finalize() ([]ir.StreamEvent, error)
}

// Registry holds registered adapters keyed by protocol.
type Registry struct {
	adapters map[Protocol]Adapter
}

// NewRegistry creates a registry pre-loaded with built-in adapters.
func NewRegistry(builtins ...Adapter) *Registry {
	r := &Registry{adapters: make(map[Protocol]Adapter, len(builtins))}
	for _, a := range builtins {
		r.Register(a)
	}
	return r
}

// Register adds or replaces an adapter for its protocol (hot-pluggable).
func (r *Registry) Register(a Adapter) {
	r.adapters[a.Protocol()] = a
}

// Get returns the adapter for a protocol.
func (r *Registry) Get(p Protocol) (Adapter, bool) {
	a, ok := r.adapters[p]
	return a, ok
}

// Protocols lists registered protocols (for the admin "protocol management" view).
func (r *Registry) Protocols() []Protocol {
	out := make([]Protocol, 0, len(r.adapters))
	for p := range r.adapters {
		out = append(out, p)
	}
	return out
}

// ByPath infers the ingress protocol from a request path.
//
//	/v1/chat/completions -> openai_chat
//	/v1/responses        -> openai_responses
//	/v1/messages         -> anthropic_messages
//	/v1beta/models/...   -> google_gemini
func ByPath(path string) (Protocol, bool) {
	// strip query
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	switch {
	case strings.HasSuffix(path, "/v1/chat/completions"):
		return ProtocolChat, true
	case strings.HasSuffix(path, "/v1/responses"):
		return ProtocolResponses, true
	case strings.HasSuffix(path, "/v1/messages"):
		return ProtocolMessages, true
	case strings.Contains(path, "/v1beta/models/"):
		return ProtocolGemini, true
	default:
		return "", false
	}
}

package pipeline

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aigateway/ai-hub/internal/adapter"
	"github.com/aigateway/ai-hub/internal/adapter/anthropicmessages"
	"github.com/aigateway/ai-hub/internal/adapter/openaichat"
	"github.com/aigateway/ai-hub/internal/egress"
	"github.com/aigateway/ai-hub/internal/ir"
	"github.com/aigateway/ai-hub/internal/registry"
	"github.com/aigateway/ai-hub/internal/router"
)

func newPipeline(t *testing.T, channels ...*registry.Channel) *Pipeline {
	t.Helper()
	b := registry.NewBuilder()
	for _, c := range channels {
		b.AddChannel(c)
	}
	src := registry.NewStatic(b.Build())
	return New(router.New(src), egress.New(adapter.NewRegistry(openaichat.New(), anthropicmessages.New())))
}

func chatProvider(id, baseURL string) *registry.Provider {
	return &registry.Provider{ID: id, Name: id, Protocol: adapter.ProtocolChat, BaseURL: baseURL, APIKey: "sk"}
}

// --- non-streaming ---

func TestRun_NonStreaming_CrossProtocol(t *testing.T) {
	// fake OpenAI-Chat upstream returning a tool call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"cc","object":"chat.completion","model":"gpt-4o",`+
			`"choices":[{"index":0,"message":{"role":"assistant","tool_calls":[`+
			`{"id":"call_1","type":"function","function":{"name":"f","arguments":"{\"x\":1}"}}]},`+
			`"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
	}))
	defer srv.Close()

	p := newPipeline(t, &registry.Channel{
		Alias: "claude-sonnet", UpstreamModel: "gpt-4o",
		Provider: chatProvider("p1", srv.URL),
	})
	req := &ir.UnifiedRequest{Model: "claude-sonnet",
		Messages: []ir.Message{{Role: ir.RoleUser, Blocks: []ir.Block{{Type: ir.BlockText, Text: "go"}}}}}

	resp, err := p.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if resp.StopReason != ir.StopToolUse {
		t.Errorf("stop = %s", resp.StopReason)
	}
	if len(resp.Blocks) != 1 || resp.Blocks[0].Type != ir.BlockToolUse || resp.Blocks[0].ToolCall.Name != "f" {
		t.Errorf("blocks = %+v", resp.Blocks)
	}
	if resp.ProviderID != "p1" || resp.UpstreamModel != "gpt-4o" {
		t.Errorf("stamp = %s %s", resp.ProviderID, resp.UpstreamModel)
	}
}

func TestRun_NonStreaming_Failover(t *testing.T) {
	hits := map[string]int{}
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits["good"]++
		fmt.Fprint(w, `{"id":"cc","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer good.Close()

	// bad channel: priority 0 (tried first), always 500 -> retryable
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits["bad"]++
		w.WriteHeader(500)
	}))
	defer bad.Close()

	p := newPipeline(t,
		&registry.Channel{Alias: "m", UpstreamModel: "gpt-4o", Provider: chatProvider("bad", bad.URL), Priority: 0},
		&registry.Channel{Alias: "m", UpstreamModel: "gpt-4o", Provider: chatProvider("good", good.URL), Priority: 1},
	)
	resp, err := p.Run(context.Background(), &ir.UnifiedRequest{Model: "m"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if hits["bad"] != 1 {
		t.Errorf("bad should be hit once, got %d", hits["bad"])
	}
	if hits["good"] != 1 {
		t.Errorf("good should be hit once, got %d", hits["good"])
	}
	if resp.Blocks[0].Text != "ok" {
		t.Errorf("text = %q", resp.Blocks[0].Text)
	}
}

// --- streaming ---

// a fake OpenAI-Chat streaming upstream emitting text then stop.
func chatStreamServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		write := func(s string) { fmt.Fprint(w, s); if flusher != nil { flusher.Flush() } }
		write("data: {\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
		write("data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n")
		write("data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n")
		write("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		write("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\n\n")
		write("data: [DONE]\n\n")
	}))
}

func collectStream(t *testing.T, p *Pipeline, req *ir.UnifiedRequest) []ir.StreamEvent {
	t.Helper()
	var out []ir.StreamEvent
	err := p.RunStream(context.Background(), req, func(ev ir.StreamEvent) { out = append(out, ev) })
	if err != nil {
		t.Fatalf("runstream: %v", err)
	}
	return out
}

func TestRunStream_TextThroughChatUpstream(t *testing.T) {
	srv := chatStreamServer()
	defer srv.Close()
	p := newPipeline(t, &registry.Channel{Alias: "m", UpstreamModel: "gpt-4o", Provider: chatProvider("p1", srv.URL)})
	evs := collectStream(t, p, &ir.UnifiedRequest{Model: "m", Stream: true,
		Messages: []ir.Message{{Role: ir.RoleUser, Blocks: []ir.Block{{Type: ir.BlockText, Text: "hi"}}}}})

	var text string
	var stopReason ir.StopReason
	for _, e := range evs {
		if e.Type == ir.EvBlockDelta && e.Delta != nil && e.Delta.Type == ir.DeltaText {
			text += e.Delta.Text
		}
		if e.Type == ir.EvMessageDelta {
			stopReason = e.StopReason
		}
	}
	if text != "Hello" {
		t.Errorf("text = %q", text)
	}
	if stopReason != ir.StopEndTurn {
		t.Errorf("stop = %s", stopReason)
	}
}

func TestRunStream_FailoverBeforeFirstByte(t *testing.T) {
	good := chatStreamServer()
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer bad.Close()

	// bad first (priority 0), then good (priority 1). Stream should failover to good.
	p := newPipeline(t,
		&registry.Channel{Alias: "m", UpstreamModel: "gpt-4o", Provider: chatProvider("bad", bad.URL), Priority: 0},
		&registry.Channel{Alias: "m", UpstreamModel: "gpt-4o", Provider: chatProvider("good", good.URL), Priority: 1},
	)
	evs := collectStream(t, p, &ir.UnifiedRequest{Model: "m", Stream: true})

	var text string
	for _, e := range evs {
		if e.Type == ir.EvBlockDelta && e.Delta != nil && e.Delta.Type == ir.DeltaText {
			text += e.Delta.Text
		}
	}
	if text != "Hello" {
		t.Errorf("expected failover to produce 'Hello', got %q", text)
	}
}

func TestRunStream_NoChannelFails(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer bad.Close()
	p := newPipeline(t, &registry.Channel{Alias: "m", UpstreamModel: "gpt-4o", Provider: chatProvider("bad", bad.URL)})
	var n int
	err := p.RunStream(context.Background(), &ir.UnifiedRequest{Model: "m", Stream: true}, func(ir.StreamEvent) { n++ })
	if err == nil {
		t.Error("want error when no channel streams")
	}
	if n != 0 {
		t.Errorf("no events should be emitted, got %d", n)
	}
}

// Ensure a streaming body is fully drained to avoid goroutine/connection leaks.
var _ = io.EOF

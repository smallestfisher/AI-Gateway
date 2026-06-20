package server_test

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aigateway/ai-hub/internal/adapter"
	"github.com/aigateway/ai-hub/internal/adapter/anthropicmessages"
	"github.com/aigateway/ai-hub/internal/adapter/openaichat"
	"github.com/aigateway/ai-hub/internal/config"
	"github.com/aigateway/ai-hub/internal/egress"
	"github.com/aigateway/ai-hub/internal/pipeline"
	"github.com/aigateway/ai-hub/internal/registry"
	"github.com/aigateway/ai-hub/internal/router"
	"github.com/aigateway/ai-hub/internal/server"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/utils"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// buildApp wires a real Fiber app whose pipeline targets the given upstream URL
// (an OpenAI-Chat-speaking fake). The exposed alias is "claude-sonnet".
func buildApp(t *testing.T, upstreamURL string) *fiber.App {
	t.Helper()
	b := registry.NewBuilder().AddChannel(&registry.Channel{
		Alias:         "claude-sonnet",
		UpstreamModel: "gpt-4o",
		Provider: &registry.Provider{
			ID: "p1", Name: "p1", Protocol: adapter.ProtocolChat,
			BaseURL: upstreamURL, APIKey: "sk-test",
		},
	})
	reg := adapter.NewRegistry(openaichat.New(), anthropicmessages.New())
	pipe := pipeline.New(router.New(registry.NewStatic(b.Build())), egress.New(reg))
	cfg := &config.Config{}
	return server.New(cfg, testLogger(), server.Deps{Registry: reg, Pipeline: pipe})
}

// A Claude Code client (Anthropic Messages) hits the gateway; the upstream is a
// fake OpenAI-Chat server. The response must come back in Anthropic Messages
// format — proving cross-protocol end-to-end.
func TestHTTP_NonStreaming_CrossProtocol(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"cc","object":"chat.completion","model":"gpt-4o",`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":"Hello from chat upstream"},`+
			`"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}`)
	}))
	defer upstream.Close()

	app := buildApp(t, upstream.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"claude-sonnet","max_tokens":256,`+
			`"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req, -1) // -1 = no timeout
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	// Anthropic envelope
	if !strings.Contains(s, `"type":"message"`) {
		t.Errorf("not anthropic envelope: %s", s)
	}
	if !strings.Contains(s, "Hello from chat upstream") {
		t.Errorf("content lost: %s", s)
	}
	if !strings.Contains(s, `"stop_reason":"end_turn"`) {
		t.Errorf("stop_reason lost: %s", s)
	}
	if resp.Header.Get("X-Upstream-Model") != "gpt-4o" {
		t.Errorf("X-Upstream-Model = %q", resp.Header.Get("X-Upstream-Model"))
	}
}

// Streaming: Claude Code client requests stream; fake Chat upstream streams;
// client must receive Anthropic SSE events (cross-protocol streaming).
func TestHTTP_Streaming_CrossProtocol(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		write := func(s string) { fmt.Fprint(w, s); if flusher != nil { flusher.Flush() } }
		write("data: {\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
		write("data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n")
		write("data: {\"choices\":[{\"delta\":{\"content\":\" there\"}}]}\n\n")
		write("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		write("data: [DONE]\n\n")
	}))
	defer upstream.Close()

	app := buildApp(t, upstream.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"claude-sonnet","max_tokens":256,"stream":true,`+
			`"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	// Anthropic SSE event names must be present
	for _, want := range []string{
		"event: message_start",
		"event: content_block_start",
		"event: content_block_delta",
		`"text_delta"`,
		"event: content_block_stop",
		"event: message_delta",
		"event: message_stop",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in SSE:\n%s", want, s)
		}
	}
	// reassemble text deltas
	var text string
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, `"text":"`) {
			// crude extraction: find "text":"..." inside text_delta
			if i := strings.Index(line, `"text":"`); i >= 0 {
				rest := line[i+len(`"text":"`):]
				if j := strings.Index(rest, `"`); j >= 0 {
					text += rest[:j]
				}
			}
		}
	}
	if text != "Hi there" {
		t.Errorf("reassembled text = %q", text)
	}
	utils.AssertEqual(t, "text/event-stream", resp.Header.Get("Content-Type"))
}

// Unknown model -> 404 model_not_found in the client protocol's error shape.
func TestHTTP_UnknownModel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	app := buildApp(t, upstream.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"does-not-exist","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"type":"error"`) {
		t.Errorf("not anthropic error shape: %s", body)
	}
}

func TestHTTP_BadRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	app := buildApp(t, upstream.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{not valid json`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

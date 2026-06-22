package egress

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aigateway/ai-hub/internal/adapter"
	"github.com/aigateway/ai-hub/internal/adapter/anthropicmessages"
	"github.com/aigateway/ai-hub/internal/adapter/openairesponses"
	"github.com/aigateway/ai-hub/internal/ir"
	"github.com/aigateway/ai-hub/internal/registry"
)

func newEgress() *Egress {
	return New(adapter.NewRegistry(anthropicmessages.New()))
}

func msgChannel(baseURL string) *registry.Channel {
	return &registry.Channel{
		Alias:         "claude-sonnet",
		UpstreamModel: "claude-real",
		Provider: &registry.Provider{
			ID:       "p1",
			Name:     "p1",
			Protocol: adapter.ProtocolMessages,
			BaseURL:  baseURL,
			APIKey:   "sk-test",
		},
	}
}

func responsesChannel(baseURL string) *registry.Channel {
	return &registry.Channel{
		Alias:         "gpt-5.5",
		UpstreamModel: "gpt-5.5",
		Provider: &registry.Provider{
			ID:       "p1",
			Name:     "p1",
			Protocol: adapter.ProtocolResponses,
			BaseURL:  baseURL,
			APIKey:   "sk-test",
		},
	}
}

func TestSend_NonStreaming_DecodesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "sk-test" {
			t.Errorf("x-api-key = %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got == "" {
			t.Errorf("anthropic-version not set")
		}
		// echo back the requested model to prove model rewrite reached upstream
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["model"] != "claude-real" {
			t.Errorf("upstream model = %v", in["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant",
			"model":       "claude-real",
			"content":     []map[string]any{{"type": "text", "text": "hi"}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 3, "output_tokens": 2},
		})
	}))
	defer srv.Close()

	eg := newEgress()
	req := &ir.UnifiedRequest{Model: "claude-sonnet", Messages: []ir.Message{{Role: ir.RoleUser, Blocks: []ir.Block{{Type: ir.BlockText, Text: "hello"}}}}}
	resp, err := eg.Send(context.Background(), req, msgChannel(srv.URL))
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if resp.StopReason != ir.StopEndTurn {
		t.Errorf("stop = %s", resp.StopReason)
	}
	if len(resp.Blocks) != 1 || resp.Blocks[0].Text != "hi" {
		t.Errorf("blocks = %+v", resp.Blocks)
	}
	if resp.Usage.InputTokens != 3 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if resp.ProviderID != "p1" || resp.UpstreamModel != "claude-real" {
		t.Errorf("provider/model not stamped: %s %s", resp.ProviderID, resp.UpstreamModel)
	}
}

func TestSend_5xxRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		_, _ = io.WriteString(w, `{"error":"down"}`)
	}))
	defer srv.Close()

	_, err := newEgress().Send(context.Background(), &ir.UnifiedRequest{Model: "claude-sonnet"}, msgChannel(srv.URL))
	if err == nil {
		t.Fatal("want error")
	}
	if !IsRetryable(err) {
		t.Errorf("want retryable, got %v", err)
	}
}

func TestSend_4xxNotRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = io.WriteString(w, `{"error":"bad"}`)
	}))
	defer srv.Close()

	_, err := newEgress().Send(context.Background(), &ir.UnifiedRequest{Model: "claude-sonnet"}, msgChannel(srv.URL))
	if err == nil {
		t.Fatal("want error")
	}
	if IsRetryable(err) {
		t.Errorf("want non-retryable, got %v", err)
	}
}

func TestSend_UnreachableRetryable(t *testing.T) {
	// a port nothing listens on -> connection refused
	ch := msgChannel("http://127.0.0.1:39999")
	_, err := newEgress().Send(context.Background(), &ir.UnifiedRequest{Model: "claude-sonnet"}, ch)
	if err == nil {
		t.Fatal("want error")
	}
	var ue *UpstreamError
	if !errors.As(err, &ue) || !ue.Retryable {
		t.Errorf("want retryable UpstreamError, got %T %v", err, err)
	}
}

func TestSend_ClientProfileHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); ua != "claude-cli/1.0" {
			t.Errorf("UA = %q", ua)
		}
		if o := r.Header.Get("Origin"); o != "https://x" {
			t.Errorf("Origin = %q", o)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "m", "type": "message", "role": "assistant", "model": "claude-real",
			"content": []map[string]any{{"type": "text", "text": "ok"}}, "stop_reason": "end_turn",
		})
	}))
	defer srv.Close()

	ch := msgChannel(srv.URL)
	ch.Profile = &registry.ClientProfile{
		UserAgent: "claude-cli/1.0",
		Origin:    "https://x",
		Headers:   map[string]string{"x-custom": "1"},
	}
	_, err := newEgress().Send(context.Background(), &ir.UnifiedRequest{Model: "claude-sonnet"}, ch)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
}

func TestSend_DefaultResponsesClientHeaders(t *testing.T) {
	var gotUA, gotAccept, gotOriginator, gotThreadID, gotSessionID, gotWindowID, gotRequestID, gotBetaFeatures, gotMetadata string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotAccept = r.Header.Get("Accept")
		gotOriginator = r.Header.Get("Originator")
		gotThreadID = r.Header.Get("Thread-Id")
		gotSessionID = r.Header.Get("Session-Id")
		gotWindowID = r.Header.Get("X-Codex-Window-Id")
		gotRequestID = r.Header.Get("X-Client-Request-Id")
		gotBetaFeatures = r.Header.Get("X-Codex-Beta-Features")
		gotMetadata = r.Header.Get("X-Codex-Turn-Metadata")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_test","object":"response","model":"gpt-5.5","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	}))
	defer srv.Close()

	eg := New(adapter.NewRegistry(openairesponses.New()))
	req := &ir.UnifiedRequest{
		ID:             "rq-test",
		ClientProtocol: string(adapter.ProtocolResponses),
		Model:          "gpt-5.5",
		Messages:       []ir.Message{{Role: ir.RoleUser, Blocks: []ir.Block{{Type: ir.BlockText, Text: "hello"}}}},
	}
	_, err := eg.Send(context.Background(), req, responsesChannel(srv.URL))
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if gotUA != "codex-tui/0.139.0 (Ubuntu 24.4.0; x86_64) WindowsTerminal (codex-tui; 0.139.0)" {
		t.Fatalf("User-Agent = %q", gotUA)
	}
	if gotAccept != "application/json" {
		t.Fatalf("Accept = %q", gotAccept)
	}
	if gotOriginator != "codex-tui" {
		t.Fatalf("Originator = %q", gotOriginator)
	}
	if gotBetaFeatures != "terminal_resize_reflow" {
		t.Fatalf("X-Codex-Beta-Features = %q", gotBetaFeatures)
	}
	if gotThreadID != "rq-test" || gotSessionID != "rq-test" || gotRequestID != "rq-test" || gotWindowID != "rq-test:0" {
		t.Fatalf("dynamic ids thread=%q session=%q request=%q window=%q", gotThreadID, gotSessionID, gotRequestID, gotWindowID)
	}
	if !strings.Contains(gotMetadata, `"request_kind":"turn"`) || !strings.Contains(gotMetadata, `"session_id":"rq-test"`) {
		t.Fatalf("X-Codex-Turn-Metadata = %q", gotMetadata)
	}
}

func TestSend_DefaultMessagesClientHeaders(t *testing.T) {
	var gotUA, gotAccept, gotXApp, gotBeta, gotSessionID, gotVersion, gotRuntime, gotPackage, gotBrowserAccess string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotAccept = r.Header.Get("Accept")
		gotXApp = r.Header.Get("X-App")
		gotBeta = r.Header.Get("Anthropic-Beta")
		gotSessionID = r.Header.Get("X-Claude-Code-Session-Id")
		gotVersion = r.Header.Get("Anthropic-Version")
		gotRuntime = r.Header.Get("X-Stainless-Runtime")
		gotPackage = r.Header.Get("X-Stainless-Package-Version")
		gotBrowserAccess = r.Header.Get("Anthropic-Dangerous-Direct-Browser-Access")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"m","type":"message","role":"assistant","model":"claude-real","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()

	eg := newEgress()
	req := &ir.UnifiedRequest{
		ID:             "session-test",
		ClientProtocol: string(adapter.ProtocolMessages),
		Model:          "claude-sonnet",
		Messages:       []ir.Message{{Role: ir.RoleUser, Blocks: []ir.Block{{Type: ir.BlockText, Text: "hello"}}}},
	}
	_, err := eg.Send(context.Background(), req, msgChannel(srv.URL))
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if gotUA != "claude-cli/2.1.181 (external, cli)" {
		t.Fatalf("User-Agent = %q", gotUA)
	}
	if gotAccept != "application/json" {
		t.Fatalf("Accept = %q", gotAccept)
	}
	if gotXApp != "cli" {
		t.Fatalf("X-App = %q", gotXApp)
	}
	if gotSessionID != "session-test" {
		t.Fatalf("X-Claude-Code-Session-Id = %q", gotSessionID)
	}
	if gotVersion != "2023-06-01" {
		t.Fatalf("Anthropic-Version = %q", gotVersion)
	}
	if !strings.Contains(gotBeta, "claude-code-20250219") || !strings.Contains(gotBeta, "structured-outputs-2025-12-15") {
		t.Fatalf("Anthropic-Beta = %q", gotBeta)
	}
	if gotRuntime != "node" || gotPackage != "0.94.0" || gotBrowserAccess != "true" {
		t.Fatalf("stainless/browser headers runtime=%q package=%q browser=%q", gotRuntime, gotPackage, gotBrowserAccess)
	}
}

func TestBuildRequestKeepsCLIStreamAccept(t *testing.T) {
	eg := New(adapter.NewRegistry(openairesponses.New()))
	req := &ir.UnifiedRequest{
		ID:             "rq-stream",
		ClientProtocol: string(adapter.ProtocolResponses),
	}
	httpReq, err := eg.buildRequest(
		context.Background(),
		req,
		responsesChannel("https://example.com"),
		&adapter.UpstreamRequest{Path: "/v1/responses", Body: []byte("{}")},
		true,
	)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if got := httpReq.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("Accept = %q", got)
	}
}

func TestSend_ClientProfileOverridesDefaultClientHeaders(t *testing.T) {
	var gotUA, gotClientName string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotClientName = r.Header.Get("X-Client-Name")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_test","object":"response","model":"gpt-5.5","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`)
	}))
	defer srv.Close()

	ch := responsesChannel(srv.URL)
	ch.Profile = &registry.ClientProfile{
		UserAgent: "custom-client/9.9",
		Headers:   map[string]string{"X-Client-Name": "custom-client"},
	}
	eg := New(adapter.NewRegistry(openairesponses.New()))
	req := &ir.UnifiedRequest{
		ClientProtocol: string(adapter.ProtocolResponses),
		Model:          "gpt-5.5",
		Messages:       []ir.Message{{Role: ir.RoleUser, Blocks: []ir.Block{{Type: ir.BlockText, Text: "hello"}}}},
	}
	_, err := eg.Send(context.Background(), req, ch)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if gotUA != "custom-client/9.9" {
		t.Fatalf("User-Agent = %q", gotUA)
	}
	if gotClientName != "custom-client" {
		t.Fatalf("X-Client-Name = %q", gotClientName)
	}
}

package egress

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aigateway/ai-hub/internal/adapter"
	"github.com/aigateway/ai-hub/internal/adapter/anthropicmessages"
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

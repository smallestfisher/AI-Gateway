package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aigateway/ai-hub/internal/adapter"
	"github.com/aigateway/ai-hub/internal/adapter/openaichat"
	"github.com/aigateway/ai-hub/internal/ir"
	"github.com/aigateway/ai-hub/internal/registry"
)

func TestDiagnosticsMinimalRequestByProtocol(t *testing.T) {
	cases := []struct {
		name     string
		proto    adapter.Protocol
		contains string
	}{
		{name: "chat", proto: adapter.ProtocolChat, contains: `"messages"`},
		{name: "messages", proto: adapter.ProtocolMessages, contains: `"max_tokens":16`},
		{name: "responses", proto: adapter.ProtocolResponses, contains: `"input":"ping"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := buildDiagnosticWireRequest(tc.proto, "alias-a", "ping")
			if err != nil {
				t.Fatalf("buildDiagnosticWireRequest: %v", err)
			}
			if !json.Valid(raw) {
				t.Fatalf("request is not valid JSON: %s", raw)
			}
			if !containsString(string(raw), tc.contains) {
				t.Fatalf("request %s does not contain %s", raw, tc.contains)
			}
		})
	}
}

func TestDiagnosticsPreviewTextCapsOutput(t *testing.T) {
	resp := &ir.UnifiedResponse{
		Blocks: []ir.Block{{Type: ir.BlockText, Text: "abcdefghijklmnopqrstuvwxyz"}},
	}
	got := diagnosticPreview(resp, 10)
	if got != "abcdefghij..." {
		t.Fatalf("preview = %q", got)
	}
}

func TestDiagnosticsDirectUpstreamUsesProviderProtocol(t *testing.T) {
	var gotPath, gotAuth, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel = body.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_test","object":"chat.completion","model":"gpt-test","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	d := NewDiagnostics(nil, adapter.NewRegistry(openaichat.New()), nil)
	result := d.testProviderUpstream(context.Background(), &registry.Provider{
		ID: "p1", Name: "Provider 1", Protocol: adapter.ProtocolChat,
		BaseURL: srv.URL, APIKey: "sk-test", Timeout: 5 * time.Second,
	}, UpstreamTestInput{UpstreamModel: "gpt-test", Message: "ping"})

	if !result.OK {
		t.Fatalf("result failed: %+v", result)
	}
	if gotPath != "/v1/chat/completions" || gotModel != "gpt-test" {
		t.Fatalf("path/model = %s/%s", gotPath, gotModel)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if result.ResponsePreview != "pong" || result.Usage == nil || result.Usage.OutputTokens != 1 {
		t.Fatalf("bad result: %+v", result)
	}
}

func containsString(s, want string) bool {
	return len(want) == 0 || (len(s) >= len(want) && jsonContains(s, want))
}

func jsonContains(s, want string) bool {
	for i := 0; i+len(want) <= len(s); i++ {
		if s[i:i+len(want)] == want {
			return true
		}
	}
	return false
}

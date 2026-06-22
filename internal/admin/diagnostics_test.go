package admin

import (
	"encoding/json"
	"testing"

	"github.com/aigateway/ai-hub/internal/adapter"
	"github.com/aigateway/ai-hub/internal/ir"
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

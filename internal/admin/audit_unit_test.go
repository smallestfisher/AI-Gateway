package admin

import "testing"

func TestAuditRedactsSecrets(t *testing.T) {
	got := redactAuditDiff(map[string]any{
		"name":          "provider",
		"api_key":       "sk-secret",
		"Authorization": "Bearer token",
		"nested": map[string]any{
			"token": "secret-token",
			"url":   "https://example.com",
		},
	})

	if got["name"] != "provider" {
		t.Fatalf("non-secret field changed: %+v", got)
	}
	if got["api_key"] != "[REDACTED]" || got["Authorization"] != "[REDACTED]" {
		t.Fatalf("top-level secrets not redacted: %+v", got)
	}
	nested, ok := got["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested map missing: %+v", got)
	}
	if nested["token"] != "[REDACTED]" || nested["url"] != "https://example.com" {
		t.Fatalf("nested redaction mismatch: %+v", nested)
	}
}

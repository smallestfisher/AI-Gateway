package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aigateway/ai-hub/internal/adapter"
)

func TestFetchUpstreamModelsReturnsUpstreamErrorWithBodyPreview(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"only codex cli"}}`))
	}))
	defer srv.Close()

	_, err := fetchUpstreamModels(context.Background(), upstreamProvider{
		ID: "p1", Protocol: string(adapter.ProtocolResponses), BaseURL: srv.URL, APIKey: "sk-test",
	}, nil)
	if err == nil {
		t.Fatal("want error")
	}
	var ue *ErrUpstream
	if !errors.As(err, &ue) {
		t.Fatalf("err=%T %v, want ErrUpstream", err, err)
	}
	if ue.Status != http.StatusForbidden {
		t.Fatalf("status=%d", ue.Status)
	}
	if !strings.Contains(err.Error(), "only codex cli") {
		t.Fatalf("err=%q", err.Error())
	}
}

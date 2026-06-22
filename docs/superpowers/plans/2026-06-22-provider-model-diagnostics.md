# Provider & Model Diagnostics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add admin diagnostics that test providers directly and test configured model channels through the gateway path.

**Architecture:** Add a focused `admin.Diagnostics` service that reuses the existing adapter registry, egress transport, and runtime pipeline snapshot. The Provider page opens a diagnostics sheet for model discovery, direct upstream tests, and forced gateway-path tests; the Model page adds a one-click gateway-path test action to each channel row.

**Tech Stack:** Go/Fiber/pgx, existing `adapter`, `egress`, `pipeline`, `registry`; Next.js App Router, TanStack Query, React Hook Form, shadcn/Radix UI, lucide-react.

---

## File Structure

Backend:

- Create `internal/admin/diagnostics.go`: diagnostic DTOs, minimal request builders, direct upstream test, gateway-path test, result shaping.
- Create `internal/admin/diagnostics_test.go`: unit tests with `httptest.Server` and static registry snapshots.
- Modify `internal/admin/upstream_models.go`: reuse provider projection logic for disabled-provider direct tests.
- Modify `internal/admin/admin.go`: add optional diagnostics dependency and register diagnostic routes.
- Modify `cmd/gateway/main.go`: instantiate diagnostics for the production Admin API.

Frontend:

- Modify `apps/web/lib/types.ts`: diagnostic request/result types.
- Modify `apps/web/lib/query-keys.ts`: diagnostic query/mutation keys where useful.
- Create `apps/web/components/diagnostics/diagnostic-result.tsx`: shared result display.
- Create `apps/web/components/diagnostics/gateway-test-sheet.tsx`: focused gateway-path test sheet for channel rows.
- Create `apps/web/app/(admin)/providers/provider-diagnostics-sheet.tsx`: provider diagnostics sheet.
- Modify `apps/web/app/(admin)/providers/columns.tsx`: add diagnostics action callback.
- Modify `apps/web/app/(admin)/providers/page.tsx`: manage diagnostics sheet state and fetch model/channel data.
- Modify `apps/web/app/(admin)/models/page.tsx`: add gateway-path test action on each expanded channel row.

Execution notes:

- There are existing uncommitted changes in `apps/web/app/globals.css`, `apps/web/app/layout.tsx`, and `apps/web/next.config.ts` from the Google Fonts/Turbopack-root fix. Do not revert them. Stage only files that belong to each task.
- Diagnostic test calls must not expose API keys, raw Authorization headers, or full upstream request bodies to the browser.
- Diagnostic requests should use a fresh egress instance without a health recorder, so manual tests do not affect circuit-breaker health data.

---

### Task 1: Backend Diagnostic Core

**Files:**
- Create: `internal/admin/diagnostics.go`
- Create: `internal/admin/diagnostics_test.go`
- Modify: `internal/admin/upstream_models.go`

- [ ] **Step 1: Write failing tests for minimal request builders and previews**

Create `internal/admin/diagnostics_test.go` with these tests:

```go
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
```

- [ ] **Step 2: Run tests and verify they fail for missing functions**

Run:

```bash
GOCACHE=/tmp/go-build go test ./internal/admin -run 'TestDiagnosticsMinimalRequestByProtocol|TestDiagnosticsPreviewTextCapsOutput' -v
```

Expected: FAIL with errors naming `buildDiagnosticWireRequest` and `diagnosticPreview`.

- [ ] **Step 3: Add diagnostic DTOs, helpers, and provider projection**

Create `internal/admin/diagnostics.go` with this structure:

```go
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aigateway/ai-hub/internal/adapter"
	"github.com/aigateway/ai-hub/internal/egress"
	"github.com/aigateway/ai-hub/internal/ir"
	"github.com/aigateway/ai-hub/internal/pipeline"
	"github.com/aigateway/ai-hub/internal/registry"
)

const diagnosticPreviewLimit = 240

type Diagnostics struct {
	store    *Store
	adapters *adapter.Registry
	pipe     *pipeline.Pipeline
	eg       *egress.Egress
}

func NewDiagnostics(st *Store, adapters *adapter.Registry, pipe *pipeline.Pipeline) *Diagnostics {
	return &Diagnostics{
		store:    st,
		adapters: adapters,
		pipe:     pipe,
		eg:       egress.New(adapters),
	}
}

type UpstreamTestInput struct {
	UpstreamModel string `json:"upstream_model"`
	Message       string `json:"message"`
	TimeoutMs     int    `json:"timeout_ms"`
}

type GatewayTestInput struct {
	ClientProtocol string `json:"client_protocol"`
	Alias          string `json:"alias"`
	ProviderID     string `json:"provider_id"`
	UpstreamModel  string `json:"upstream_model"`
	Message        string `json:"message"`
	TimeoutMs      int    `json:"timeout_ms"`
}

type DiagnosticResult struct {
	OK              bool             `json:"ok"`
	Mode            string           `json:"mode"`
	ClientProtocol  string           `json:"client_protocol,omitempty"`
	Alias           string           `json:"alias,omitempty"`
	ProviderID      string           `json:"provider_id,omitempty"`
	ProviderName    string           `json:"provider_name,omitempty"`
	Protocol        string           `json:"protocol,omitempty"`
	UpstreamModel   string           `json:"upstream_model,omitempty"`
	LatencyMs       int              `json:"latency_ms"`
	HTTPStatus      int              `json:"http_status,omitempty"`
	StopReason      string           `json:"stop_reason,omitempty"`
	Usage           *ir.Usage        `json:"usage,omitempty"`
	ResponsePreview string           `json:"response_preview,omitempty"`
	Error           *DiagnosticError `json:"error,omitempty"`
}

type DiagnosticError struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	BodyPreview string `json:"body_preview,omitempty"`
}

func buildDiagnosticWireRequest(proto adapter.Protocol, model, message string) ([]byte, error) {
	if message == "" {
		message = "ping"
	}
	switch proto {
	case adapter.ProtocolChat:
		return json.Marshal(map[string]any{
			"model": model,
			"messages": []map[string]string{
				{"role": "user", "content": message},
			},
			"max_tokens": 16,
			"stream":     false,
		})
	case adapter.ProtocolMessages:
		return json.Marshal(map[string]any{
			"model":      model,
			"messages":   []map[string]any{{"role": "user", "content": message}},
			"max_tokens": 16,
			"stream":     false,
		})
	case adapter.ProtocolResponses:
		return json.Marshal(map[string]any{
			"model":             model,
			"input":             message,
			"max_output_tokens": 16,
			"stream":            false,
		})
	default:
		return nil, fmt.Errorf("admin diagnostics: unsupported protocol %s", proto)
	}
}

func diagnosticPreview(resp *ir.UnifiedResponse, limit int) string {
	if resp == nil || limit <= 0 {
		return ""
	}
	var b strings.Builder
	for _, block := range resp.Blocks {
		if block.Type == ir.BlockText && block.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(block.Text)
		}
	}
	out := b.String()
	if len(out) > limit {
		return out[:limit] + "..."
	}
	return out
}

func diagnosticTimeout(parent context.Context, timeoutMs int) (context.Context, context.CancelFunc) {
	if timeoutMs <= 0 {
		timeoutMs = 30000
	}
	if timeoutMs > 120000 {
		timeoutMs = 120000
	}
	return context.WithTimeout(parent, time.Duration(timeoutMs)*time.Millisecond)
}

func diagnosticFailure(mode string, start time.Time, base DiagnosticResult, err error) DiagnosticResult {
	base.OK = false
	base.Mode = mode
	base.LatencyMs = int(time.Since(start).Milliseconds())
	base.Error = diagnosticError(err)
	var ue *egress.UpstreamError
	if errors.As(err, &ue) {
		base.HTTPStatus = ue.Status
		if len(ue.Body) > 0 {
			body := string(ue.Body)
			if len(body) > diagnosticPreviewLimit {
				body = body[:diagnosticPreviewLimit] + "..."
			}
			base.Error.BodyPreview = body
		}
	}
	return base
}

func diagnosticError(err error) *DiagnosticError {
	if err == nil {
		return nil
	}
	var ue *egress.UpstreamError
	if errors.As(err, &ue) {
		return &DiagnosticError{Code: ue.Code, Message: ue.Error()}
	}
	if errors.Is(err, ErrValidation) {
		return &DiagnosticError{Code: "validation_error", Message: err.Error()}
	}
	if errors.Is(err, ErrNotFound) {
		return &DiagnosticError{Code: "not_found", Message: err.Error()}
	}
	return &DiagnosticError{Code: "diagnostic_failed", Message: err.Error()}
}

func diagnosticHTTPStatus(result DiagnosticResult) int {
	if result.OK {
		return http.StatusOK
	}
	if result.HTTPStatus >= 400 {
		return http.StatusOK
	}
	return http.StatusOK
}
```

Modify `internal/admin/upstream_models.go` by adding a provider projection method used by diagnostics:

```go
func (s *Store) getDiagnosticProvider(ctx context.Context, id string) (*registry.Provider, error) {
	if id == "" {
		return nil, ErrValidation
	}
	var (
		p             registry.Provider
		proto         string
		apiKey        []byte
		proxyURL      *string
		timeoutMS     int
		connTimeoutMS int
		maxRetries    int
		metadata      []byte
	)
	err := s.pool.QueryRow(ctx, `
		SELECT p.id, p.name, p.protocol, p.base_url, p.api_key_enc,
		       pe.url, p.timeout_ms, p.connect_timeout_ms, p.max_retries, p.metadata
		FROM providers p
		LEFT JOIN proxy_egress pe ON pe.id = p.proxy_id AND pe.enabled = true
		WHERE p.id = $1`, id).Scan(
		&p.ID, &p.Name, &proto, &p.BaseURL, &apiKey, &proxyURL,
		&timeoutMS, &connTimeoutMS, &maxRetries, &metadata,
	)
	if err != nil {
		return nil, err
	}
	p.Protocol = adapter.Protocol(proto)
	p.APIKey = string(apiKey)
	p.Timeout = time.Duration(timeoutMS) * time.Millisecond
	p.ConnectTimeout = time.Duration(connTimeoutMS) * time.Millisecond
	p.MaxRetries = maxRetries
	if proxyURL != nil {
		p.ProxyURL = *proxyURL
	}
	p.Headers = extractProviderHeaders(metadata)
	return &p, nil
}

func extractProviderHeaders(metadata []byte) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	var doc struct {
		Headers map[string]string `json:"headers"`
	}
	if json.Unmarshal(metadata, &doc) != nil {
		return nil
	}
	return doc.Headers
}
```

Use existing imports in `upstream_models.go`; add `github.com/aigateway/ai-hub/internal/adapter` and `github.com/aigateway/ai-hub/internal/registry` if missing.

- [ ] **Step 4: Run helper tests and verify they pass**

Run:

```bash
gofmt -w internal/admin/diagnostics.go internal/admin/diagnostics_test.go internal/admin/upstream_models.go
GOCACHE=/tmp/go-build go test ./internal/admin -run 'TestDiagnosticsMinimalRequestByProtocol|TestDiagnosticsPreviewTextCapsOutput' -v
```

Expected: PASS for both tests.

- [ ] **Step 5: Commit Task 1**

```bash
git add internal/admin/diagnostics.go internal/admin/diagnostics_test.go internal/admin/upstream_models.go
git commit -m "feat: add diagnostics core helpers"
```

---

### Task 2: Direct Provider Upstream Test

**Files:**
- Modify: `internal/admin/diagnostics.go`
- Modify: `internal/admin/diagnostics_test.go`

- [ ] **Step 1: Write failing direct upstream test**

Append this test to `internal/admin/diagnostics_test.go`:

```go
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
```

Add these imports to `internal/admin/diagnostics_test.go`:

```go
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
```

- [ ] **Step 2: Run test and verify it fails**

Run:

```bash
GOCACHE=/tmp/go-build go test ./internal/admin -run TestDiagnosticsDirectUpstreamUsesProviderProtocol -v
```

Expected: FAIL with `d.testProviderUpstream undefined`.

- [ ] **Step 3: Implement direct upstream execution**

Add this method to `internal/admin/diagnostics.go`:

```go
func (d *Diagnostics) TestProviderUpstream(ctx context.Context, providerID string, in UpstreamTestInput) DiagnosticResult {
	start := time.Now()
	base := DiagnosticResult{Mode: "upstream", ProviderID: providerID, UpstreamModel: in.UpstreamModel}
	if d == nil || d.store == nil {
		return diagnosticFailure("upstream", start, base, fmt.Errorf("admin diagnostics: %w", ErrValidation))
	}
	p, err := d.store.getDiagnosticProvider(ctx, providerID)
	if err != nil {
		return diagnosticFailure("upstream", start, base, err)
	}
	return d.testProviderUpstream(ctx, p, in)
}

func (d *Diagnostics) testProviderUpstream(ctx context.Context, p *registry.Provider, in UpstreamTestInput) DiagnosticResult {
	start := time.Now()
	base := DiagnosticResult{
		Mode:          "upstream",
		ProviderID:    p.ID,
		ProviderName:  p.Name,
		Protocol:      string(p.Protocol),
		UpstreamModel: in.UpstreamModel,
	}
	if p == nil || strings.TrimSpace(in.UpstreamModel) == "" {
		return diagnosticFailure("upstream", start, base, ErrValidation)
	}
	raw, err := buildDiagnosticWireRequest(p.Protocol, in.UpstreamModel, in.Message)
	if err != nil {
		return diagnosticFailure("upstream", start, base, err)
	}
	adp, ok := d.adapters.Get(p.Protocol)
	if !ok {
		return diagnosticFailure("upstream", start, base, fmt.Errorf("admin diagnostics: protocol_disabled: %s", p.Protocol))
	}
	req, err := adp.DecodeRequest(raw, nil)
	if err != nil {
		return diagnosticFailure("upstream", start, base, fmt.Errorf("decode_failed: %w", err))
	}
	req.Model = in.UpstreamModel
	req.Stream = false
	req.ID = "admin-diagnostic"
	ch := &registry.Channel{Alias: in.UpstreamModel, Provider: p, UpstreamModel: in.UpstreamModel, Weight: 1}
	callCtx, cancel := diagnosticTimeout(ctx, in.TimeoutMs)
	defer cancel()
	resp, err := d.eg.Send(callCtx, req, ch)
	if err != nil {
		return diagnosticFailure("upstream", start, base, err)
	}
	usage := resp.Usage
	return DiagnosticResult{
		OK:              true,
		Mode:            "upstream",
		ProviderID:      p.ID,
		ProviderName:    p.Name,
		Protocol:        string(p.Protocol),
		UpstreamModel:   in.UpstreamModel,
		LatencyMs:       int(time.Since(start).Milliseconds()),
		HTTPStatus:      http.StatusOK,
		StopReason:      string(resp.StopReason),
		Usage:           &usage,
		ResponsePreview: diagnosticPreview(resp, diagnosticPreviewLimit),
	}
}
```

- [ ] **Step 4: Run direct upstream test and admin package tests**

Run:

```bash
gofmt -w internal/admin/diagnostics.go internal/admin/diagnostics_test.go
GOCACHE=/tmp/go-build go test ./internal/admin -run 'TestDiagnosticsDirectUpstreamUsesProviderProtocol|TestDiagnosticsMinimalRequestByProtocol|TestDiagnosticsPreviewTextCapsOutput' -v
```

Expected: PASS.

- [ ] **Step 5: Commit Task 2**

```bash
git add internal/admin/diagnostics.go internal/admin/diagnostics_test.go
git commit -m "feat: test providers against upstream"
```

---

### Task 3: Gateway Path Diagnostics and Admin Routes

**Files:**
- Modify: `internal/admin/diagnostics.go`
- Modify: `internal/admin/diagnostics_test.go`
- Modify: `internal/admin/admin.go`
- Modify: `cmd/gateway/main.go`

- [ ] **Step 1: Write failing gateway forced-channel test**

Append this test to `internal/admin/diagnostics_test.go`:

```go
func TestDiagnosticsGatewayForcedChannelUsesSelectedProvider(t *testing.T) {
	var hitProvider string
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitProvider = "a"
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_a","object":"chat.completion","model":"model-a","choices":[{"index":0,"message":{"role":"assistant","content":"from-a"},"finish_reason":"stop"}]}`))
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitProvider = "b"
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_b","object":"chat.completion","model":"model-b","choices":[{"index":0,"message":{"role":"assistant","content":"from-b"},"finish_reason":"stop"}]}`))
	}))
	defer srvB.Close()

	reg := adapter.NewRegistry(openaichat.New())
	pA := &registry.Provider{ID: "pa", Name: "A", Protocol: adapter.ProtocolChat, BaseURL: srvA.URL, APIKey: "sk-a", Timeout: 5 * time.Second}
	pB := &registry.Provider{ID: "pb", Name: "B", Protocol: adapter.ProtocolChat, BaseURL: srvB.URL, APIKey: "sk-b", Timeout: 5 * time.Second}
	snap := registry.NewBuilder().
		AddChannel(&registry.Channel{Alias: "alias", Provider: pA, UpstreamModel: "model-a", Weight: 1}).
		AddChannel(&registry.Channel{Alias: "alias", Provider: pB, UpstreamModel: "model-b", Weight: 1}).
		Build()
	pipe := pipeline.New(router.New(registry.NewStatic(snap)), egress.New(reg))
	d := NewDiagnostics(nil, reg, pipe)

	result := d.TestGatewayPath(context.Background(), GatewayTestInput{
		ClientProtocol: string(adapter.ProtocolChat),
		Alias:          "alias",
		ProviderID:     "pb",
		UpstreamModel:  "model-b",
		Message:        "ping",
	})

	if !result.OK {
		t.Fatalf("result failed: %+v", result)
	}
	if hitProvider != "b" || result.ProviderID != "pb" || result.ResponsePreview != "from-b" {
		t.Fatalf("forced channel not used: hit=%s result=%+v", hitProvider, result)
	}
}
```

Add imports if absent:

```go
	"github.com/aigateway/ai-hub/internal/egress"
	"github.com/aigateway/ai-hub/internal/pipeline"
	"github.com/aigateway/ai-hub/internal/router"
```

- [ ] **Step 2: Run test and verify it fails**

Run:

```bash
GOCACHE=/tmp/go-build go test ./internal/admin -run TestDiagnosticsGatewayForcedChannelUsesSelectedProvider -v
```

Expected: FAIL with `TestGatewayPath undefined`.

- [ ] **Step 3: Implement gateway-path diagnostics**

Add these methods to `internal/admin/diagnostics.go`:

```go
func (d *Diagnostics) TestGatewayPath(ctx context.Context, in GatewayTestInput) DiagnosticResult {
	start := time.Now()
	base := DiagnosticResult{
		Mode:           "gateway",
		ClientProtocol: in.ClientProtocol,
		Alias:          in.Alias,
		ProviderID:     in.ProviderID,
		UpstreamModel:  in.UpstreamModel,
	}
	if d == nil || d.pipe == nil || d.adapters == nil {
		return diagnosticFailure("gateway", start, base, ErrValidation)
	}
	proto := adapter.Protocol(in.ClientProtocol)
	if proto == "" {
		proto = adapter.ProtocolChat
	}
	ingress, ok := d.adapters.Get(proto)
	if !ok {
		return diagnosticFailure("gateway", start, base, fmt.Errorf("protocol_disabled: %s", proto))
	}
	if strings.TrimSpace(in.Alias) == "" {
		return diagnosticFailure("gateway", start, base, ErrValidation)
	}
	raw, err := buildDiagnosticWireRequest(proto, in.Alias, in.Message)
	if err != nil {
		return diagnosticFailure("gateway", start, base, err)
	}
	req, err := ingress.DecodeRequest(raw, nil)
	if err != nil {
		return diagnosticFailure("gateway", start, base, fmt.Errorf("decode_failed: %w", err))
	}
	req.ID = "admin-diagnostic"
	req.Model = in.Alias
	req.Stream = false
	callCtx, cancel := diagnosticTimeout(ctx, in.TimeoutMs)
	defer cancel()

	if in.ProviderID != "" {
		ch, err := d.findForcedChannel(in.Alias, in.ProviderID, in.UpstreamModel)
		if err != nil {
			return diagnosticFailure("gateway", start, base, err)
		}
		resp, err := d.eg.Send(callCtx, req, ch)
		if err != nil {
			return diagnosticFailure("gateway", start, base, err)
		}
		return diagnosticSuccessGateway(start, proto, in.Alias, ch, resp)
	}

	resp, err := d.pipe.Run(callCtx, req)
	if err != nil {
		return diagnosticFailure("gateway", start, base, err)
	}
	return DiagnosticResult{
		OK:              true,
		Mode:            "gateway",
		ClientProtocol:  string(proto),
		Alias:           in.Alias,
		ProviderID:      resp.ProviderID,
		UpstreamModel:   resp.UpstreamModel,
		LatencyMs:       int(time.Since(start).Milliseconds()),
		HTTPStatus:      http.StatusOK,
		StopReason:      string(resp.StopReason),
		Usage:           &resp.Usage,
		ResponsePreview: diagnosticPreview(resp, diagnosticPreviewLimit),
	}
}

func (d *Diagnostics) findForcedChannel(alias, providerID, upstreamModel string) (*registry.Channel, error) {
	snap, err := d.pipe.Snapshot()
	if err != nil {
		return nil, err
	}
	for _, ch := range snap.ChannelsFor(alias) {
		if ch.Provider == nil || ch.Provider.ID != providerID {
			continue
		}
		if upstreamModel != "" && ch.UpstreamModel != upstreamModel {
			continue
		}
		return ch, nil
	}
	return nil, fmt.Errorf("admin diagnostics: %w: %s/%s", ErrNotFound, alias, providerID)
}

func diagnosticSuccessGateway(start time.Time, proto adapter.Protocol, alias string, ch *registry.Channel, resp *ir.UnifiedResponse) DiagnosticResult {
	usage := resp.Usage
	providerName := ""
	providerID := resp.ProviderID
	upstreamModel := resp.UpstreamModel
	if ch != nil {
		upstreamModel = ch.UpstreamModel
		if ch.Provider != nil {
			providerID = ch.Provider.ID
			providerName = ch.Provider.Name
		}
	}
	return DiagnosticResult{
		OK:              true,
		Mode:            "gateway",
		ClientProtocol:  string(proto),
		Alias:           alias,
		ProviderID:      providerID,
		ProviderName:    providerName,
		UpstreamModel:   upstreamModel,
		LatencyMs:       int(time.Since(start).Milliseconds()),
		HTTPStatus:      http.StatusOK,
		StopReason:      string(resp.StopReason),
		Usage:           &usage,
		ResponsePreview: diagnosticPreview(resp, diagnosticPreviewLimit),
	}
}
```

- [ ] **Step 4: Add admin route registration with optional diagnostics**

Modify `internal/admin/admin.go`:

```go
type MountOption func(*mountOptions)

type mountOptions struct {
	diagnostics *Diagnostics
}

func WithDiagnostics(d *Diagnostics) MountOption {
	return func(o *mountOptions) {
		o.diagnostics = d
	}
}

func Mount(app *fiber.App, st *Store, token string, opts ...MountOption) {
	if token == "" {
		return
	}
	var mo mountOptions
	for _, opt := range opts {
		opt(&mo)
	}
	g := app.Group("/api/admin", authMiddleware(token))
```

Inside the provider route section, after `g.Get("/providers/:id/upstream-models", ...)`, add:

```go
	if mo.diagnostics != nil {
		g.Post("/providers/:id/test-upstream", func(c *fiber.Ctx) error {
			var in UpstreamTestInput
			if err := c.BodyParser(&in); err != nil {
				return c.Status(400).JSON(errMap("bad_request", err.Error()))
			}
			result := mo.diagnostics.TestProviderUpstream(c.UserContext(), c.Params("id"), in)
			return c.Status(diagnosticHTTPStatus(result)).JSON(result)
		})
		g.Post("/test-gateway", func(c *fiber.Ctx) error {
			var in GatewayTestInput
			if err := c.BodyParser(&in); err != nil {
				return c.Status(400).JSON(errMap("bad_request", err.Error()))
			}
			result := mo.diagnostics.TestGatewayPath(c.UserContext(), in)
			return c.Status(diagnosticHTTPStatus(result)).JSON(result)
		})
	}
```

Modify `cmd/gateway/main.go` admin mount:

```go
	admin.Mount(app, admin.NewStore(pool, rdb), cfg.AdminToken,
		admin.WithDiagnostics(admin.NewDiagnostics(admin.NewStore(pool, rdb), registryHub, pipe)),
	)
```

Then replace the double `NewStore` allocation with a local:

```go
	st := admin.NewStore(pool, rdb)
	admin.Mount(app, st, cfg.AdminToken,
		admin.WithDiagnostics(admin.NewDiagnostics(st, registryHub, pipe)),
	)
```

- [ ] **Step 5: Run targeted Go tests**

Run:

```bash
gofmt -w internal/admin/diagnostics.go internal/admin/diagnostics_test.go internal/admin/admin.go cmd/gateway/main.go
GOCACHE=/tmp/go-build go test ./internal/admin -run 'TestDiagnosticsGatewayForcedChannelUsesSelectedProvider|TestDiagnosticsDirectUpstreamUsesProviderProtocol|TestDiagnosticsMinimalRequestByProtocol|TestDiagnosticsPreviewTextCapsOutput' -v
```

Expected: PASS.

- [ ] **Step 6: Commit Task 3**

```bash
git add internal/admin/diagnostics.go internal/admin/diagnostics_test.go internal/admin/admin.go cmd/gateway/main.go
git commit -m "feat: expose admin diagnostics endpoints"
```

---

### Task 4: Provider Page Diagnostics UI

**Files:**
- Modify: `apps/web/lib/types.ts`
- Modify: `apps/web/lib/query-keys.ts`
- Create: `apps/web/components/diagnostics/diagnostic-result.tsx`
- Create: `apps/web/app/(admin)/providers/provider-diagnostics-sheet.tsx`
- Modify: `apps/web/app/(admin)/providers/columns.tsx`
- Modify: `apps/web/app/(admin)/providers/page.tsx`

- [ ] **Step 1: Add frontend types**

Append these types to `apps/web/lib/types.ts`:

```ts
export interface DiagnosticError {
  code: string;
  message: string;
  body_preview?: string;
}

export interface DiagnosticUsage {
  input_tokens: number;
  output_tokens: number;
  cache_creation_tokens?: number;
  cache_read_tokens?: number;
  reasoning_tokens?: number;
}

export interface DiagnosticResult {
  ok: boolean;
  mode: "upstream" | "gateway";
  client_protocol?: string;
  alias?: string;
  provider_id?: string;
  provider_name?: string;
  protocol?: string;
  upstream_model?: string;
  latency_ms: number;
  http_status?: number;
  stop_reason?: string;
  usage?: DiagnosticUsage;
  response_preview?: string;
  error?: DiagnosticError;
}

export interface UpstreamTestInput {
  upstream_model: string;
  message?: string;
  timeout_ms?: number;
}

export interface GatewayTestInput {
  client_protocol: Protocol | string;
  alias: string;
  provider_id?: string;
  upstream_model?: string;
  message?: string;
  timeout_ms?: number;
}
```

Modify `apps/web/lib/query-keys.ts`:

```ts
  providerDiagnostics: (providerId: string) => ["provider-diagnostics", providerId] as const,
```

- [ ] **Step 2: Create shared result component**

Create `apps/web/components/diagnostics/diagnostic-result.tsx`:

```tsx
"use client";

import { CheckCircle2, XCircle } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import type { DiagnosticResult } from "@/lib/types";

export function DiagnosticResultView({ result }: { result: DiagnosticResult | null }) {
  if (!result) return null;
  return (
    <div className="rounded-md border bg-background p-3 text-sm">
      <div className="mb-3 flex items-center justify-between gap-2">
        <Badge variant={result.ok ? "default" : "destructive"} className="gap-1">
          {result.ok ? <CheckCircle2 className="size-3.5" /> : <XCircle className="size-3.5" />}
          {result.ok ? "成功" : "失败"}
        </Badge>
        <span className="text-xs text-muted-foreground tabular-nums">
          {result.latency_ms}ms
          {result.http_status ? ` · HTTP ${result.http_status}` : ""}
        </span>
      </div>
      <dl className="grid grid-cols-2 gap-x-3 gap-y-2 text-xs">
        <dt className="text-muted-foreground">模式</dt>
        <dd className="font-mono">{result.mode}</dd>
        <dt className="text-muted-foreground">供应商</dt>
        <dd>{result.provider_name || result.provider_id || "-"}</dd>
        <dt className="text-muted-foreground">上游模型</dt>
        <dd className="font-mono break-all">{result.upstream_model || "-"}</dd>
        {result.client_protocol && (
          <>
            <dt className="text-muted-foreground">客户端协议</dt>
            <dd className="font-mono">{result.client_protocol}</dd>
          </>
        )}
        {result.stop_reason && (
          <>
            <dt className="text-muted-foreground">停止原因</dt>
            <dd className="font-mono">{result.stop_reason}</dd>
          </>
        )}
        {result.usage && (
          <>
            <dt className="text-muted-foreground">Token</dt>
            <dd className="tabular-nums">
              in {result.usage.input_tokens} / out {result.usage.output_tokens}
            </dd>
          </>
        )}
      </dl>
      {result.response_preview && (
        <pre className="mt-3 max-h-32 overflow-auto rounded-md bg-muted p-2 text-xs whitespace-pre-wrap">
          {result.response_preview}
        </pre>
      )}
      {result.error && (
        <div className="mt-3 rounded-md border border-destructive/30 bg-destructive/5 p-2 text-xs text-destructive">
          <div className="font-mono">{result.error.code}</div>
          <div className="mt-1">{result.error.message}</div>
          {result.error.body_preview && (
            <pre className="mt-2 whitespace-pre-wrap">{result.error.body_preview}</pre>
          )}
        </div>
      )}
    </div>
  );
}
```

- [ ] **Step 3: Create provider diagnostics sheet**

Create `apps/web/app/(admin)/providers/provider-diagnostics-sheet.tsx` with:

```tsx
"use client";

import { useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Loader2, RefreshCw, Send } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { qk } from "@/lib/query-keys";
import type { DiagnosticResult, GatewayTestInput, Model, ModelChannel, Provider, UpstreamModel, UpstreamTestInput } from "@/lib/types";
import { DiagnosticResultView } from "@/components/diagnostics/diagnostic-result";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

const PROTOCOLS = ["openai_chat", "anthropic_messages", "openai_responses"] as const;

export function ProviderDiagnosticsSheet({
  provider,
  open,
  onOpenChange,
  models,
  channels,
}: {
  provider: Provider | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  models: Model[];
  channels: ModelChannel[];
}) {
  const providerId = provider?.id ?? "";
  const [upstreamModel, setUpstreamModel] = useState("");
  const [message, setMessage] = useState("ping");
  const [clientProtocol, setClientProtocol] = useState("openai_chat");
  const [channelKey, setChannelKey] = useState("");
  const [result, setResult] = useState<DiagnosticResult | null>(null);

  const upstreamModels = useQuery({
    queryKey: qk.upstreamModels(providerId),
    queryFn: () => api.list<UpstreamModel>(`/providers/${providerId}/upstream-models`),
    enabled: open && Boolean(providerId),
    retry: false,
  });

  const modelAlias = useMemo(() => {
    const map = new Map<string, string>();
    for (const model of models) if (model.id) map.set(model.id, model.alias);
    return map;
  }, [models]);

  const providerChannels = useMemo(
    () => channels.filter((channel) => channel.provider_id === providerId && channel.enabled),
    [channels, providerId],
  );

  const direct = useMutation({
    mutationFn: (body: UpstreamTestInput) =>
      api.post<DiagnosticResult>(`/providers/${providerId}/test-upstream`, body),
    onSuccess: setResult,
    onError: (e) => toast.error(e instanceof Error ? e.message : "测试失败"),
  });

  const gateway = useMutation({
    mutationFn: (body: GatewayTestInput) => api.post<DiagnosticResult>("/test-gateway", body),
    onSuccess: setResult,
    onError: (e) => toast.error(e instanceof Error ? e.message : "测试失败"),
  });

  const selectedChannel = providerChannels.find((channel) => channel.id === channelKey);

  function runDirect() {
    if (!upstreamModel.trim()) {
      toast.error("请输入上游模型");
      return;
    }
    direct.mutate({ upstream_model: upstreamModel.trim(), message, timeout_ms: 30000 });
  }

  function runGateway() {
    if (!selectedChannel) {
      toast.error("请选择已绑定通道");
      return;
    }
    const alias = modelAlias.get(selectedChannel.model_id);
    if (!alias) {
      toast.error("找不到模型别名");
      return;
    }
    gateway.mutate({
      client_protocol: clientProtocol,
      alias,
      provider_id: selectedChannel.provider_id,
      upstream_model: selectedChannel.upstream_model,
      message,
      timeout_ms: 30000,
    });
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-xl">
        <SheetHeader>
          <SheetTitle>诊断供应商</SheetTitle>
          <SheetDescription>{provider?.name ?? "选择供应商后可测试上游与网关链路。"}</SheetDescription>
        </SheetHeader>
        <div className="mt-5 space-y-5">
          <section className="space-y-2">
            <div className="flex items-center justify-between">
              <Label>支持的上游模型</Label>
              <Button type="button" variant="outline" size="icon-sm" onClick={() => upstreamModels.refetch()} disabled={!providerId || upstreamModels.isFetching}>
                <RefreshCw className={upstreamModels.isFetching ? "size-3.5 animate-spin" : "size-3.5"} />
              </Button>
            </div>
            <Select value={upstreamModel} onValueChange={setUpstreamModel} disabled={(upstreamModels.data ?? []).length === 0}>
              <SelectTrigger>
                <SelectValue placeholder={upstreamModels.isLoading ? "正在拉取模型..." : upstreamModels.isError ? "拉取失败，可手动输入" : "选择上游模型"} />
              </SelectTrigger>
              <SelectContent>
                {(upstreamModels.data ?? []).map((model) => (
                  <SelectItem key={model.id} value={model.id}>
                    <span className="font-mono">{model.id}</span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Input value={upstreamModel} onChange={(e) => setUpstreamModel(e.target.value)} placeholder="手动输入上游模型" />
          </section>

          <section className="space-y-2">
            <Label>测试消息</Label>
            <Input value={message} onChange={(e) => setMessage(e.target.value)} placeholder="ping" />
          </section>

          <section className="space-y-3 rounded-md border p-3">
            <div className="flex items-center justify-between">
              <div>
                <h3 className="text-sm font-medium">直连上游测试</h3>
                <p className="text-xs text-muted-foreground">验证 base_url、API key、代理和上游模型。</p>
              </div>
              <Button type="button" size="sm" onClick={runDirect} disabled={direct.isPending || !providerId}>
                {direct.isPending ? <Loader2 className="size-4 animate-spin" /> : <Send className="size-4" />}
                测试
              </Button>
            </div>
          </section>

          <section className="space-y-3 rounded-md border p-3">
            <div className="grid gap-3">
              <div>
                <Label>已绑定通道</Label>
                <Select value={channelKey} onValueChange={setChannelKey}>
                  <SelectTrigger>
                    <SelectValue placeholder="选择模型通道" />
                  </SelectTrigger>
                  <SelectContent>
                    {providerChannels.map((channel) => (
                      <SelectItem key={channel.id} value={channel.id ?? ""}>
                        {(modelAlias.get(channel.model_id) ?? channel.model_id) + " -> " + channel.upstream_model}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div>
                <Label>客户端协议</Label>
                <Select value={clientProtocol} onValueChange={setClientProtocol}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {PROTOCOLS.map((protocol) => (
                      <SelectItem key={protocol} value={protocol}>{protocol}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
            <Button type="button" size="sm" onClick={runGateway} disabled={gateway.isPending || !providerId}>
              {gateway.isPending ? <Loader2 className="size-4 animate-spin" /> : <Send className="size-4" />}
              经网关测试
            </Button>
          </section>

          <DiagnosticResultView result={result} />
        </div>
      </SheetContent>
    </Sheet>
  );
}
```

- [ ] **Step 4: Wire Provider page action**

Modify `apps/web/app/(admin)/providers/columns.tsx`:

```tsx
import { Activity, Pencil, Trash2 } from "lucide-react";
```

Change function arguments:

```tsx
export function providerColumns({
  onEdit,
  onDelete,
  onDiagnostics,
}: {
  onEdit: (p: Provider) => void;
  onDelete: (p: Provider) => void;
  onDiagnostics: (p: Provider) => void;
}): ColumnDef<Provider>[] {
```

Add the action button before edit:

```tsx
<Button
  variant="ghost"
  size="icon-sm"
  aria-label="诊断"
  onClick={() => onDiagnostics(row.original)}
>
  <Activity className="size-3.5" />
</Button>
```

Modify `apps/web/app/(admin)/providers/page.tsx` imports:

```tsx
import { useQuery } from "@tanstack/react-query";
import type { Model, ModelChannel, Provider } from "@/lib/types";
import { api } from "@/lib/api";
import { ProviderDiagnosticsSheet } from "./provider-diagnostics-sheet";
```

Add queries and state:

```tsx
const models = useQuery({ queryKey: qk.models, queryFn: () => api.list<Model>("/models") });
const channels = useQuery({ queryKey: qk.channels, queryFn: () => api.list<ModelChannel>("/model-channels") });
const [diagnostics, setDiagnostics] = useState<Provider | null>(null);
```

Pass callback:

```tsx
const columns = providerColumns({
  onEdit: startEdit,
  onDelete: setPendingDelete,
  onDiagnostics: setDiagnostics,
});
```

Render the sheet near `ProviderForm`:

```tsx
<ProviderDiagnosticsSheet
  provider={diagnostics}
  open={diagnostics !== null}
  onOpenChange={(o) => !o && setDiagnostics(null)}
  models={models.data ?? []}
  channels={channels.data ?? []}
/>
```

- [ ] **Step 5: Run frontend lint**

Run:

```bash
pnpm lint
```

from `apps/web`.

Expected: exit 0. The existing TanStack Table React Compiler warning in `components/data-table.tsx` may still appear; there must be 0 errors.

- [ ] **Step 6: Commit Task 4**

```bash
git add apps/web/lib/types.ts apps/web/lib/query-keys.ts apps/web/components/diagnostics/diagnostic-result.tsx 'apps/web/app/(admin)/providers/provider-diagnostics-sheet.tsx' 'apps/web/app/(admin)/providers/columns.tsx' 'apps/web/app/(admin)/providers/page.tsx'
git commit -m "feat(web): add provider diagnostics"
```

---

### Task 5: Model Channel Gateway Test UI and Final Verification

**Files:**
- Create: `apps/web/components/diagnostics/gateway-test-sheet.tsx`
- Modify: `apps/web/app/(admin)/models/page.tsx`

- [ ] **Step 1: Create focused gateway test sheet**

Create `apps/web/components/diagnostics/gateway-test-sheet.tsx`:

```tsx
"use client";

import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Loader2, Send } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import type { DiagnosticResult, GatewayTestInput, ModelChannel } from "@/lib/types";
import { DiagnosticResultView } from "@/components/diagnostics/diagnostic-result";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

const PROTOCOLS = ["openai_chat", "anthropic_messages", "openai_responses"] as const;

export function GatewayTestSheet({
  open,
  onOpenChange,
  channel,
  alias,
  providerName,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  channel: ModelChannel | null;
  alias: string | null;
  providerName: string | null;
}) {
  const [clientProtocol, setClientProtocol] = useState("openai_chat");
  const [message, setMessage] = useState("ping");
  const [result, setResult] = useState<DiagnosticResult | null>(null);
  const test = useMutation({
    mutationFn: (body: GatewayTestInput) => api.post<DiagnosticResult>("/test-gateway", body),
    onSuccess: setResult,
    onError: (e) => toast.error(e instanceof Error ? e.message : "测试失败"),
  });

  function run() {
    if (!channel || !alias) {
      toast.error("缺少模型通道");
      return;
    }
    test.mutate({
      client_protocol: clientProtocol,
      alias,
      provider_id: channel.provider_id,
      upstream_model: channel.upstream_model,
      message,
      timeout_ms: 30000,
    });
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-lg">
        <SheetHeader>
          <SheetTitle>测试模型通道</SheetTitle>
          <SheetDescription>
            {alias && channel ? `${alias} -> ${providerName ?? channel.provider_id} / ${channel.upstream_model}` : "选择通道后可测试。"}
          </SheetDescription>
        </SheetHeader>
        <div className="mt-5 space-y-4">
          <div className="space-y-2">
            <Label>客户端协议</Label>
            <Select value={clientProtocol} onValueChange={setClientProtocol}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {PROTOCOLS.map((protocol) => (
                  <SelectItem key={protocol} value={protocol}>{protocol}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-2">
            <Label>测试消息</Label>
            <Input value={message} onChange={(e) => setMessage(e.target.value)} placeholder="ping" />
          </div>
          <Button type="button" size="sm" onClick={run} disabled={test.isPending || !channel || !alias}>
            {test.isPending ? <Loader2 className="size-4 animate-spin" /> : <Send className="size-4" />}
            经网关测试
          </Button>
          <DiagnosticResultView result={result} />
        </div>
      </SheetContent>
    </Sheet>
  );
}
```

- [ ] **Step 2: Add Model page channel test action**

Modify imports in `apps/web/app/(admin)/models/page.tsx`:

```tsx
import { Boxes, Plus, TestTube2, Trash2 } from "lucide-react";
import { GatewayTestSheet } from "@/components/diagnostics/gateway-test-sheet";
```

Add state:

```tsx
const [testingChannel, setTestingChannel] = useState<{
  model: Model;
  channel: ModelChannel;
} | null>(null);
```

In each channel row, add a test button before delete:

```tsx
<Button
  variant="ghost"
  size="icon-sm"
  aria-label="测试通道"
  onClick={() => setTestingChannel({ model, channel: c })}
>
  <TestTube2 className="size-3.5" />
</Button>
```

Render the sheet after `ChannelForm`:

```tsx
<GatewayTestSheet
  open={testingChannel !== null}
  onOpenChange={(o) => !o && setTestingChannel(null)}
  channel={testingChannel?.channel ?? null}
  alias={testingChannel?.model.alias ?? null}
  providerName={
    testingChannel
      ? providerName.get(testingChannel.channel.provider_id) ?? null
      : null
  }
/>
```

- [ ] **Step 3: Run frontend lint and build**

Run from `apps/web`:

```bash
pnpm lint
pnpm build
```

Expected:

- `pnpm lint` exits 0. Existing React Compiler warning for TanStack Table is acceptable if there are 0 errors.
- `pnpm build` exits 0.

- [ ] **Step 4: Run backend package tests**

Run from repo root:

```bash
GOCACHE=/tmp/go-build go test ./internal/admin ./internal/pipeline ./internal/egress ./internal/server -v
```

Expected: PASS. Integration tests that require `GATEWAY_TEST_POSTGRES_DSN` / `GATEWAY_TEST_REDIS_ADDR` may skip when those variables are absent.

- [ ] **Step 5: Run final status check**

Run:

```bash
git status --short
```

Expected: only intended feature files are modified or staged. Existing pre-feature font/Turbopack-root files may remain modified if they have not been committed separately:

```text
 M apps/web/app/globals.css
 M apps/web/app/layout.tsx
 M apps/web/next.config.ts
```

Do not include those three files in diagnostics commits unless the user explicitly asks to fold the earlier dev-server fix into the branch.

- [ ] **Step 6: Commit Task 5**

```bash
git add apps/web/components/diagnostics/gateway-test-sheet.tsx 'apps/web/app/(admin)/models/page.tsx'
git commit -m "feat(web): test configured model channels"
```

---

## Self-Review Checklist

- Spec coverage:
  - Provider model discovery on Provider page: Task 4.
  - Direct provider upstream test: Tasks 1 and 2.
  - Gateway-path test with optional forced provider/channel: Task 3.
  - Provider page full workflow: Task 4.
  - Model channel row test action: Task 5.
  - Result display with redaction and compact error detail: Tasks 1, 2, 4, 5.
  - Non-streaming first version: Tasks 1-5 only build non-streaming requests.
- Type consistency:
  - Backend request/result names: `UpstreamTestInput`, `GatewayTestInput`, `DiagnosticResult`.
  - Frontend request/result names match backend JSON fields.
  - Route paths match spec: `/providers/:id/test-upstream` and `/test-gateway` behind the BFF `/api/admin` prefix.
- Verification:
  - Go targeted tests after backend tasks.
  - `pnpm lint`, `pnpm build`, and selected Go package tests after UI tasks.


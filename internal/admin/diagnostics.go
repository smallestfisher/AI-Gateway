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
	return http.StatusOK
}

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
	if p == nil {
		return diagnosticFailure("upstream", start, DiagnosticResult{
			Mode:          "upstream",
			UpstreamModel: in.UpstreamModel,
		}, ErrValidation)
	}
	base := DiagnosticResult{
		Mode:          "upstream",
		ProviderID:    p.ID,
		ProviderName:  p.Name,
		Protocol:      string(p.Protocol),
		UpstreamModel: in.UpstreamModel,
	}
	if d == nil || d.adapters == nil || d.eg == nil || strings.TrimSpace(in.UpstreamModel) == "" {
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

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
	Stream        bool   `json:"stream"`
}

type GatewayTestInput struct {
	ClientProtocol string `json:"client_protocol"`
	Alias          string `json:"alias"`
	ProviderID     string `json:"provider_id"`
	UpstreamModel  string `json:"upstream_model"`
	Message        string `json:"message"`
	TimeoutMs      int    `json:"timeout_ms"`
	Stream         bool   `json:"stream"`
}

type DiagnosticResult struct {
	OK              bool             `json:"ok"`
	Mode            string           `json:"mode"`
	RequestID       string           `json:"request_id,omitempty"`
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

func newDiagnosticRequestID() string {
	return fmt.Sprintf("admin-diagnostic-%d", time.Now().UnixNano())
}

func (d *Diagnostics) TestProviderUpstream(ctx context.Context, providerID string, in UpstreamTestInput) DiagnosticResult {
	start := time.Now()
	base := DiagnosticResult{Mode: "upstream", RequestID: newDiagnosticRequestID(), ProviderID: providerID, UpstreamModel: in.UpstreamModel}
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
	requestID := newDiagnosticRequestID()
	if p == nil {
		return diagnosticFailure("upstream", start, DiagnosticResult{
			Mode:          "upstream",
			RequestID:     requestID,
			UpstreamModel: in.UpstreamModel,
		}, ErrValidation)
	}
	base := DiagnosticResult{
		Mode:          "upstream",
		RequestID:     requestID,
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
	req.ID = requestID
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
		RequestID:       requestID,
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

func (d *Diagnostics) TestGatewayPath(ctx context.Context, in GatewayTestInput) DiagnosticResult {
	start := time.Now()
	requestID := newDiagnosticRequestID()
	base := DiagnosticResult{
		Mode:           "gateway",
		RequestID:      requestID,
		ClientProtocol: in.ClientProtocol,
		Alias:          in.Alias,
		ProviderID:     in.ProviderID,
		UpstreamModel:  in.UpstreamModel,
	}
	if d == nil || d.pipe == nil || d.adapters == nil || d.eg == nil {
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
	req.ID = requestID
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
		return diagnosticSuccessGateway(start, requestID, proto, in.Alias, ch, resp)
	}

	resp, err := d.pipe.Run(callCtx, req)
	if err != nil {
		return diagnosticFailure("gateway", start, base, err)
	}
	return DiagnosticResult{
		OK:              true,
		Mode:            "gateway",
		RequestID:       requestID,
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

func diagnosticSuccessGateway(start time.Time, requestID string, proto adapter.Protocol, alias string, ch *registry.Channel, resp *ir.UnifiedResponse) DiagnosticResult {
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
		RequestID:       requestID,
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

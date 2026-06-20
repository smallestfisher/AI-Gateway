// Package egress sends a UnifiedRequest to an upstream provider and returns a
// UnifiedResponse (or a stream of IR events). It owns the transport concerns:
// URL assembly, auth, Client-Profile header injection, timeouts, proxy egress,
// and SSE forwarding. Protocol body shaping is delegated to the channel's
// Adapter. See docs/02-modules.md §3.
package egress

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/aigateway/ai-hub/internal/adapter"
	"github.com/aigateway/ai-hub/internal/ir"
	"github.com/aigateway/ai-hub/internal/registry"
)

// Recorder observes request outcomes for health/metrics. health.Store
// implements it; nil means no recording.
type Recorder interface {
	Record(ctx context.Context, providerID, model string, success bool, ttftMs, latencyMs int) error
}

// Egress sends requests to upstreams.
type Egress struct {
	adapters *adapter.Registry
	pool     *TransportPool
	rec      Recorder
}

// New creates an Egress over the given adapter registry.
func New(reg *adapter.Registry) *Egress {
	return &Egress{adapters: reg, pool: NewTransportPool()}
}

// SetRecorder attaches a health/metrics recorder.
func (e *Egress) SetRecorder(r Recorder) { e.rec = r }

// record reports an outcome if a recorder is attached.
func (e *Egress) record(ctx context.Context, ch *registry.Channel, success bool, ttftMs, latencyMs int) {
	if e.rec == nil {
		return
	}
	_ = e.rec.Record(ctx, ch.Provider.ID, ch.UpstreamModel, success, ttftMs, latencyMs)
}

// Send performs a non-streaming upstream call and returns the decoded IR response.
func (e *Egress) Send(ctx context.Context, req *ir.UnifiedRequest, ch *registry.Channel) (*ir.UnifiedResponse, error) {
	adp, err := e.adapterFor(ch)
	if err != nil {
		return nil, err
	}
	req.Stream = false // non-streaming call
	up, err := adp.BuildUpstream(req, ch.UpstreamModel)
	if err != nil {
		return nil, fmt.Errorf("egress: build upstream: %w", err)
	}

	httpReq, err := e.buildRequest(ctx, ch, up, false)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	resp, err := e.clientFor(ch).Do(httpReq)
	if err != nil {
		e.record(ctx, ch, false, int(time.Since(start).Milliseconds()), int(time.Since(start).Milliseconds()))
		return nil, &UpstreamError{Code: "upstream_unreachable", Retryable: true, Cause: err}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		e.record(ctx, ch, false, 0, int(time.Since(start).Milliseconds()))
		return nil, &UpstreamError{Code: "upstream_read_failed", Retryable: true, Cause: err}
	}
	if resp.StatusCode >= 500 {
		e.record(ctx, ch, false, 0, int(time.Since(start).Milliseconds()))
		return nil, &UpstreamError{Code: fmt.Sprintf("upstream_%d", resp.StatusCode), Retryable: true, Status: resp.StatusCode, Body: body}
	}
	if resp.StatusCode >= 400 {
		e.record(ctx, ch, false, 0, int(time.Since(start).Milliseconds()))
		return nil, &UpstreamError{Code: fmt.Sprintf("upstream_%d", resp.StatusCode), Retryable: false, Status: resp.StatusCode, Body: body}
	}
	iresp, err := adp.DecodeUpstreamResponse(body)
	if err != nil {
		e.record(ctx, ch, false, 0, int(time.Since(start).Milliseconds()))
		return nil, fmt.Errorf("egress: decode upstream response: %w", err)
	}
	iresp.ProviderID = ch.Provider.ID
	iresp.UpstreamModel = ch.UpstreamModel
	e.record(ctx, ch, true, int(time.Since(start).Milliseconds()), int(time.Since(start).Milliseconds()))
	return iresp, nil
}

// SendStream performs a streaming upstream call, emitting IR events via emit.
// The first emit corresponds to the first upstream token; the caller treats
// any error before the first emit as failover-eligible.
func (e *Egress) SendStream(ctx context.Context, req *ir.UnifiedRequest, ch *registry.Channel, emit func(ir.StreamEvent)) (err error) {
	start := time.Now()
	var firstEvent time.Time
	wrappedEmit := func(ev ir.StreamEvent) {
		if firstEvent.IsZero() {
			firstEvent = time.Now()
		}
		emit(ev)
	}
	defer func() {
		ttft := 0
		if !firstEvent.IsZero() {
			ttft = int(firstEvent.Sub(start).Milliseconds())
		}
		e.record(ctx, ch, err == nil, ttft, int(time.Since(start).Milliseconds()))
	}()

	adp, err := e.adapterFor(ch)
	if err != nil {
		return err
	}
	req.Stream = true
	up, err := adp.BuildUpstream(req, ch.UpstreamModel)
	if err != nil {
		return fmt.Errorf("egress: build upstream: %w", err)
	}
	httpReq, err := e.buildRequest(ctx, ch, up, true)
	if err != nil {
		return err
	}
	resp, err := e.clientFor(ch).Do(httpReq)
	if err != nil {
		return &UpstreamError{Code: "upstream_unreachable", Retryable: true, Cause: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		retryable := resp.StatusCode >= 500
		return &UpstreamError{Code: fmt.Sprintf("upstream_%d", resp.StatusCode), Retryable: retryable, Status: resp.StatusCode, Body: body}
	}

	dec := adp.NewStreamDecoder()
	br := bufio.NewReaderSize(resp.Body, 64*1024)
	var pending strings.Builder
	flushPending := func() error {
		data := pending.String()
		pending.Reset()
		if data == "" {
			return nil
		}
		evs, derr := dec.Feed([]byte(data))
		if derr != nil {
			return derr
		}
		for _, ev := range evs {
			wrappedEmit(ev)
		}
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line, rerr := br.ReadString('\n')
		trimmed := strings.TrimRight(line, "\r\n")

		if trimmed == "" {
			// event boundary: deliver accumulated data
			if rerr == io.EOF {
				if ferr := flushPending(); ferr != nil {
					return ferr
				}
				break
			}
			if ferr := flushPending(); ferr != nil {
				return ferr
			}
			continue
		}
		if strings.HasPrefix(trimmed, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
			if payload == "[DONE]" {
				if ferr := flushPending(); ferr != nil {
					return ferr
				}
				break
			}
			if pending.Len() > 0 {
				pending.WriteByte('\n')
			}
			pending.WriteString(payload)
		}
		// other SSE fields (event:, id:, comments) are ignored; the JSON
		// payload carries the type for both Chat and Messages.

		if rerr == io.EOF {
			if ferr := flushPending(); ferr != nil {
				return ferr
			}
			break
		}
		if rerr != nil {
			return &UpstreamError{Code: "upstream_read_failed", Retryable: false, Cause: rerr}
		}
	}

	evs, ferr := dec.Finalize()
	if ferr != nil {
		return ferr
	}
	for _, ev := range evs {
		emit(ev)
	}
	return nil
}

func (e *Egress) adapterFor(ch *registry.Channel) (adapter.Adapter, error) {
	adp, ok := e.adapters.Get(ch.Provider.Protocol)
	if !ok {
		return nil, fmt.Errorf("egress: no adapter for protocol %s", ch.Provider.Protocol)
	}
	return adp, nil
}

func (e *Egress) buildRequest(ctx context.Context, ch *registry.Channel, up *adapter.UpstreamRequest, stream bool) (*http.Request, error) {
	fullURL := strings.TrimRight(ch.Provider.BaseURL, "/") + up.Path
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(up.Body))
	if err != nil {
		return nil, err
	}
	hdr := http.Header{}
	for k, v := range authHeaders(ch.Provider.Protocol, ch.Provider.APIKey) {
		hdr.Set(k, v)
	}
	for k, v := range ch.Provider.Headers {
		hdr.Set(k, v)
	}
	if ch.Profile != nil {
		if ch.Profile.StripClientHeaders {
			// (client headers are never on this server-side request anyway)
		}
		for k, v := range ch.Profile.Headers {
			hdr.Set(k, v)
		}
		if ch.Profile.UserAgent != "" {
			hdr.Set("User-Agent", ch.Profile.UserAgent)
		}
		if ch.Profile.Origin != "" {
			hdr.Set("Origin", ch.Profile.Origin)
		}
		if ch.Profile.Referer != "" {
			hdr.Set("Referer", ch.Profile.Referer)
		}
	}
	hdr.Set("Content-Type", "application/json")
	if stream {
		hdr.Set("Accept", "text/event-stream")
	}
	httpReq.Header = hdr
	return httpReq, nil
}

func (e *Egress) clientFor(ch *registry.Channel) *http.Client {
	return e.pool.Client(ch.Provider)
}

// authHeaders returns protocol-appropriate auth headers.
func authHeaders(p adapter.Protocol, key string) map[string]string {
	switch p {
	case adapter.ProtocolMessages:
		return map[string]string{
			"x-api-key":         key,
			"anthropic-version": "2023-06-01",
		}
	default: // openai_chat, openai_responses
		return map[string]string{"Authorization": "Bearer " + key}
	}
}

// UpstreamError wraps a failed upstream interaction. Retryable drives failover.
type UpstreamError struct {
	Code      string
	Retryable bool
	Status    int
	Body      []byte
	Cause     error
}

func (e *UpstreamError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("egress: %s: %v", e.Code, e.Cause)
	}
	return fmt.Sprintf("egress: %s (status %d)", e.Code, e.Status)
}

func (e *UpstreamError) Unwrap() error { return e.Cause }

// IsRetryable reports whether err is a retryable upstream error.
func IsRetryable(err error) bool {
	var ue *UpstreamError
	if errors.As(err, &ue) {
		return ue.Retryable
	}
	return false
}

// ---------------------------------------------------------------------------
// TransportPool
// ---------------------------------------------------------------------------

// TransportPool maintains one *http.Client per (provider, proxy) so connections
// are reused and timeouts/proxies are isolated per upstream.
type TransportPool struct {
	mu      sync.RWMutex
	clients map[string]*http.Client
}

// NewTransportPool creates an empty pool.
func NewTransportPool() *TransportPool {
	return &TransportPool{clients: map[string]*http.Client{}}
}

// Client returns (creating if needed) the HTTP client for a provider.
func (p *TransportPool) Client(prov *registry.Provider) *http.Client {
	key := prov.ID + "|" + prov.ProxyURL
	p.mu.RLock()
	if c, ok := p.clients[key]; ok {
		p.mu.RUnlock()
		return c
	}
	p.mu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.clients[key]; ok {
		return c
	}
	timeout := prov.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	tr := &http.Transport{
		MaxIdleConns:        256,
		MaxIdleConnsPerHost: 128,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   true,
	}
	if prov.ProxyURL != "" {
		if u, err := url.Parse(prov.ProxyURL); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	c := &http.Client{Transport: tr, Timeout: timeout}
	p.clients[key] = c
	return c
}

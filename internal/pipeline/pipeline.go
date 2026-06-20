// Package pipeline orchestrates a request end to end: Router (alias -> ordered
// candidate channels with failover) -> Egress (real upstream call) -> the
// caller encodes the result back to the client protocol. Streaming failover is
// allowed only before the first event is emitted to the caller; once the first
// IR event is emitted the stream is committed and subsequent errors terminate
// it (see docs/01-architecture.md §2, docs/07-streaming.md §4).
package pipeline

import (
	"context"
	"errors"

	"github.com/aigateway/ai-hub/internal/egress"
	"github.com/aigateway/ai-hub/internal/ir"
	"github.com/aigateway/ai-hub/internal/registry"
	"github.com/aigateway/ai-hub/internal/router"
)

// Pipeline ties routing and egress together.
type Pipeline struct {
	Router *router.Router
	Egress *egress.Egress
}

// New creates a Pipeline.
func New(r *router.Router, e *egress.Egress) *Pipeline {
	return &Pipeline{Router: r, Egress: e}
}

// Run performs a non-streaming request: try candidate channels in failover
// order until one succeeds. Returns the IR response, or the last error.
func (p *Pipeline) Run(ctx context.Context, req *ir.UnifiedRequest) (*ir.UnifiedResponse, error) {
	channels, _, err := p.Router.Resolve(req.Model)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, ch := range channels {
		resp, err := p.Egress.Send(ctx, req, ch)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !egress.IsRetryable(err) {
			break // business error; do not failover
		}
	}
	if lastErr == nil {
		lastErr = errors.New("pipeline: no channel succeeded")
	}
	return nil, lastErr
}

// RunStream performs a streaming request, emitting IR events via emit. It tries
// candidate channels in failover order; the first channel that emits a non-error
// event commits the stream. If a channel fails (or emits only an error) before
// committing, the next channel is tried. Once committed, errors terminate the
// stream via an error event. Returns nil if anything was emitted, else the error.
func (p *Pipeline) RunStream(ctx context.Context, req *ir.UnifiedRequest, emit func(ir.StreamEvent)) error {
	channels, _, err := p.Router.Resolve(req.Model)
	if err != nil {
		return err
	}
	var lastErr error
	for _, ch := range channels {
		committed, cerr := p.tryStream(ctx, req, ch, emit)
		if committed {
			return nil
		}
		if cerr != nil {
			lastErr = cerr
		}
	}
	if lastErr == nil {
		lastErr = errors.New("pipeline: no stream channel succeeded before first byte")
	}
	return lastErr
}

// tryStream attempts one channel. It commits (returns committed=true) as soon as
// a non-error event is emitted to the caller. Returns (false, err) if the
// channel failed before committing, so the caller can failover.
func (p *Pipeline) tryStream(ctx context.Context, req *ir.UnifiedRequest, ch *registry.Channel, emit func(ir.StreamEvent)) (bool, error) {
	var buffer []ir.StreamEvent
	committed := false

	realEmit := func(ev ir.StreamEvent) {
		if committed {
			emit(ev)
			return
		}
		if ev.Type == ir.EvError {
			// pre-commit upstream error: do not commit; caller will failover
			return
		}
		committed = true
		buffer = append(buffer, ev)
		for _, bev := range buffer {
			emit(bev)
		}
		buffer = nil
	}

	err := p.Egress.SendStream(ctx, req, ch, realEmit)
	return committed, err
}

// Package server wires the Fiber HTTP application: health checks and the proxy
// ingress. A request is decoded into the IR, routed through the Pipeline
// (Router + Egress with failover), and re-encoded to the client protocol — for
// both non-streaming and streaming. See docs/01-architecture.md, docs/02-modules.md.
package server

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/aigateway/ai-hub/internal/adapter"
	"github.com/aigateway/ai-hub/internal/auth"
	"github.com/aigateway/ai-hub/internal/config"
	"github.com/aigateway/ai-hub/internal/egress"
	"github.com/aigateway/ai-hub/internal/ir"
	"github.com/aigateway/ai-hub/internal/logging"
	"github.com/aigateway/ai-hub/internal/pipeline"
	"github.com/aigateway/ai-hub/internal/router"

	"github.com/gofiber/fiber/v2"
)

// Deps bundles the server's collaborators.
type Deps struct {
	Registry *adapter.Registry
	Pipeline *pipeline.Pipeline
	Auth     *AuthDeps // nil = auth disabled (dev)
	Logger   *logging.Logger // nil = logging disabled
}

// AuthDeps bundles the proxy auth controls.
type AuthDeps struct {
	Resolver *auth.Resolver
	Limiter  *auth.Limiter
	Quota    *auth.Quota
}

// New builds the Fiber app.
func New(cfg *config.Config, log *slog.Logger, deps Deps) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:      "ai-agent-gateway",
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 0, // streaming responses manage their own write timing
		IdleTimeout:  120 * time.Second,
	})

	app.Use(requestIDMiddleware())
	app.Use(accessLogMiddleware(log))

	app.Get("/healthz", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok", "protocols": deps.Registry.Protocols()})
	})

	app.All("/v1/*", proxyHandler(deps, log))
	app.All("/v1beta/*", proxyHandler(deps, log))
	return app
}

func proxyHandler(deps Deps, log *slog.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		proto, ok := adapter.ByPath(c.Path())
		if !ok {
			return c.Status(http.StatusNotFound).JSON(errBody("unknown_endpoint", "no adapter for path "+c.Path()))
		}
		ingress, ok := deps.Registry.Get(proto)
		if !ok {
			return c.Status(http.StatusNotImplemented).JSON(errBody("protocol_disabled", "protocol "+string(proto)+" not enabled"))
		}

		// Authenticate (if auth is configured).
		var ident *auth.Identity
		if deps.Auth != nil {
			var authErr error
			ident, authErr = authenticate(c, deps.Auth, proto)
			if authErr != nil {
				return authErr // already a written response
			}
		}

		ireq, err := ingress.DecodeRequest(c.Body(), nil)
		if err != nil {
			log.Warn("ingress decode failed", "protocol", proto, "err", err, "request_id", c.Locals("request_id"))
			return writeProtoError(c, proto, "bad_request", err.Error(), http.StatusBadRequest)
		}
		ireq.ID, _ = c.Locals("request_id").(string)
		if ident != nil {
			ireq.Client = &ir.ClientContext{UserID: ident.UserID, APIKeyID: ident.APIKeyID}
			if err := authorize(c, deps.Auth, ident, ireq.Model, proto); err != nil {
				return err
			}
		}

		if ireq.Stream {
			return handleStream(c, deps.Pipeline, ingress, ireq, deps.Auth, deps.Logger, log)
		}
		return handleUnary(c, deps.Pipeline, ingress, ireq, deps.Auth, deps.Logger, log)
	}
}

// authenticate resolves the caller's API key and enforces the RPM limit.
func authenticate(c *fiber.Ctx, a *AuthDeps, proto adapter.Protocol) (*auth.Identity, error) {
	raw := extractKey(c)
	ident, err := a.Resolver.Resolve(c.UserContext(), raw)
	if err != nil {
		code := "invalid_api_key"
		if errors.Is(err, auth.ErrNoKey) {
			code = "missing_api_key"
		} else if errors.Is(err, auth.ErrUserDisabled) {
			code = "user_disabled"
		}
		return nil, writeProtoError(c, proto, code, err.Error(), http.StatusUnauthorized)
	}
	// RPM rate limit (counts the request).
	if ok, retry := a.Limiter.AllowRPM(c.UserContext(), ident.APIKeyID, ident.RPM); !ok {
		c.Set("Retry-After", itoa(retry))
		return nil, writeProtoError(c, proto, "rate_limit_exceeded",
			"RPM limit exceeded", http.StatusTooManyRequests)
	}
	return ident, nil
}

// authorize checks model whitelist and quota balance.
func authorize(c *fiber.Ctx, a *AuthDeps, ident *auth.Identity, model string, proto adapter.Protocol) error {
	if !ident.CanUseModel(model) {
		return writeProtoError(c, proto, "model_not_allowed",
			"model "+model+" not allowed for this key", http.StatusForbidden)
	}
	ok, err := a.Quota.HasCredit(c.UserContext(), ident.UserID)
	if err == nil && !ok {
		return writeProtoError(c, proto, "quota_exceeded",
			"insufficient quota", http.StatusPaymentRequired)
	}
	return nil
}

func extractKey(c *fiber.Ctx) string {
	if h := c.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return c.Get("x-api-key")
}

func itoa(n int) string {
	// small helper to avoid strconv import churn in this file
	if n <= 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func handleUnary(c *fiber.Ctx, p *pipeline.Pipeline, ingress adapter.Adapter, ireq *ir.UnifiedRequest, a *AuthDeps, logger *logging.Logger, log *slog.Logger) error {
	start := time.Now()
	ctx, cancel := context.WithCancel(c.UserContext())
	defer cancel()

	resp, err := p.Run(ctx, ireq)
	latency := int(time.Since(start).Milliseconds())

	// Log the request
	logEntry := logging.RequestLog{
		RequestID:  ireq.ID,
		Protocol:   ingress.Protocol(),
		Model:      ireq.Model,
		Stream:     false,
		LatencyMs:  latency,
		Timestamp:  start,
	}
	if ireq.Client != nil {
		logEntry.UserID = ireq.Client.UserID
		logEntry.APIKeyID = ireq.Client.APIKeyID
	}

	if err != nil {
		log.Warn("pipeline run failed", "request_id", ireq.ID, "model", ireq.Model, "err", err)
		status := http.StatusBadGateway
		code := "upstream_error"
		logEntry.Status = "error"
		if errors.Is(err, router.ErrNoChannel) {
			status = http.StatusNotFound
			code = "model_not_found"
			logEntry.Status = "no_channel"
		} else if egress.IsRetryable(err) {
			status = http.StatusServiceUnavailable
			code = "no_available_channel"
			logEntry.Status = "no_available_channel"
		}
		logEntry.HTTPStatus = status
		logEntry.ErrorCode = code
		logEntry.ErrorMsg = err.Error()
		if logger != nil {
			logger.Log(ctx, logEntry)
		}
		return writeProtoError(c, ingress.Protocol(), code, err.Error(), status)
	}

	// Success case
	logEntry.Status = "success"
	logEntry.HTTPStatus = http.StatusOK
	logEntry.ProviderID = resp.ProviderID
	logEntry.UpstreamModel = resp.UpstreamModel
	logEntry.StopReason = string(resp.StopReason)
	logEntry.Usage = &resp.Usage
	if logger != nil {
		logger.Log(ctx, logEntry)
	}

	deduct(c.UserContext(), a, ireq.Client, resp.Usage)
	body, err := ingress.EncodeResponse(resp)
	if err != nil {
		return writeProtoError(c, ingress.Protocol(), "encode_failed", err.Error(), http.StatusInternalServerError)
	}
	c.Set("X-Upstream-Model", resp.UpstreamModel)
	c.Set("X-Provider-Id", resp.ProviderID)
	return c.Send(body)
}

func handleStream(c *fiber.Ctx, p *pipeline.Pipeline, ingress adapter.Adapter, ireq *ir.UnifiedRequest, a *AuthDeps, logger *logging.Logger, log *slog.Logger) error {
	type evMsg struct {
		ev  ir.StreamEvent
		end bool
	}
	start := time.Now()
	ctx, cancel := context.WithCancel(c.UserContext())
	ch := make(chan evMsg, 32)

	go func() {
		defer func() { ch <- evMsg{end: true} }()
		_ = p.RunStream(ctx, ireq, func(e ir.StreamEvent) { ch <- evMsg{ev: e} })
	}()

	// Wait for the first event before committing any bytes — this is what makes
	// pre-first-byte failover able to return a clean HTTP error.
	first := <-ch
	if first.end {
		cancel()
		latency := int(time.Since(start).Milliseconds())
		// Log failure
		logEntry := logging.RequestLog{
			RequestID:  ireq.ID,
			Protocol:   ingress.Protocol(),
			Model:      ireq.Model,
			Stream:     true,
			Status:     "no_available_channel",
			HTTPStatus: http.StatusServiceUnavailable,
			LatencyMs:  latency,
			ErrorCode:  "no_available_channel",
			ErrorMsg:   "no upstream could start the stream",
			Timestamp:  start,
		}
		if ireq.Client != nil {
			logEntry.UserID = ireq.Client.UserID
			logEntry.APIKeyID = ireq.Client.APIKeyID
		}
		if logger != nil {
			logger.Log(ctx, logEntry)
		}
		// no event emitted: total routing/upstream failure before first byte
		return writeProtoError(c, ingress.Protocol(), "no_available_channel",
			"no upstream could start the stream", http.StatusServiceUnavailable)
	}

	c.Status(http.StatusOK)
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")

	enc := ingress.NewStreamEncoder()
	firstBytes, _ := enc.Encode(first.ev)
	client := ireq.Client

	// Track TTFT and metadata for logging
	var ttftMs int
	var stopReason ir.StopReason
	ttftRecorded := false

	// fasthttp's StreamWriter (func(w *bufio.Writer)) runs once in a goroutine
	// and must stream the whole body, flushing as it goes.
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer cancel()
		var usage *ir.Usage
		captureUsage := func(ev ir.StreamEvent) {
			if ev.Type == ir.EvMessageDelta && ev.Usage != nil {
				usage = ev.Usage
			}
		}
		captureMetadata := func(ev ir.StreamEvent) {
			if !ttftRecorded && ev.Type != ir.EvError {
				ttftMs = int(time.Since(start).Milliseconds())
				ttftRecorded = true
			}
			if ev.StopReason != "" {
				stopReason = ev.StopReason
			}
		}
		captureUsage(first.ev)
		captureMetadata(first.ev)
		if len(firstBytes) > 0 {
			if _, err := w.Write(firstBytes); err != nil {
				return
			}
			w.Flush()
			firstBytes = nil
		}
		for {
			msg, ok := <-ch
			if !ok || msg.end {
				// Log successful stream completion
				latency := int(time.Since(start).Milliseconds())
				logEntry := logging.RequestLog{
					RequestID:  ireq.ID,
					Protocol:   ingress.Protocol(),
					Model:      ireq.Model,
					Stream:     true,
					Status:     "success",
					HTTPStatus: http.StatusOK,
					StopReason: string(stopReason),
					TTFTMs:     ttftMs,
					LatencyMs:  latency,
					Usage:      usage,
					Timestamp:  start,
				}
				if ireq.Client != nil {
					logEntry.UserID = client.UserID
					logEntry.APIKeyID = client.APIKeyID
				}
				if logger != nil {
					logger.Log(ctx, logEntry)
				}
				if usage != nil {
					deduct(ctx, a, client, *usage)
				}
				return
			}
			captureUsage(msg.ev)
			captureMetadata(msg.ev)
			b, _ := enc.Encode(msg.ev)
			if _, err := w.Write(b); err != nil {
				// Client disconnected — still log what we got
				latency := int(time.Since(start).Milliseconds())
				logEntry := logging.RequestLog{
					RequestID:  ireq.ID,
					Protocol:   ingress.Protocol(),
					Model:      ireq.Model,
					Stream:     true,
					Status:     "client_disconnect",
					HTTPStatus: http.StatusOK,
					StopReason: string(stopReason),
					TTFTMs:     ttftMs,
					LatencyMs:  latency,
					Usage:      usage,
					Timestamp:  start,
				}
				if ireq.Client != nil {
					logEntry.UserID = client.UserID
					logEntry.APIKeyID = client.APIKeyID
				}
				if logger != nil {
					logger.Log(ctx, logEntry)
				}
				if usage != nil {
					deduct(ctx, a, client, *usage)
				}
				return // client disconnected
			}
			w.Flush()
		}
	})
	return nil
}

// deduct charges the caller's quota by token usage (1 credit/token), if auth is on.
func deduct(ctx context.Context, a *AuthDeps, client *ir.ClientContext, u ir.Usage) {
	if a == nil || a.Quota == nil || client == nil {
		return
	}
	cost := int64(u.InputTokens + u.OutputTokens)
	if cost <= 0 {
		return
	}
	_ = a.Quota.Deduct(ctx, client.UserID, cost)
}

// writeProtoError emits a protocol-shaped error body. The supported protocols
// each expect a different envelope (see docs/10-api.md §1.4).
func writeProtoError(c *fiber.Ctx, proto adapter.Protocol, code, message string, status int) error {
	c.Status(status)
	switch proto {
	case adapter.ProtocolMessages:
		return c.JSON(fiber.Map{
			"type":  "error",
			"error": fiber.Map{"type": code, "message": message},
		})
	default: // openai_chat, openai_responses
		return c.JSON(fiber.Map{
			"error": fiber.Map{"message": message, "type": code, "code": code},
		})
	}
}

func errBody(code, message string) fiber.Map {
	return fiber.Map{"error": fiber.Map{"code": code, "message": message}}
}

func requestIDMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		rid := c.Get("X-Request-Id")
		if rid == "" {
			rid = newRequestID()
		}
		c.Locals("request_id", rid)
		c.Set("X-Request-Id", rid)
		return c.Next()
	}
}

func accessLogMiddleware(log *slog.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		log.Info("request",
			"request_id", c.Locals("request_id"),
			"method", c.Method(),
			"path", c.Path(),
			"status", c.Response().StatusCode(),
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return err
	}
}

// newRequestID returns a compact unique id.
func newRequestID() string {
	return "rq_" + randHex(12)
}

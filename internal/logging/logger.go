// Package logging writes request logs to the database for observability.
// Each successful or failed request writes one row to request_logs, capturing
// protocol, model, timing (TTFT, latency), tokens, status, and errors.
package logging

import (
	"context"
	"time"

	"github.com/aigateway/ai-hub/internal/adapter"
	"github.com/aigateway/ai-hub/internal/ir"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Logger writes request logs to PostgreSQL.
type Logger struct {
	pool *pgxpool.Pool
}

// NewLogger creates a Logger.
func NewLogger(pool *pgxpool.Pool) *Logger {
	return &Logger{pool: pool}
}

// RequestLog represents a single request log entry.
type RequestLog struct {
	RequestID     string
	UserID        string
	APIKeyID      string
	Protocol      adapter.Protocol
	Model         string
	ProviderID    string
	UpstreamModel string
	Stream        bool
	Status        string // "success" | "error" | "no_channel" | "auth_failed" | "rate_limited"
	HTTPStatus    int
	StopReason    string
	TTFTMs        int // time to first token (streaming only)
	LatencyMs     int // total request duration
	Usage         *ir.Usage
	ErrorCode     string
	ErrorMsg      string
	Timestamp     time.Time
}

// Log writes a request log entry asynchronously (fire-and-forget).
func (l *Logger) Log(ctx context.Context, entry RequestLog) {
	if l.pool == nil {
		return
	}
	// Fire-and-forget: spawn a goroutine so we don't block the response.
	go l.write(context.Background(), entry)
}

// write performs the actual database insert.
func (l *Logger) write(ctx context.Context, e RequestLog) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var userID, apiKeyID, providerID, upstreamModel, stopReason, errorCode, errorMsg *string
	if e.UserID != "" {
		userID = &e.UserID
	}
	if e.APIKeyID != "" {
		apiKeyID = &e.APIKeyID
	}
	if e.ProviderID != "" {
		providerID = &e.ProviderID
	}
	if e.UpstreamModel != "" {
		upstreamModel = &e.UpstreamModel
	}
	if e.StopReason != "" {
		stopReason = &e.StopReason
	}
	if e.ErrorCode != "" {
		errorCode = &e.ErrorCode
	}
	if e.ErrorMsg != "" {
		errorMsg = &e.ErrorMsg
	}

	var ttft, latency, inputTok, outputTok, cacheRead, cacheCreate, reasoning *int
	if e.TTFTMs > 0 {
		ttft = &e.TTFTMs
	}
	if e.LatencyMs > 0 {
		latency = &e.LatencyMs
	}
	if e.Usage != nil {
		if e.Usage.InputTokens > 0 {
			inputTok = &e.Usage.InputTokens
		}
		if e.Usage.OutputTokens > 0 {
			outputTok = &e.Usage.OutputTokens
		}
		if e.Usage.CacheReadTokens > 0 {
			cacheRead = &e.Usage.CacheReadTokens
		}
		if e.Usage.CacheCreationTokens > 0 {
			cacheCreate = &e.Usage.CacheCreationTokens
		}
		if e.Usage.ReasoningTokens > 0 {
			reasoning = &e.Usage.ReasoningTokens
		}
	}

	ts := e.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}

	_, _ = l.pool.Exec(ctx, `
		INSERT INTO request_logs (
			ts, user_id, api_key_id, client_protocol, model, provider_id, upstream_model,
			stream, status, http_status, stop_reason, ttft_ms, latency_ms,
			input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, reasoning_tokens,
			error_code, error_msg, request_id
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`,
		ts, userID, apiKeyID, e.Protocol, e.Model, providerID, upstreamModel,
		e.Stream, e.Status, e.HTTPStatus, stopReason, ttft, latency,
		inputTok, outputTok, cacheRead, cacheCreate, reasoning,
		errorCode, errorMsg, e.RequestID,
	)
	// Errors are silently dropped (fire-and-forget). In production you'd want
	// to emit metrics on write failures, but don't block the request path.
}

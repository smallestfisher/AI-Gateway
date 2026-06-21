package admin

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// RequestLog is the admin view of a request_logs row. Nullable columns are
// surfaced as empty strings / zero ints (omitted from JSON); the logger writes
// NULL for unset optionals.
type RequestLog struct {
	ID                  string `json:"id"`
	Timestamp           string `json:"timestamp"` // RFC3339
	UserID              string `json:"user_id,omitempty"`
	APIKeyID            string `json:"api_key_id,omitempty"`
	Protocol            string `json:"protocol"`
	Model               string `json:"model"`
	ProviderID          string `json:"provider_id,omitempty"`
	UpstreamModel       string `json:"upstream_model,omitempty"`
	Stream              bool   `json:"stream"`
	Status              string `json:"status"`
	HTTPStatus          int    `json:"http_status,omitempty"`
	StopReason          string `json:"stop_reason,omitempty"`
	TTFTMs              int    `json:"ttft_ms,omitempty"`
	LatencyMs           int    `json:"latency_ms,omitempty"`
	InputTokens         int    `json:"input_tokens,omitempty"`
	OutputTokens        int    `json:"output_tokens,omitempty"`
	CacheReadTokens     int    `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int    `json:"cache_creation_tokens,omitempty"`
	ReasoningTokens     int    `json:"reasoning_tokens,omitempty"`
	ErrorCode           string `json:"error_code,omitempty"`
	ErrorMsg            string `json:"error_msg,omitempty"`
	RequestID           string `json:"request_id"`
}

// LogList is the paginated response for the log query endpoint.
type LogList struct {
	Data  []RequestLog `json:"data"`
	Total int          `json:"total"`
}

// LogFilter holds the optional filters for ListLogs. Zero values mean "unset".
type LogFilter struct {
	UserID        string
	Model         string
	ProviderID    string
	Protocol      string
	Status        string
	StopReason    string
	Q             string // case-insensitive search on request_id/model/error fields
	Stream        *bool
	From, To      time.Time // zero = unset
	Limit, Offset int
}

// ListLogs returns a page of request_logs rows matching the filter, plus the
// total matching count (ignoring limit/offset) so the caller can render page
// counts.
func (s *Store) ListLogs(ctx context.Context, f LogFilter) (LogList, error) {
	where, args := logWhereClause(f)

	var total int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM request_logs`+where, args...,
	).Scan(&total); err != nil {
		return LogList{}, err
	}

	// Append LIMIT/OFFSET after the filter args. Their 1-based placeholder
	// indices are len(args)+1 and len(args)+2 (before appending each).
	limitIdx := len(args) + 1
	args = append(args, clamp(f.Limit, 1, 200, 50))
	offsetIdx := len(args) + 1
	args = append(args, max(f.Offset, 0))
	rows, err := s.pool.Query(ctx, `
		SELECT id, ts, COALESCE(user_id::text,''), COALESCE(api_key_id::text,''),
		       client_protocol, model, COALESCE(provider_id::text,''),
		       COALESCE(upstream_model,''), stream, status, COALESCE(http_status,0),
		       COALESCE(stop_reason,''), COALESCE(ttft_ms,0), COALESCE(latency_ms,0),
		       COALESCE(input_tokens,0), COALESCE(output_tokens,0),
		       COALESCE(cache_read_tokens,0), COALESCE(cache_creation_tokens,0),
		       COALESCE(reasoning_tokens,0), COALESCE(error_code,''), COALESCE(error_msg,''),
		       request_id
		FROM request_logs`+where+`
		ORDER BY ts DESC
		LIMIT $`+strconv.Itoa(limitIdx)+` OFFSET $`+strconv.Itoa(offsetIdx), args...)
	if err != nil {
		return LogList{}, err
	}
	defer rows.Close()

	out := LogList{Data: []RequestLog{}, Total: total}
	for rows.Next() {
		var (
			l  RequestLog
			ts time.Time
		)
		if err := rows.Scan(&l.ID, &ts, &l.UserID, &l.APIKeyID, &l.Protocol, &l.Model,
			&l.ProviderID, &l.UpstreamModel, &l.Stream, &l.Status, &l.HTTPStatus,
			&l.StopReason, &l.TTFTMs, &l.LatencyMs, &l.InputTokens, &l.OutputTokens,
			&l.CacheReadTokens, &l.CacheCreationTokens, &l.ReasoningTokens,
			&l.ErrorCode, &l.ErrorMsg, &l.RequestID); err != nil {
			return LogList{}, err
		}
		l.Timestamp = ts.Format(time.RFC3339)
		out.Data = append(out.Data, l)
	}
	return out, rows.Err()
}

// logWhereClause builds " WHERE ... " (or "" when no filters) and its args. The
// placeholder index starts at 1 and is appended to by the caller for LIMIT/OFFSET.
func logWhereClause(f LogFilter) (clause string, args []any) {
	var conds []string
	n := 1
	add := func(expr string, val any) {
		conds = append(conds, expr)
		args = append(args, val)
		n++
	}
	if f.UserID != "" {
		add("user_id::text = $"+strconv.Itoa(n), f.UserID)
	}
	if f.Model != "" {
		add("model = $"+strconv.Itoa(n), f.Model)
	}
	if f.ProviderID != "" {
		add("provider_id::text = $"+strconv.Itoa(n), f.ProviderID)
	}
	if f.Protocol != "" {
		add("client_protocol = $"+strconv.Itoa(n), f.Protocol)
	}
	if f.Status != "" {
		add("status = $"+strconv.Itoa(n), f.Status)
	}
	if f.StopReason != "" {
		add("stop_reason = $"+strconv.Itoa(n), f.StopReason)
	}
	if f.Stream != nil {
		add("stream = $"+strconv.Itoa(n), *f.Stream)
	}
	if !f.From.IsZero() {
		add("ts >= $"+strconv.Itoa(n), f.From)
	}
	if !f.To.IsZero() {
		add("ts <= $"+strconv.Itoa(n), f.To)
	}
	if f.Q != "" {
		p := "$" + strconv.Itoa(n)
		add("(request_id ILIKE "+p+
			" OR model ILIKE "+p+
			" OR COALESCE(error_code,'') ILIKE "+p+
			" OR COALESCE(error_msg,'') ILIKE "+p+")", "%"+f.Q+"%")
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// parseLogFilter reads the optional log query params from the request and
// applies sane defaults/clamps.
func parseLogFilter(c *fiber.Ctx) LogFilter {
	f := LogFilter{
		UserID:     c.Query("user_id"),
		Model:      c.Query("model"),
		ProviderID: c.Query("provider_id"),
		Protocol:   c.Query("protocol"),
		Status:     c.Query("status"),
		StopReason: c.Query("stop_reason"),
		Q:          c.Query("q"),
		Limit:      clamp(c.QueryInt("limit", 50), 1, 200, 50),
		Offset:     max(c.QueryInt("offset", 0), 0),
	}
	if s := c.Query("stream"); s == "true" {
		t := true
		f.Stream = &t
	} else if s == "false" {
		fl := false
		f.Stream = &fl
	}
	f.From = parseRFC3339(c.Query("from"))
	f.To = parseRFC3339(c.Query("to"))
	return f
}

func parseRFC3339(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// clamp returns v bounded to [lo,hi]; if v is zero (the default) it returns def.
func clamp(v, lo, hi, def int) int {
	if v == 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

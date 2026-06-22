package admin

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
)

const auditRedacted = "[REDACTED]"

// AuditLog is the admin view of one audit_logs row.
type AuditLog struct {
	ID         string         `json:"id"`
	Timestamp  string         `json:"timestamp"`
	ActorID    string         `json:"actor_id,omitempty"`
	Action     string         `json:"action"`
	TargetType string         `json:"target_type"`
	TargetID   string         `json:"target_id,omitempty"`
	Diff       map[string]any `json:"diff,omitempty"`
	RequestID  string         `json:"request_id,omitempty"`
}

// AuditLogList is the paginated response for the audit log endpoint.
type AuditLogList struct {
	Data  []AuditLog `json:"data"`
	Total int        `json:"total"`
}

// AuditFilter holds optional filters for audit log listing.
type AuditFilter struct {
	Action     string
	TargetType string
	TargetID   string
	Q          string
	From       time.Time
	To         time.Time
	Limit      int
	Offset     int
}

func (s *Store) ListAuditLogs(ctx context.Context, f AuditFilter) (AuditLogList, error) {
	where, args := auditWhereClause(f)

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_logs`+where, args...).Scan(&total); err != nil {
		return AuditLogList{}, err
	}

	limitIdx := len(args) + 1
	args = append(args, clamp(f.Limit, 1, 200, 50))
	offsetIdx := len(args) + 1
	args = append(args, max(f.Offset, 0))

	rows, err := s.pool.Query(ctx, `
		SELECT id, ts, COALESCE(actor_id::text,''), action, target_type,
		       COALESCE(target_id::text,''), COALESCE(diff, '{}'::jsonb), COALESCE(request_id,'')
		FROM audit_logs`+where+`
		ORDER BY ts DESC
		LIMIT $`+strconv.Itoa(limitIdx)+` OFFSET $`+strconv.Itoa(offsetIdx), args...)
	if err != nil {
		return AuditLogList{}, err
	}
	defer rows.Close()

	out := AuditLogList{Data: []AuditLog{}, Total: total}
	for rows.Next() {
		var (
			l    AuditLog
			ts   time.Time
			diff []byte
		)
		if err := rows.Scan(&l.ID, &ts, &l.ActorID, &l.Action, &l.TargetType, &l.TargetID, &diff, &l.RequestID); err != nil {
			return AuditLogList{}, err
		}
		l.Timestamp = ts.Format(time.RFC3339)
		_ = json.Unmarshal(diff, &l.Diff)
		if l.Diff == nil {
			l.Diff = map[string]any{}
		}
		out.Data = append(out.Data, l)
	}
	return out, rows.Err()
}

func auditWhereClause(f AuditFilter) (clause string, args []any) {
	var conds []string
	n := 1
	add := func(expr string, val any) {
		conds = append(conds, expr)
		args = append(args, val)
		n++
	}
	if f.Action != "" {
		add("action = $"+strconv.Itoa(n), f.Action)
	}
	if f.TargetType != "" {
		add("target_type = $"+strconv.Itoa(n), f.TargetType)
	}
	if f.TargetID != "" {
		add("target_id::text = $"+strconv.Itoa(n), f.TargetID)
	}
	if !f.From.IsZero() {
		add("ts >= $"+strconv.Itoa(n), f.From)
	}
	if !f.To.IsZero() {
		add("ts <= $"+strconv.Itoa(n), f.To)
	}
	if f.Q != "" {
		p := "$" + strconv.Itoa(n)
		add("(action ILIKE "+p+
			" OR target_type ILIKE "+p+
			" OR COALESCE(target_id::text,'') ILIKE "+p+
			" OR COALESCE(request_id,'') ILIKE "+p+
			" OR COALESCE(diff::text,'') ILIKE "+p+")", "%"+f.Q+"%")
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func parseAuditFilter(c *fiber.Ctx) AuditFilter {
	return AuditFilter{
		Action:     c.Query("action"),
		TargetType: c.Query("target_type"),
		TargetID:   c.Query("target_id"),
		Q:          c.Query("q"),
		From:       parseRFC3339(c.Query("from")),
		To:         parseRFC3339(c.Query("to")),
		Limit:      clamp(c.QueryInt("limit", 50), 1, 200, 50),
		Offset:     max(c.QueryInt("offset", 0), 0),
	}
}

func insertAudit(ctx context.Context, tx pgx.Tx, action, targetType, targetID string, diff map[string]any) error {
	return insertAuditWithRequest(ctx, tx, action, targetType, targetID, diff, "")
}

func insertAuditWithRequest(ctx context.Context, tx pgx.Tx, action, targetType, targetID string, diff map[string]any, requestID string) error {
	if action == "" || targetType == "" {
		return ErrValidation
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (action, target_type, target_id, diff, request_id)
		VALUES ($1, $2, $3, $4, $5)`,
		action, targetType, auditTargetID(targetID), marshalJSON(redactAuditDiff(diff)), strPtr(requestID))
	return err
}

func (s *Store) RecordAudit(ctx context.Context, action, targetType, targetID string, diff map[string]any) error {
	if action == "" || targetType == "" {
		return ErrValidation
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_logs (action, target_type, target_id, diff)
		VALUES ($1, $2, $3, $4)`,
		action, targetType, auditTargetID(targetID), marshalJSON(redactAuditDiff(diff)))
	return err
}

func auditTargetID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func redactAuditDiff(diff map[string]any) map[string]any {
	if diff == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(diff))
	for k, v := range diff {
		if isSecretAuditKey(k) {
			out[k] = auditRedacted
			continue
		}
		out[k] = redactAuditValue(v)
	}
	return out
}

func redactAuditValue(v any) any {
	switch vv := v.(type) {
	case map[string]any:
		return redactAuditDiff(vv)
	case map[string]string:
		out := make(map[string]string, len(vv))
		for k, item := range vv {
			if isSecretAuditKey(k) {
				out[k] = auditRedacted
			} else {
				out[k] = item
			}
		}
		return out
	case []any:
		out := make([]any, 0, len(vv))
		for _, item := range vv {
			out = append(out, redactAuditValue(item))
		}
		return out
	default:
		return v
	}
}

func isSecretAuditKey(key string) bool {
	k := strings.ToLower(key)
	for _, needle := range []string{"api_key", "key", "secret", "token", "authorization"} {
		if strings.Contains(k, needle) {
			return true
		}
	}
	return false
}

func auditBulkModelChannelItems(items []BulkModelChannelRowResult) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"alias":          item.Alias,
			"upstream_model": item.UpstreamModel,
			"status":         item.Status,
			"model_id":       item.ModelID,
			"channel_id":     item.ChannelID,
		})
	}
	return out
}

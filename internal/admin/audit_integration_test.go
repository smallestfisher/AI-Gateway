package admin_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aigateway/ai-hub/internal/admin"
)

func TestListAuditLogsFiltersByActionAndTarget(t *testing.T) {
	pool, rdb, _, _ := setup(t)
	ctx := context.Background()
	st := admin.NewStore(pool, rdb)

	var targetID string
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&targetID); err != nil {
		t.Fatalf("uuid: %v", err)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO audit_logs (action, target_type, target_id, diff, request_id)
		VALUES
		  ('provider.create', 'provider', $1, '{"name":"p"}', 'req-audit-1'),
		  ('model.create', 'model', NULL, '{"alias":"m"}', 'req-audit-2')`,
		targetID,
	)
	if err != nil {
		t.Fatalf("seed audit logs: %v", err)
	}

	res, err := st.ListAuditLogs(ctx, admin.AuditFilter{
		Action:     "provider.create",
		TargetType: "provider",
		TargetID:   targetID,
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListAuditLogs: %v", err)
	}
	if res.Total != 1 || len(res.Data) != 1 {
		t.Fatalf("want one audit row, got total=%d data=%+v", res.Total, res.Data)
	}
	row := res.Data[0]
	if row.Action != "provider.create" || row.TargetType != "provider" || row.TargetID != targetID || row.RequestID != "req-audit-1" {
		t.Fatalf("audit row mismatch: %+v", row)
	}
	if row.Diff["name"] != "p" {
		t.Fatalf("diff mismatch: %+v", row.Diff)
	}
}

func TestProviderCreateRecordsAuditLog(t *testing.T) {
	pool, rdb, _, _ := setup(t)
	ctx := context.Background()
	st := admin.NewStore(pool, rdb)

	providerID, err := st.CreateProvider(ctx, admin.Provider{
		Name:       "audited-provider",
		Protocol:   "openai_chat",
		BaseURL:    "https://audit.example.com",
		APIKey:     "sk-audit-secret",
		Enabled:    true,
		TimeoutMs:  60000,
		MaxRetries: 2,
	})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}

	res, err := st.ListAuditLogs(ctx, admin.AuditFilter{
		Action:     "provider.create",
		TargetType: "provider",
		TargetID:   providerID,
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListAuditLogs: %v", err)
	}
	if res.Total != 1 || len(res.Data) != 1 {
		t.Fatalf("want one audit row, got total=%d data=%+v", res.Total, res.Data)
	}
	body, _ := json.Marshal(res.Data[0].Diff)
	if string(body) == "" || json.Valid(body) == false {
		t.Fatalf("invalid diff: %s", body)
	}
	if res.Data[0].Diff["api_key"] != "[REDACTED]" {
		t.Fatalf("api key was not redacted: %+v", res.Data[0].Diff)
	}
}

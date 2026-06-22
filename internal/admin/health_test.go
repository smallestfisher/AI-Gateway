package admin_test

import (
	"context"
	"testing"

	"github.com/aigateway/ai-hub/internal/admin"
	"github.com/aigateway/ai-hub/internal/health"
)

type fakeHealthStats struct {
	stats health.Stats
	err   error
}

func (f fakeHealthStats) Stats(context.Context, string, string) (health.Stats, error) {
	return f.stats, f.err
}

func TestAdminHealthListsChannelStats(t *testing.T) {
	pool, rdb, app, _ := setup(t)
	ctx := context.Background()
	st := admin.NewStore(pool, rdb)

	_, b := do(t, app, "POST", "/api/admin/models",
		`{"alias":"health-alias","display_name":"Health Alias","enabled":true}`, true)
	modelID := idOf(t, b)
	_, b = do(t, app, "POST", "/api/admin/providers",
		`{"name":"health-provider","protocol":"openai_chat","base_url":"https://health.example.com","api_key":"sk-health","enabled":true,"hc_error_rate":0.25,"hc_p95_ttft_ms":5000,"hc_window_sec":90,"hc_cooldown_sec":45}`, true)
	providerID := idOf(t, b)
	_, b = do(t, app, "POST", "/api/admin/model-channels",
		`{"model_id":"`+modelID+`","provider_id":"`+providerID+`","upstream_model":"gpt-health","enabled":true}`, true)

	res, err := st.ListHealth(ctx, fakeHealthStats{stats: health.Stats{
		Total:      10,
		Failures:   2,
		Slow:       1,
		ErrorRate:  0.2,
		SlowRate:   0.1,
		Open:       true,
		OpenedAgoS: 12,
	}})
	if err != nil {
		t.Fatalf("ListHealth: %v", err)
	}
	if res.Warning != "" {
		t.Fatalf("unexpected warning: %s", res.Warning)
	}
	if len(res.Data) != 1 {
		t.Fatalf("rows = %d, want 1 (%+v)", len(res.Data), res.Data)
	}
	row := res.Data[0]
	if row.ProviderID != providerID || row.ProviderName != "health-provider" || row.UpstreamModel != "gpt-health" {
		t.Fatalf("identity mismatch: %+v", row)
	}
	if row.Total != 10 || row.Failures != 2 || row.Slow != 1 || !row.Open || row.OpenedAgoS != 12 {
		t.Fatalf("stats mismatch: %+v", row)
	}
	if row.Thresholds.ErrorRate != 0.25 || row.Thresholds.P95TTFTMs != 5000 || row.Thresholds.WindowSec != 90 || row.Thresholds.CooldownSec != 45 {
		t.Fatalf("thresholds mismatch: %+v", row.Thresholds)
	}
}

func TestAdminHealthWithoutStatsProviderReturnsWarning(t *testing.T) {
	pool, rdb, app, _ := setup(t)
	ctx := context.Background()
	st := admin.NewStore(pool, rdb)

	_, b := do(t, app, "POST", "/api/admin/models",
		`{"alias":"health-no-redis","display_name":"Health No Redis","enabled":true}`, true)
	modelID := idOf(t, b)
	_, b = do(t, app, "POST", "/api/admin/providers",
		`{"name":"health-provider-no-redis","protocol":"openai_chat","base_url":"https://health.example.com","api_key":"sk-health","enabled":true}`, true)
	providerID := idOf(t, b)
	_, b = do(t, app, "POST", "/api/admin/model-channels",
		`{"model_id":"`+modelID+`","provider_id":"`+providerID+`","upstream_model":"gpt-health-no-redis","enabled":true}`, true)

	res, err := st.ListHealth(ctx, nil)
	if err != nil {
		t.Fatalf("ListHealth: %v", err)
	}
	if res.Warning == "" {
		t.Fatalf("expected degraded warning, got empty")
	}
	if len(res.Data) != 1 || res.Data[0].ProviderID != providerID {
		t.Fatalf("degraded rows mismatch: %+v", res.Data)
	}
}

package admin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aigateway/ai-hub/internal/admin"
	"github.com/gofiber/fiber/v2"
)

func TestBulkCreateProviderModelChannelsCreatesModelsAndChannels(t *testing.T) {
	pool, rdb, app, _ := setup(t)
	ctx := context.Background()
	providerID := createProviderForBulkSync(t, app)
	st := admin.NewStore(pool, rdb)

	res, err := st.BulkCreateProviderModelChannels(ctx, providerID, admin.BulkModelChannelInput{
		Items: []admin.BulkModelChannelItem{
			{
				UpstreamModel: "gpt-4o-mini",
				Alias:         "gpt-4o-mini",
				DisplayName:   "GPT-4o mini",
			},
		},
		Weight:   2,
		Priority: 5,
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("bulk create: %v", err)
	}
	if res.CreatedModels != 1 || res.CreatedChannels != 1 || res.SkippedChannels != 0 {
		t.Fatalf("unexpected summary: %+v", res)
	}
	if len(res.Items) != 1 || res.Items[0].Status != "created" || res.Items[0].ModelID == "" || res.Items[0].ChannelID == "" {
		t.Fatalf("unexpected row result: %+v", res.Items)
	}

	var got struct {
		Alias         string
		DisplayName   string
		UpstreamModel string
		Weight        int
		Priority      int
		Enabled       bool
	}
	if err := pool.QueryRow(ctx, `
		SELECT m.alias, m.display_name, mc.upstream_model, mc.weight, mc.priority, mc.enabled
		FROM model_channels mc
		JOIN models m ON m.id = mc.model_id
		WHERE mc.provider_id = $1`, providerID,
	).Scan(&got.Alias, &got.DisplayName, &got.UpstreamModel, &got.Weight, &got.Priority, &got.Enabled); err != nil {
		t.Fatalf("query created channel: %v", err)
	}
	if got.Alias != "gpt-4o-mini" || got.DisplayName != "GPT-4o mini" || got.UpstreamModel != "gpt-4o-mini" {
		t.Fatalf("created model/channel mismatch: %+v", got)
	}
	if got.Weight != 2 || got.Priority != 5 || !got.Enabled {
		t.Fatalf("channel settings mismatch: %+v", got)
	}
}

func TestBulkCreateProviderModelChannelsIsIdempotent(t *testing.T) {
	pool, rdb, app, _ := setup(t)
	ctx := context.Background()
	providerID := createProviderForBulkSync(t, app)
	st := admin.NewStore(pool, rdb)
	input := admin.BulkModelChannelInput{
		Items: []admin.BulkModelChannelItem{
			{UpstreamModel: "claude-3-5-sonnet", Alias: "claude-3.5-sonnet", DisplayName: "Claude 3.5 Sonnet"},
		},
		Weight:  1,
		Enabled: true,
	}

	first, err := st.BulkCreateProviderModelChannels(ctx, providerID, input)
	if err != nil {
		t.Fatalf("first bulk create: %v", err)
	}
	second, err := st.BulkCreateProviderModelChannels(ctx, providerID, input)
	if err != nil {
		t.Fatalf("second bulk create: %v", err)
	}
	if first.CreatedModels != 1 || first.CreatedChannels != 1 {
		t.Fatalf("first summary mismatch: %+v", first)
	}
	if second.CreatedModels != 0 || second.CreatedChannels != 0 || second.SkippedChannels != 1 {
		t.Fatalf("second summary mismatch: %+v", second)
	}
	if len(second.Items) != 1 || second.Items[0].Status != "skipped" {
		t.Fatalf("second row mismatch: %+v", second.Items)
	}

	var modelCount, channelCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM models WHERE alias=$1`, "claude-3.5-sonnet").Scan(&modelCount); err != nil {
		t.Fatalf("count models: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM model_channels WHERE provider_id=$1 AND upstream_model=$2`, providerID, "claude-3-5-sonnet").Scan(&channelCount); err != nil {
		t.Fatalf("count channels: %v", err)
	}
	if modelCount != 1 || channelCount != 1 {
		t.Fatalf("expected one model and channel, got models=%d channels=%d", modelCount, channelCount)
	}
}

func TestBulkCreateProviderModelChannelsRejectsBadAlias(t *testing.T) {
	pool, rdb, app, _ := setup(t)
	providerID := createProviderForBulkSync(t, app)
	st := admin.NewStore(pool, rdb)

	_, err := st.BulkCreateProviderModelChannels(context.Background(), providerID, admin.BulkModelChannelInput{
		Items: []admin.BulkModelChannelItem{
			{UpstreamModel: "bad", Alias: "bad alias"},
		},
		Enabled: true,
	})
	if !errors.Is(err, admin.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

func createProviderForBulkSync(t *testing.T, app *fiber.App) string {
	t.Helper()
	resp, b := do(t, app, "POST", "/api/admin/providers",
		`{"name":"bulk-sync-provider","protocol":"openai_chat","base_url":"https://upstream.example.com","api_key":"sk-test","enabled":true}`, true)
	if resp.StatusCode != 201 {
		t.Fatalf("create provider: %d %s", resp.StatusCode, b)
	}
	return idOf(t, b)
}

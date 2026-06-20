package registrydb

import (
	"context"
	"os"
	"testing"

	"github.com/aigateway/ai-hub/internal/adapter"

	"github.com/jackc/pgx/v5/pgxpool"
)

// This integration test requires a migrated Postgres. It is skipped unless
// GATEWAY_TEST_POSTGRES_DSN is set. Run against docker:
//
//	docker run -d --rm --name aihub-pg -e POSTGRES_PASSWORD=test -e POSTGRES_DB=aihub -p 55432:5432 postgres:16-alpine
//	# apply migration, then:
//	GATEWAY_TEST_POSTGRES_DSN='postgres://postgres:test@localhost:55432/aihub?sslmode=disable' go test ./internal/registrydb/
func dsn() string { return os.Getenv("GATEWAY_TEST_POSTGRES_DSN") }

func skipNoDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	d := dsn()
	if d == "" {
		t.Skip("GATEWAY_TEST_POSTGRES_DSN not set; skipping DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), d)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

const (
	pID    = "11111111-1111-1111-1111-111111111111"
	mID    = "22222222-2222-2222-2222-222222222222"
	pfID   = "33333333-3333-3333-3333-333333333333"
	pfID2  = "44444444-4444-4444-4444-444444444444"
	polID  = "55555555-5555-5555-5555-555555555555"
)

func seed(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	// clean
	for _, tbl := range []string{"model_channels", "client_profiles", "router_policies", "models", "providers"} {
		if _, err := pool.Exec(ctx, "DELETE FROM "+tbl); err != nil {
			t.Fatalf("clean %s: %v", tbl, err)
		}
	}
	exec := func(sql string, args ...any) {
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	exec(`INSERT INTO providers (id,name,protocol,base_url,api_key_enc,enabled,timeout_ms,
		hc_error_rate,hc_p95_ttft_ms,hc_window_sec,hc_cooldown_sec,metadata)
		VALUES ($1,$2,$3,$4,$5,true,60000, 0.2,5000,60,20,$6)`,
		pID, "testprov", "openai_chat", "https://up.example.com", []byte("sk-test"),
		`{"headers":{"x-app":"aihub"}}`)
	exec(`INSERT INTO models (id,alias,display_name,enabled) VALUES ($1,$2,$3,true)`,
		mID, "test-alias", "Test Alias")
	exec(`INSERT INTO model_channels (model_id,provider_id,upstream_model,weight,priority,enabled)
		VALUES ($1,$2,$3,7,0,true)`, mID, pID, "gpt-4o")
	// default profile + model-scope profile (should merge)
	exec(`INSERT INTO client_profiles (id,name,scope,headers,user_agent,enabled)
		VALUES ($1,'default','default','{}','default-ua',true)`, pfID)
	exec(`INSERT INTO client_profiles (id,name,scope,target_id,headers,origin,enabled)
		VALUES ($1,'mprof','model',$2,'{"x-model":"1"}','https://m.example.com',true)`, pfID2, mID)
	exec(`INSERT INTO router_policies (id,scope,model_id,mode,params,enabled)
		VALUES ($1,'model',$2,'weighted','{"max_attempts":3}',true)`, polID, mID)
}

func TestLoad_BuildsSnapshot(t *testing.T) {
	pool := skipNoDB(t)
	seed(t, pool)

	snap, err := Load(context.Background(), pool, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	chs := snap.ChannelsFor("test-alias")
	if len(chs) != 1 {
		t.Fatalf("channels = %+v", snap.Channels)
	}
	ch := chs[0]
	if ch.UpstreamModel != "gpt-4o" || ch.Weight != 7 || ch.Priority != 0 {
		t.Errorf("channel = %+v", ch)
	}
	if ch.Provider == nil || ch.Provider.ID != pID {
		t.Fatalf("provider not attached: %+v", ch.Provider)
	}
	if ch.Provider.Protocol != adapter.ProtocolChat {
		t.Errorf("protocol = %s", ch.Provider.Protocol)
	}
	if ch.Provider.BaseURL != "https://up.example.com" {
		t.Errorf("base_url = %s", ch.Provider.BaseURL)
	}
	if ch.Provider.APIKey != "sk-test" {
		t.Errorf("api key not decrypted/loaded: %q", ch.Provider.APIKey)
	}
	// provider extra headers from metadata
	if ch.Provider.Headers["x-app"] != "aihub" {
		t.Errorf("provider headers = %+v", ch.Provider.Headers)
	}
	// health thresholds
	if ch.Provider.HealthErrorRate != 0.2 || ch.Provider.HealthP95TTFTMs != 5000 || ch.Provider.HealthCooldown != 20 {
		t.Errorf("health cfg = %+v", ch.Provider)
	}
	// merged profile: default UA + model origin + model header
	if ch.Profile == nil {
		t.Fatal("profile not resolved")
	}
	if ch.Profile.UserAgent != "default-ua" {
		t.Errorf("ua = %q", ch.Profile.UserAgent)
	}
	if ch.Profile.Origin != "https://m.example.com" {
		t.Errorf("origin = %q", ch.Profile.Origin)
	}
	if ch.Profile.Headers["x-model"] != "1" {
		t.Errorf("headers = %+v", ch.Profile.Headers)
	}
	// policy
	pol := snap.PolicyFor("test-alias")
	if pol == nil || pol.Mode != "weighted" || pol.MaxAttempts != 3 {
		t.Errorf("policy = %+v", pol)
	}
	// providers index
	if snap.ProviderByID(pID) == nil {
		t.Error("provider not in Providers index")
	}
}

func TestLoad_DisabledRowsExcluded(t *testing.T) {
	pool := skipNoDB(t)
	seed(t, pool)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE providers SET enabled=false WHERE id=$1`, pID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `UPDATE providers SET enabled=true WHERE id=$1`, pID) })

	snap, err := Load(ctx, pool, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.ChannelsFor("test-alias")) != 0 {
		t.Errorf("disabled provider should yield no channels: %+v", snap.Channels)
	}
}

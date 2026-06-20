package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aigateway/ai-hub/internal/adapter"
	"github.com/aigateway/ai-hub/internal/adapter/anthropicmessages"
	"github.com/aigateway/ai-hub/internal/adapter/openaichat"
	"github.com/aigateway/ai-hub/internal/admin"
	"github.com/aigateway/ai-hub/internal/auth"
	"github.com/aigateway/ai-hub/internal/config"
	"github.com/aigateway/ai-hub/internal/egress"
	"github.com/aigateway/ai-hub/internal/pipeline"
	"github.com/aigateway/ai-hub/internal/registry"
	"github.com/aigateway/ai-hub/internal/registrydb"
	"github.com/aigateway/ai-hub/internal/router"
	"github.com/aigateway/ai-hub/internal/server"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const admToken = "adm-token-auth"

func setupAuthedApp(t *testing.T, upstreamURL string) (*fiber.App, *registrydb.Reloader, *pgxpool.Pool, *redis.Client) {
	t.Helper()
	dsn := os.Getenv("GATEWAY_TEST_POSTGRES_DSN")
	raddr := os.Getenv("GATEWAY_TEST_REDIS_ADDR")
	if dsn == "" || raddr == "" {
		t.Skip("GATEWAY_TEST_POSTGRES_DSN / GATEWAY_TEST_REDIS_ADDR not set; skipping auth integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, tbl := range []string{"api_keys", "user_quotas", "users", "model_channels", "client_profiles", "router_policies", "models", "providers"} {
		pool.Exec(context.Background(), "DELETE FROM "+tbl)
	}

	rdb := redis.NewClient(&redis.Options{Addr: raddr})
	t.Cleanup(func() { rdb.FlushAll(context.Background()); _ = rdb.Close() })

	reloader := registrydb.NewReloader(pool, rdb, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := reloader.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reloader.Stop)

	reg := adapter.NewRegistry(openaichat.New(), anthropicmessages.New())
	b := registry.NewBuilder().AddChannel(&registry.Channel{
		Alias: "authed", UpstreamModel: "gpt-4o",
		Provider: &registry.Provider{ID: "p1", Name: "p1", Protocol: adapter.ProtocolChat, BaseURL: upstreamURL, APIKey: "sk"},
	})
	rt := router.New(registry.NewStatic(b.Build()))
	pipe := pipeline.New(rt, egress.New(reg))

	app := server.New(&config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), server.Deps{
		Registry: reg,
		Pipeline: pipe,
		Auth: &server.AuthDeps{
			Resolver: auth.NewResolver(pool, rdb),
			Limiter:  auth.NewLimiter(rdb),
			Quota:    auth.NewQuota(rdb, pool),
		},
	})
	admin.Mount(app, admin.NewStore(pool, rdb), admToken)
	t.Cleanup(func() { _ = app.Shutdown() })
	return app, reloader, pool, rdb
}

// adminJSON runs an admin request and returns the id (if any).
func adminReq(t *testing.T, app *fiber.App, method, path, body string) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+admToken)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func proxyReq(t *testing.T, app *fiber.App, key string) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/messages",
		strings.NewReader(`{"model":"authed","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestAuth_EndToEnd(t *testing.T) {
	// fake chat upstream returning token usage (prompt 10, completion 5 -> cost 15)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"cc","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}))
	defer upstream.Close()

	app, reloader, pool, rdb := setupAuthedApp(t, upstream.URL)

	// create a user + key with balance 1000
	st, b := adminReq(t, app, "POST", "/api/admin/users", `{"name":"alice","balance":1000}`)
	if st != 201 {
		t.Fatal(st, string(b))
	}
	var u struct{ ID string `json:"id"` }
	json.Unmarshal(b, &u)
	st, b = adminReq(t, app, "POST", "/api/admin/users/"+u.ID+"/api-keys", `{"name":"k1"}`)
	if st != 201 {
		t.Fatal(st, string(b))
	}
	var kr struct{ Key string `json:"key"` }
	json.Unmarshal(b, &kr)
	key := kr.Key

	// wait for the route to be live (the static config already has "authed",
	// but the reloader overlays from DB which is empty -> "authed" may vanish).
	// The static source above is NOT the reloader; the app uses the reloader as
	// source. So seed "authed" into the DB via admin too.
	mst, mb := adminReq(t, app, "POST", "/api/admin/models", `{"alias":"authed","enabled":true}`)
	if mst != 201 {
		t.Fatal(mst, string(mb))
	}
	var m struct{ ID string `json:"id"` }
	json.Unmarshal(mb, &m)
	pst, pb := adminReq(t, app, "POST", "/api/admin/providers",
		`{"name":"p1","protocol":"openai_chat","base_url":"`+upstream.URL+`","api_key":"sk","enabled":true}`)
	var pv struct{ ID string `json:"id"` }
	json.Unmarshal(pb, &pv)
	if pst != 201 {
		t.Fatal(pst, string(pb))
	}
	adminReq(t, app, "POST", "/api/admin/model-channels",
		`{"model_id":"`+m.ID+`","provider_id":"`+pv.ID+`","upstream_model":"gpt-4o","enabled":true}`)

	waitRoute(t, reloader, "authed")

	// 1) no key -> 401
	if resp, _ := proxyReq(t, app, ""); resp.StatusCode != 401 {
		t.Errorf("no key: got %d want 401", resp.StatusCode)
	}
	// 2) valid key -> 200
	if resp, _ := proxyReq(t, app, key); resp.StatusCode != 200 {
		t.Errorf("valid key: got %d want 200", resp.StatusCode)
	}
	// balance should have dropped ~15
	bal, _ := auth.NewQuota(rdb, pool).Balance(context.Background(), u.ID)
	if bal >= 1000 {
		t.Errorf("balance not deducted: %d", bal)
	}

	// 3) quota exhausted -> 402 (fresh user with balance 0)
	st, b = adminReq(t, app, "POST", "/api/admin/users", `{"name":"broke","balance":0}`)
	var broke struct{ ID string `json:"id"` }
	json.Unmarshal(b, &broke)
	st, b = adminReq(t, app, "POST", "/api/admin/users/"+broke.ID+"/api-keys", `{"name":"k"}`)
	var brokeKey struct{ Key string `json:"key"` }
	json.Unmarshal(b, &brokeKey)
	if resp, _ := proxyReq(t, app, brokeKey.Key); resp.StatusCode != 402 {
		t.Errorf("broke user: got %d want 402", resp.StatusCode)
	}

	// 4) RPM exceeded -> 429 (fresh user, rpm=1)
	st, b = adminReq(t, app, "POST", "/api/admin/users", `{"name":"fast","balance":1000}`)
	var fast struct{ ID string `json:"id"` }
	json.Unmarshal(b, &fast)
	adminReq(t, app, "PUT", "/api/admin/users/"+fast.ID+"/quota", `{"balance":1000,"rpm":1}`)
	st, b = adminReq(t, app, "POST", "/api/admin/users/"+fast.ID+"/api-keys", `{"name":"k"}`)
	var fastKey struct{ Key string `json:"key"` }
	json.Unmarshal(b, &fastKey)
	if resp, _ := proxyReq(t, app, fastKey.Key); resp.StatusCode != 200 {
		t.Errorf("fast req1: got %d want 200", resp.StatusCode)
	}
	if resp, _ := proxyReq(t, app, fastKey.Key); resp.StatusCode != 429 {
		t.Errorf("fast req2 (rpm=1): got %d want 429", resp.StatusCode)
	}

	// 5) model not whitelisted -> 403
	st, b = adminReq(t, app, "POST", "/api/admin/users", `{"name":"wl","balance":1000}`)
	var wlu struct{ ID string `json:"id"` }
	json.Unmarshal(b, &wlu)
	adminReq(t, app, "PUT", "/api/admin/users/"+wlu.ID+"/quota", `{"balance":1000,"whitelist":["other-model"]}`)
	st, b = adminReq(t, app, "POST", "/api/admin/users/"+wlu.ID+"/api-keys", `{"name":"k"}`)
	var wlKey struct{ Key string `json:"key"` }
	json.Unmarshal(b, &wlKey)
	if resp, _ := proxyReq(t, app, wlKey.Key); resp.StatusCode != 403 {
		t.Errorf("whitelisted: got %d want 403", resp.StatusCode)
	}
}

func waitRoute(t *testing.T, r *registrydb.Reloader, alias string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s, err := r.Snapshot()
		if err == nil && len(s.ChannelsFor(alias)) > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("route %s did not become available", alias)
}

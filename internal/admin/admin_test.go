package admin_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aigateway/ai-hub/internal/admin"
	"github.com/aigateway/ai-hub/internal/registrydb"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const token = "test-admin-token"

func setup(t *testing.T) (*pgxpool.Pool, *redis.Client, *fiber.App, *registrydb.Reloader) {
	t.Helper()
	dsn := os.Getenv("GATEWAY_TEST_POSTGRES_DSN")
	raddr := os.Getenv("GATEWAY_TEST_REDIS_ADDR")
	if dsn == "" || raddr == "" {
		t.Skip("GATEWAY_TEST_POSTGRES_DSN / GATEWAY_TEST_REDIS_ADDR not set; skipping admin integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pg: %v", err)
	}
	t.Cleanup(pool.Close)
	// clean config tables
	for _, tbl := range []string{"model_channels", "client_profiles", "router_policies", "models", "providers"} {
		if _, err := pool.Exec(context.Background(), "DELETE FROM "+tbl); err != nil {
			t.Fatal(err)
		}
	}
	rdb := redis.NewClient(&redis.Options{Addr: raddr})
	t.Cleanup(func() { _ = rdb.Close() })

	reloader := registrydb.NewReloader(pool, rdb, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := reloader.Start(context.Background()); err != nil {
		t.Fatalf("reloader start: %v", err)
	}
	t.Cleanup(reloader.Stop)

	app := fiber.New()
	admin.Mount(app, admin.NewStore(pool, rdb), token)
	t.Cleanup(func() { _ = app.Shutdown() })
	return pool, rdb, app, reloader
}

func do(t *testing.T, app *fiber.App, method, path, body string, auth bool) (*http.Response, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Content-Type", "application/json")
	if auth {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test %s %s: %v", method, path, err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// TestAdminCRUDAndHotReload creates config via the API and verifies the
// Reloader picks it up via the published config:invalidate — the hot-reload
// closed loop.
func TestAdminCRUDAndHotReload(t *testing.T) {
	_, _, app, reloader := setup(t)

	// create model
	resp, b := do(t, app, "POST", "/api/admin/models",
		`{"alias":"hot-alias","display_name":"Hot","enabled":true}`, true)
	if resp.StatusCode != 201 {
		t.Fatalf("create model: %d %s", resp.StatusCode, b)
	}
	modelID := idOf(t, b)

	// create provider
	resp, b = do(t, app, "POST", "/api/admin/providers",
		`{"name":"hotprov","protocol":"openai_chat","base_url":"https://up.example.com","api_key":"sk-hot","enabled":true}`, true)
	if resp.StatusCode != 201 {
		t.Fatalf("create provider: %d %s", resp.StatusCode, b)
	}
	provID := idOf(t, b)

	// create channel binding alias -> provider
	resp, b = do(t, app, "POST", "/api/admin/model-channels",
		`{"model_id":"`+modelID+`","provider_id":"`+provID+`","upstream_model":"gpt-4o","weight":3,"enabled":true}`, true)
	if resp.StatusCode != 201 {
		t.Fatalf("create channel: %d %s", resp.StatusCode, b)
	}

	// list reflects it
	resp, b = do(t, app, "GET", "/api/admin/providers", "", true)
	if resp.StatusCode != 200 || !strings.Contains(string(b), "hotprov") {
		t.Errorf("list providers: %d %s", resp.StatusCode, b)
	}

	// HOT RELOAD: the writes published config:invalidate; the Reloader should
	// have reloaded so the new alias is resolvable.
	deadline := time.Now().Add(3 * time.Second)
	var resolved bool
	for time.Now().Before(deadline) {
		snap, err := reloader.Snapshot()
		if err == nil && len(snap.ChannelsFor("hot-alias")) == 1 {
			ch := snap.ChannelsFor("hot-alias")[0]
			if ch.Provider.Name == "hotprov" && ch.UpstreamModel == "gpt-4o" && ch.Weight == 3 {
				resolved = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !resolved {
		t.Fatal("hot-reload did not surface the new alias within timeout")
	}
}

func TestAdminAuth(t *testing.T) {
	_, _, app, _ := setup(t)
	// no token -> 401
	resp, _ := do(t, app, "GET", "/api/admin/providers", "", false)
	if resp.StatusCode != 401 {
		t.Errorf("no token: got %d want 401", resp.StatusCode)
	}
	// wrong token -> 401
	req := httptest.NewRequest("GET", "/api/admin/providers", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	resp, _ = app.Test(req, -1)
	if resp.StatusCode != 401 {
		t.Errorf("wrong token: got %d want 401", resp.StatusCode)
	}
}

func TestAdminValidation(t *testing.T) {
	_, _, app, _ := setup(t)
	// missing required fields -> 400
	resp, b := do(t, app, "POST", "/api/admin/providers", `{"name":""}`, true)
	if resp.StatusCode != 400 {
		t.Errorf("validation: got %d want 400 (%s)", resp.StatusCode, b)
	}
}

func TestAdminDeleteInvalidates(t *testing.T) {
	_, _, app, reloader := setup(t)
	// build a full alias binding so ChannelsFor has something to drop
	_, b := do(t, app, "POST", "/api/admin/models", `{"alias":"del-alias","enabled":true}`, true)
	modelID := idOf(t, b)
	_, b = do(t, app, "POST", "/api/admin/providers",
		`{"name":"delprov","protocol":"openai_chat","base_url":"https://x","api_key":"sk","enabled":true}`, true)
	provID := idOf(t, b)
	_, b = do(t, app, "POST", "/api/admin/model-channels",
		`{"model_id":"`+modelID+`","provider_id":"`+provID+`","upstream_model":"gpt-4o","enabled":true}`, true)
	chID := idOf(t, b)

	waitFor(t, reloader, "del-alias", true) // binding visible after hot reload

	// delete the channel -> alias should drop
	resp, b := do(t, app, "DELETE", "/api/admin/model-channels/"+chID, "", true)
	if resp.StatusCode != 204 {
		t.Fatalf("delete channel: %d %s", resp.StatusCode, b)
	}
	waitFor(t, reloader, "del-alias", false)
}

func waitFor(t *testing.T, r *registrydb.Reloader, alias string, wantPresent bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snap, err := r.Snapshot()
		present := err == nil && len(snap.ChannelsFor(alias)) > 0
		if present == wantPresent {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("alias %s presence=%v did not converge", alias, wantPresent)
}

func idOf(t *testing.T, body []byte) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", body, err)
	}
	id, _ := m["id"].(string)
	if id == "" {
		t.Fatalf("no id in %s", body)
	}
	return id
}

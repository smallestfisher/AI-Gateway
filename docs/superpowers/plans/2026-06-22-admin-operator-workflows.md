# Admin Operator Workflows Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add desktop admin workflows for bulk provider model setup, richer diagnostics, log details, health visibility, operation feedback, and audit logs.

**Architecture:** Keep the Admin API as the only backend boundary. Add focused Store methods and DTO files beside existing admin modules, then wire compact Next.js pages/sheets through the existing BFF and TanStack Query patterns. Commit after each independently testable batch.

**Tech Stack:** Go/Fiber/pgx/Redis, Next.js 16 App Router, TypeScript, TanStack Query, Tailwind/Radix UI, pnpm.

---

## File Map

Backend:

- Modify: `internal/admin/store.go` for bulk model/channel write and audit helper hooks where existing write methods live.
- Create: `internal/admin/model_sync.go` for bulk sync DTOs and row-result shaping.
- Create: `internal/admin/model_sync_test.go` for idempotency and validation tests.
- Modify: `internal/admin/diagnostics.go` for `request_id` and optional `stream` fields.
- Modify: `internal/admin/diagnostics_test.go` for request ID and non-recording behavior tests.
- Create: `internal/admin/health.go` for Admin health DTOs and PostgreSQL/Redis aggregation.
- Create: `internal/admin/health_test.go` for health response shaping with fake stats provider.
- Create: `internal/admin/audit.go` for audit DTOs, filtering, redaction, insert/list helpers.
- Create: `internal/admin/audit_test.go` for redaction/list filters and write recording.
- Modify: `internal/admin/admin.go` to register new Admin routes.
- Modify: `cmd/gateway/main.go` to pass the health store into Admin mounting.

Frontend:

- Modify: `apps/web/lib/types.ts` for bulk sync, diagnostic request ID, health, and audit types.
- Modify: `apps/web/lib/query-keys.ts` for health/audit/model-sync keys.
- Create: `apps/web/app/(admin)/providers/model-sync-sheet.tsx`.
- Modify: `apps/web/app/(admin)/providers/columns.tsx`.
- Modify: `apps/web/app/(admin)/providers/page.tsx`.
- Modify: `apps/web/components/diagnostics/diagnostic-result.tsx`.
- Modify: `apps/web/components/diagnostics/gateway-test-sheet.tsx`.
- Modify: `apps/web/app/(admin)/providers/provider-diagnostics-sheet.tsx`.
- Modify: `apps/web/app/(admin)/logs/page.tsx`.
- Create: `apps/web/app/(admin)/health/page.tsx`.
- Create: `apps/web/app/(admin)/audit/page.tsx`.
- Modify: `apps/web/components/app-sidebar.tsx`.
- Modify: form pages/components touched by operation feedback cleanup:
  `apps/web/app/(admin)/providers/page.tsx`,
  `apps/web/app/(admin)/models/page.tsx`,
  `apps/web/app/(admin)/profiles/page.tsx`,
  `apps/web/app/(admin)/router/page.tsx`,
  `apps/web/app/(admin)/users/page.tsx`.

Docs:

- Existing spec: `docs/superpowers/specs/2026-06-22-admin-operator-workflows-design.md`.
- This plan: `docs/superpowers/plans/2026-06-22-admin-operator-workflows.md`.

---

## Task 1: Bulk Model/Channel Backend

**Files:**
- Create: `internal/admin/model_sync.go`
- Create: `internal/admin/model_sync_test.go`
- Modify: `internal/admin/store.go`
- Modify: `internal/admin/admin.go`

- [x] **Step 1: Write failing bulk sync tests**

Create `internal/admin/model_sync_test.go` with tests named:

```go
func TestBulkCreateProviderModelChannelsCreatesModelsAndChannels(t *testing.T)
func TestBulkCreateProviderModelChannelsIsIdempotent(t *testing.T)
func TestBulkCreateProviderModelChannelsRejectsBadAlias(t *testing.T)
```

Use the existing `admin_test.go` integration harness pattern:

```go
pool, rdb := testDB(t)
st := NewStore(pool, rdb)
providerID := seedProvider(t, pool)
res, err := st.BulkCreateProviderModelChannels(ctx, providerID, BulkModelChannelInput{
    Items: []BulkModelChannelItem{{UpstreamModel: "gpt-4o-mini", Alias: "gpt-4o-mini", DisplayName: "GPT-4o mini"}},
    Weight: 1,
    Priority: 0,
    Enabled: true,
})
```

Expected behavior:

- First test creates one `models` row and one `model_channels` row.
- Second test returns `SkippedChannels == 1` on the second call.
- Third test returns `ErrValidation` for aliases not matching `^[a-zA-Z0-9_.\\-/]+$`.

- [x] **Step 2: Run tests to verify failure**

Run:

```bash
GOCACHE=/tmp/go-build go test ./internal/admin -run 'TestBulkCreateProviderModelChannels' -v
```

Expected: FAIL because `BulkModelChannelInput` and `BulkCreateProviderModelChannels` do not exist.

- [x] **Step 3: Add DTOs and validation**

Create `internal/admin/model_sync.go` with:

```go
package admin

import "regexp"

var modelAliasPattern = regexp.MustCompile(`^[a-zA-Z0-9_.\-/]+$`)

type BulkModelChannelInput struct {
    Items    []BulkModelChannelItem `json:"items"`
    Weight   int                    `json:"weight"`
    Priority int                    `json:"priority"`
    Enabled  bool                   `json:"enabled"`
}

type BulkModelChannelItem struct {
    UpstreamModel string `json:"upstream_model"`
    Alias         string `json:"alias"`
    DisplayName   string `json:"display_name,omitempty"`
}

type BulkModelChannelResult struct {
    CreatedModels   int                         `json:"created_models"`
    CreatedChannels int                         `json:"created_channels"`
    SkippedChannels int                         `json:"skipped_channels"`
    Items           []BulkModelChannelRowResult `json:"items"`
}

type BulkModelChannelRowResult struct {
    Alias         string `json:"alias"`
    UpstreamModel string `json:"upstream_model"`
    Status        string `json:"status"`
    ModelID       string `json:"model_id,omitempty"`
    ChannelID     string `json:"channel_id,omitempty"`
    Error         string `json:"error,omitempty"`
}
```

- [x] **Step 4: Implement Store bulk method**

Add to `internal/admin/store.go`:

```go
func (s *Store) BulkCreateProviderModelChannels(ctx context.Context, providerID string, in BulkModelChannelInput) (BulkModelChannelResult, error)
```

Implementation requirements:

- Validate provider ID and at least one item.
- Validate each alias with `modelAliasPattern`.
- Use one transaction.
- Ensure provider exists: `SELECT id FROM providers WHERE id=$1`.
- For each item:
  - Insert model with `ON CONFLICT (alias) DO UPDATE SET display_name=models.display_name RETURNING id`.
  - Insert channel with `ON CONFLICT (model_id, provider_id, upstream_model) DO NOTHING RETURNING id`.
  - Count created/skipped rows.
- Call `s.invalidate(ctx)` after commit through existing `s.inTx`.

- [x] **Step 5: Add Admin route**

In `internal/admin/admin.go`, add after upstream model listing:

```go
g.Post("/providers/:id/bulk-model-channels", func(c *fiber.Ctx) error {
    var in BulkModelChannelInput
    if err := c.BodyParser(&in); err != nil {
        return c.Status(400).JSON(errMap("bad_request", err.Error()))
    }
    res, err := st.BulkCreateProviderModelChannels(c.UserContext(), c.Params("id"), in)
    if err != nil {
        return writeErr(c, err)
    }
    return c.JSON(res)
})
```

- [x] **Step 6: Run backend tests**

Run:

```bash
gofmt -w internal/admin/model_sync.go internal/admin/model_sync_test.go internal/admin/store.go internal/admin/admin.go
GOCACHE=/tmp/go-build go test ./internal/admin -run 'TestBulkCreateProviderModelChannels' -v
```

Expected: PASS, with DB/Redis integration tests skipped only when test env vars are missing.

- [x] **Step 7: Commit**

```bash
git add internal/admin/model_sync.go internal/admin/model_sync_test.go internal/admin/store.go internal/admin/admin.go
git commit -m "feat: bulk bind provider models"
```

---

## Task 2: Bulk Model/Channel Frontend

**Files:**
- Modify: `apps/web/lib/types.ts`
- Modify: `apps/web/lib/query-keys.ts`
- Create: `apps/web/app/(admin)/providers/model-sync-sheet.tsx`
- Modify: `apps/web/app/(admin)/providers/columns.tsx`
- Modify: `apps/web/app/(admin)/providers/page.tsx`

- [x] **Step 1: Add frontend types**

Add TypeScript interfaces mirroring `BulkModelChannelInput`, `BulkModelChannelItem`, `BulkModelChannelResult`, and `BulkModelChannelRowResult`.

- [x] **Step 2: Add Provider table action**

In provider columns, add a sync action using a suitable lucide icon (`ListPlus` or `DownloadCloud`) with `aria-label="同步模型"`.

- [x] **Step 3: Create `ModelSyncSheet`**

The sheet must:

- Fetch upstream models for the selected provider.
- Receive existing `models` and `channels` from the page.
- Render a searchable selectable list.
- Allow per-row alias edits.
- Submit selected rows to `/providers/:id/bulk-model-channels`.
- Show created/skipped summary.
- Invalidate `qk.models` and `qk.channels`.

- [x] **Step 4: Wire sheet into Provider page**

Add state:

```ts
const [syncing, setSyncing] = useState<Provider | null>(null);
```

Pass `onSyncModels: setSyncing` into columns and render `ModelSyncSheet` with `key={syncing?.id ?? "closed"}`.

- [x] **Step 5: Verify frontend**

Run:

```bash
pnpm lint
pnpm build
```

Expected: lint exits 0 with only the existing TanStack Table warning; build exits 0.

- [x] **Step 6: Commit**

```bash
git add apps/web/lib/types.ts apps/web/lib/query-keys.ts 'apps/web/app/(admin)/providers/model-sync-sheet.tsx' 'apps/web/app/(admin)/providers/columns.tsx' 'apps/web/app/(admin)/providers/page.tsx'
git commit -m "feat(web): add provider model sync"
```

---

## Task 3: Diagnostic History and Request IDs

**Files:**
- Modify: `internal/admin/diagnostics.go`
- Modify: `internal/admin/diagnostics_test.go`
- Modify: `apps/web/lib/types.ts`
- Modify: `apps/web/components/diagnostics/diagnostic-result.tsx`
- Modify: `apps/web/components/diagnostics/gateway-test-sheet.tsx`
- Modify: `apps/web/app/(admin)/providers/provider-diagnostics-sheet.tsx`

- [x] **Step 1: Add backend tests**

Add tests proving:

- `DiagnosticResult.RequestID` is set for direct upstream and gateway tests.
- Request IDs are stable non-empty strings and use the `admin-diagnostic-` prefix.

- [x] **Step 2: Implement request IDs**

Add `RequestID string `json:"request_id"` to `DiagnosticResult`.

Generate IDs using existing request ID conventions if available; otherwise use:

```go
func newDiagnosticRequestID() string {
    return "admin-diagnostic-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}
```

Set `req.ID` and `DiagnosticResult.RequestID` from the same value.

- [x] **Step 3: Update frontend result view**

Show request ID in `DiagnosticResultView` with a copyable monospace value.

- [x] **Step 4: Add local history helper**

In each diagnostic sheet, store successful and failed results in `localStorage` under keys:

```ts
diagnostic-history:provider:${provider.id}
diagnostic-history:channel:${alias}:${provider_id}:${upstream_model}
```

Keep only the newest 20 results. Selecting a history row sets the current result.

- [x] **Step 5: Verify**

Run:

```bash
gofmt -w internal/admin/diagnostics.go internal/admin/diagnostics_test.go
GOCACHE=/tmp/go-build go test ./internal/admin -run 'TestDiagnostics' -v
pnpm lint
pnpm build
```

Expected: PASS/exit 0, aside from existing frontend lint warning.

- [x] **Step 6: Commit**

```bash
git add internal/admin/diagnostics.go internal/admin/diagnostics_test.go apps/web/lib/types.ts apps/web/components/diagnostics/diagnostic-result.tsx apps/web/components/diagnostics/gateway-test-sheet.tsx 'apps/web/app/(admin)/providers/provider-diagnostics-sheet.tsx'
git commit -m "feat: retain diagnostic run history"
```

---

## Task 4: Logs Detail Drawer and Health Page

**Files:**
- Create: `internal/admin/health.go`
- Create: `internal/admin/health_test.go`
- Modify: `internal/admin/admin.go`
- Modify: `cmd/gateway/main.go`
- Modify: `apps/web/lib/types.ts`
- Modify: `apps/web/lib/query-keys.ts`
- Modify: `apps/web/app/(admin)/logs/page.tsx`
- Create: `apps/web/app/(admin)/health/page.tsx`
- Modify: `apps/web/components/app-sidebar.tsx`

- [x] **Step 1: Add health backend DTOs**

Create `HealthStatus`, `HealthRow`, `HealthThresholds`, and a small interface:

```go
type HealthStatsProvider interface {
    Stats(ctx context.Context, providerID, model string) (health.Stats, error)
}
```

Add `Store.ListHealth(ctx, hp HealthStatsProvider) (HealthStatus, error)`.

- [x] **Step 2: Query channel/provider rows**

Use PostgreSQL query:

```sql
SELECT DISTINCT p.id, p.name, mc.upstream_model,
       p.hc_error_rate, p.hc_p95_ttft_ms, p.hc_window_sec, p.hc_cooldown_sec
FROM model_channels mc
JOIN providers p ON p.id = mc.provider_id
JOIN models m ON m.id = mc.model_id
WHERE mc.enabled = true AND p.enabled = true AND m.enabled = true
ORDER BY p.name, mc.upstream_model
```

- [x] **Step 3: Register health route**

Extend `MountOption` with a `health HealthStatsProvider` field and add:

```go
g.Get("/health", func(c *fiber.Ctx) error {
    res, err := st.ListHealth(c.UserContext(), mo.health)
    if err != nil {
        return writeErr(c, err)
    }
    return c.JSON(res)
})
```

Pass the Redis health store from `cmd/gateway/main.go`.

- [x] **Step 4: Add Logs drawer**

In `apps/web/app/(admin)/logs/page.tsx`:

- Add `selectedLog` state.
- Make rows clickable.
- Render a `Sheet` with Request, Routing, Timings, Usage, and Error sections.

- [x] **Step 5: Add Health page**

Create `apps/web/app/(admin)/health/page.tsx`:

- Query `/health`.
- Render summary cards and grouped table.
- Auto-refresh every 20 seconds.
- Show a degraded Redis warning if backend returns it.

- [x] **Step 6: Enable sidebar item**

In `apps/web/components/app-sidebar.tsx`, remove `disabled: true` from `/health`.

- [x] **Step 7: Verify**

Run:

```bash
gofmt -w internal/admin/health.go internal/admin/health_test.go internal/admin/admin.go cmd/gateway/main.go
GOCACHE=/tmp/go-build go test ./internal/admin ./internal/health -run 'TestHealth|TestAdmin' -v
pnpm lint
pnpm build
```

Expected: Go tests pass or skip DB/Redis integration by env; frontend lint/build exit 0.

- [x] **Step 8: Commit**

```bash
git add internal/admin/health.go internal/admin/health_test.go internal/admin/admin.go cmd/gateway/main.go apps/web/lib/types.ts apps/web/lib/query-keys.ts 'apps/web/app/(admin)/logs/page.tsx' 'apps/web/app/(admin)/health/page.tsx' apps/web/components/app-sidebar.tsx
git commit -m "feat: add log details and health status"
```

---

## Task 5: Operation Feedback Cleanup

**Files:**
- Modify: `apps/web/app/(admin)/providers/page.tsx`
- Modify: `apps/web/app/(admin)/models/page.tsx`
- Modify: `apps/web/app/(admin)/profiles/page.tsx`
- Modify: `apps/web/app/(admin)/router/page.tsx`
- Modify: `apps/web/app/(admin)/users/page.tsx`
- Modify if needed: `apps/web/components/form-sheet.tsx`

- [x] **Step 1: Close create/update sheets after success**

For each page submit handler, after successful mutation:

```ts
setOpen(false);
setEditing(null);
```

Use the page's existing state names.

- [x] **Step 2: Refresh dependent queries**

Ensure these are invalidated/refetched after writes:

- Providers write: providers, channels, profiles, policies as affected by cascade.
- Models write: models, channels.
- Channels write: channels.
- Profiles write: profiles.
- Router write: policies.
- Users/key/quota write: users and current apiKeys query.

- [x] **Step 3: Defer "save and continue" to a focused FormSheet enhancement**

The current `FormSheet` supports one submit path. This pass keeps default
submit as save-and-close and avoids adding a second submit path to the shared
form component while larger audit/health work is in flight.

- [x] **Step 4: Verify**

Run:

```bash
pnpm lint
pnpm build
```

Expected: exit 0, only existing TanStack Table warning.

- [x] **Step 5: Commit**

```bash
git add 'apps/web/app/(admin)/providers/page.tsx' 'apps/web/app/(admin)/models/page.tsx' 'apps/web/app/(admin)/profiles/page.tsx' 'apps/web/app/(admin)/router/page.tsx' 'apps/web/app/(admin)/users/page.tsx' apps/web/components/form-sheet.tsx
git commit -m "fix(web): normalize admin form feedback"
```

---

## Task 6: Audit Backend

**Files:**
- Create: `internal/admin/audit.go`
- Create: `internal/admin/audit_test.go`
- Modify: `internal/admin/store.go`
- Modify: `internal/admin/admin.go`

- [ ] **Step 1: Add audit DTOs and filter tests**

Create tests for:

```go
func TestAuditRedactsSecrets(t *testing.T)
func TestListAuditLogsFiltersByActionAndTarget(t *testing.T)
func TestProviderCreateRecordsAuditLog(t *testing.T)
```

- [ ] **Step 2: Implement audit helpers**

Create:

```go
type AuditLog struct {
    ID string `json:"id"`
    Timestamp string `json:"timestamp"`
    ActorID string `json:"actor_id,omitempty"`
    Action string `json:"action"`
    TargetType string `json:"target_type"`
    TargetID string `json:"target_id,omitempty"`
    Diff map[string]any `json:"diff,omitempty"`
    RequestID string `json:"request_id,omitempty"`
}

type AuditFilter struct {
    Action string
    TargetType string
    TargetID string
    Q string
    From time.Time
    To time.Time
    Limit int
    Offset int
}
```

Add redaction for keys containing `api_key`, `key`, `secret`, `token`, `authorization`.

- [ ] **Step 3: Record audits in write transactions**

Add a transaction helper:

```go
func insertAudit(ctx context.Context, tx pgx.Tx, action, targetType, targetID string, diff map[string]any) error
```

Call it in existing Store write methods after the write succeeds and before transaction commit.

- [ ] **Step 4: Add audit list route**

Register:

```text
GET /api/admin/audit-logs
```

Parse filters like `logs.go` does.

- [ ] **Step 5: Verify**

Run:

```bash
gofmt -w internal/admin/audit.go internal/admin/audit_test.go internal/admin/store.go internal/admin/admin.go
GOCACHE=/tmp/go-build go test ./internal/admin -run 'TestAudit|TestProviderCreateRecordsAuditLog' -v
```

Expected: PASS or DB/Redis integration skips when env vars are missing.

- [ ] **Step 6: Commit**

```bash
git add internal/admin/audit.go internal/admin/audit_test.go internal/admin/store.go internal/admin/admin.go
git commit -m "feat: record admin audit logs"
```

---

## Task 7: Audit Frontend

**Files:**
- Modify: `apps/web/lib/types.ts`
- Modify: `apps/web/lib/query-keys.ts`
- Create: `apps/web/app/(admin)/audit/page.tsx`
- Modify: `apps/web/components/app-sidebar.tsx`

- [ ] **Step 1: Add types and query key**

Add `AuditLog`, `AuditLogList`, and `AuditFilter` interfaces.

Add:

```ts
auditLogs: (filter: unknown) => ["audit-logs", filter] as const
```

- [ ] **Step 2: Create Audit page**

The page should mirror Logs page density:

- Filters for action, target type, search, from/to.
- Table columns: time, action, target, request ID.
- Detail drawer with formatted redacted JSON diff.

- [ ] **Step 3: Add sidebar item**

Add `/audit` under "可观测" with `ClipboardList` from `lucide-react`.

- [ ] **Step 4: Verify**

Run:

```bash
pnpm lint
pnpm build
```

Expected: exit 0, only existing TanStack Table warning.

- [ ] **Step 5: Commit**

```bash
git add apps/web/lib/types.ts apps/web/lib/query-keys.ts 'apps/web/app/(admin)/audit/page.tsx' apps/web/components/app-sidebar.tsx
git commit -m "feat(web): add audit log page"
```

---

## Task 8: Full Verification and Push

**Files:** no source edits unless verification reveals a bug.

- [ ] **Step 1: Run full selected backend tests**

```bash
GOCACHE=/tmp/go-build go test ./internal/admin ./internal/pipeline ./internal/egress ./internal/server ./internal/health -v
```

Expected: PASS, with Postgres/Redis integration tests skipped only when env vars are not set.

- [ ] **Step 2: Run frontend verification**

```bash
cd apps/web
pnpm lint
pnpm build
```

Expected: lint exit 0 with the existing TanStack Table warning; build exit 0.

- [ ] **Step 3: Run route smoke**

Start production web server and curl:

```bash
pnpm start
curl -sS -o /tmp/ai-gateway-login.html -w '%{http_code}\n' http://localhost:3000/login
curl -sS -o /tmp/ai-gateway-health.html -w '%{http_code}\n' -H 'Cookie: admin_token=smoke' http://localhost:3000/health
curl -sS -o /tmp/ai-gateway-audit.html -w '%{http_code}\n' -H 'Cookie: admin_token=smoke' http://localhost:3000/audit
```

Expected: login 200, health 200, audit 200.

- [ ] **Step 4: Confirm clean status**

```bash
git status -sb
```

Expected: branch has no uncommitted changes.

- [ ] **Step 5: Push**

```bash
git push
```

Expected: push succeeds to `origin/main`.

---

## Self-Review

Spec coverage:

- Bulk model setup: Task 1 and Task 2.
- Diagnostic request IDs/history: Task 3.
- Operation feedback: Task 5.
- Log detail drawer: Task 4.
- Health status page: Task 4.
- Audit records/query page: Task 6 and Task 7.
- Full verification/push: Task 8.

Known deliberate deferrals:

- Mobile navigation remains out of scope.
- Multi-role RBAC remains out of scope.
- Diagnostic history is local browser history, not a server table.
- Streaming diagnostics are optional within Task 3 and may defer if they require a larger stream execution path than the current helpers expose.

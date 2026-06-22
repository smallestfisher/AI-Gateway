# Admin Operator Workflows — Design Spec

**Date:** 2026-06-22
**Status:** Approved scope -> implementation plan next.

> Improve the desktop admin console for day-to-day operation: bulk model setup,
> richer diagnostics, clearer operation feedback, request log details, provider
> health visibility, and admin audit records. Mobile navigation and multi-role
> RBAC are explicitly out of scope.

---

## 1. Context

- The admin console already has Providers, Models, Client Profiles, Router,
  Users, Dashboard, Logs, and provider/channel diagnostics.
- Provider model discovery exists through
  `GET /api/admin/providers/:id/upstream-models`.
- Request logs are written to `request_logs` and listed through
  `GET /api/admin/logs`.
- Redis-backed health state already exists in `internal/health.Store`.
- The initial database schema already includes `audit_logs`, but Admin API
  writes do not record audit rows yet.
- The sidebar contains a disabled Health item.

## 2. Goals

- Reduce model configuration work by turning upstream discovery into bulk alias
  and channel setup.
- Make provider/channel tests more useful during incident response.
- Make admin mutations feel predictable: save closes the sheet by default,
  lists refresh, and repeated creation remains available where useful.
- Let operators inspect a request log row without leaving the Logs page.
- Expose provider/model health and circuit-breaker state in the admin console.
- Record admin write operations in `audit_logs` and expose a query page.

## 3. Non-Goals

- No mobile navigation work.
- No multi-admin accounts, role hierarchy, or permission editor. The gateway
  still uses the single `GATEWAY_ADMIN_TOKEN` admin model.
- No schema changes unless implementation uncovers a hard need. `audit_logs`
  already exists, and diagnostic history can live in browser-local state.
- No ClickHouse/log warehouse work.
- No full request-body capture in logs. Existing `request_logs` fields remain
  the detail source.

## 4. Approach Options Considered

### Recommended: Incremental Admin Workflow Pass

Implement the work as five focused batches:

1. Model sync + bulk bind.
2. Diagnostic UI/history enhancements.
3. Log detail drawer and health page.
4. Operation feedback cleanup.
5. Audit logging and audit page.

This keeps each change testable and avoids coupling UI workflow changes to
database/audit internals.

### Alternative: One Large Operator Console Rewrite

This would redesign Providers, Models, Logs, and Health together. It may produce
a more uniform UI, but it has a large review surface and higher regression risk.

### Alternative: Backend-First Only

This would add bulk/audit/health APIs before touching UI. It is clean from an
API perspective, but the user's pain is operational workflow, so delivering
visible UI improvements earlier is more useful.

## 5. Feature Design

### 5.1 Model Sync + Bulk Bind

Add a Provider-page action: **同步模型**.

Flow:

1. The operator opens the sync sheet from a provider row.
2. The sheet calls `/providers/:id/upstream-models`.
3. Models are shown in a dense selectable table with search.
4. Existing local aliases/channels are marked:
   - `已绑定`: alias + provider + upstream model already exists.
   - `已有别名`: alias exists but this provider channel is missing.
   - `新模型`: no matching alias exists.
5. The operator selects rows and confirms.
6. The backend creates missing aliases and binds missing channels in one
   transaction.

Default alias mapping:

- Alias defaults to the upstream model ID.
- Display name defaults to the upstream display name, then upstream model ID.
- Per-row alias can be edited before submit.
- Weight/priority/enabled defaults match the existing channel form.

Backend endpoint:

```text
POST /api/admin/providers/:id/bulk-model-channels
```

Request:

```json
{
  "items": [
    {
      "upstream_model": "gpt-4o-mini",
      "alias": "gpt-4o-mini",
      "display_name": "GPT-4o mini"
    }
  ],
  "weight": 1,
  "priority": 0,
  "enabled": true
}
```

Response:

```json
{
  "created_models": 3,
  "created_channels": 3,
  "skipped_channels": 1,
  "items": [
    {
      "alias": "gpt-4o-mini",
      "upstream_model": "gpt-4o-mini",
      "status": "created"
    }
  ]
}
```

Behavior:

- Idempotent for existing `(model_id, provider_id, upstream_model)` channels.
- Fails individual rows only for validation conflicts, while still returning a
  clear per-row result. If a database transaction fails, no partial write is
  committed.
- Invalid aliases are rejected before submit in the UI and again in the API.

### 5.2 Diagnostic Enhancements

Keep current provider and model-channel diagnostic entry points.

Enhancements:

- Generate and show a diagnostic request ID for each test.
- Keep the last 20 diagnostic results in browser `localStorage`, grouped by
  provider/channel key. This is intentionally local history, not a new table.
- Add a compact history list in the diagnostic sheet. Selecting a previous run
  shows the old result.
- Add a `stream` toggle only where the current protocol and backend helper can
  support it safely. If streaming support is larger than expected, the toggle is
  deferred and the rest ships first.
- Show the resolved route fields for gateway-path tests:
  client protocol, alias, provider, upstream model, HTTP status, latency, usage,
  stop reason, and response preview.

Backend changes:

- Add `request_id` to `DiagnosticResult`.
- Accept optional `stream` in diagnostic inputs, but non-streaming remains the
  default and required fallback.
- Keep diagnostics from influencing health/circuit-breaker metrics.

### 5.3 Operation Feedback Cleanup

Normalize admin forms:

- Successful create/update/delete shows a toast and refreshes affected queries.
- Create/update sheets close by default.
- Bulk workflows show an inline summary after submit.
- For workflows where repeated entry is common, add a secondary action:
  **保存并继续**.

Initial targets:

- Provider form.
- Model form.
- Channel form.
- Client profile form.
- Router policy form.
- User/quota/key flows.

### 5.4 Log Detail Drawer

The Logs page remains a table. Clicking a row opens a drawer.

Drawer sections:

- Request: request ID, timestamp, user ID, API key ID, protocol, model, stream.
- Routing: provider ID, upstream model, status, HTTP status, stop reason.
- Timings: latency, TTFT.
- Usage: input/output/cache/reasoning tokens.
- Error: error code and error message.

Backend:

- The existing list row already contains the fields required for v1 details.
- Add `GET /api/admin/logs/:id` only if implementation needs a direct lookup for
  refresh/deep-link support; otherwise the drawer uses the selected row data.

### 5.5 Health Status Page

Enable `/health` in the sidebar for desktop.

Backend endpoint:

```text
GET /api/admin/health
```

Response shape:

```json
{
  "data": [
    {
      "provider_id": "uuid",
      "provider_name": "OpenAI",
      "upstream_model": "gpt-4o-mini",
      "total": 120,
      "failures": 2,
      "slow": 5,
      "error_rate": 0.016,
      "slow_rate": 0.041,
      "open": false,
      "opened_ago_s": 0,
      "thresholds": {
        "error_rate": 0.3,
        "p95_ttft_ms": 8000,
        "window_sec": 60,
        "cooldown_sec": 30
      }
    }
  ]
}
```

Data source:

- Query enabled model channels from PostgreSQL.
- For each `(provider_id, upstream_model)`, call `health.Store.Stats`.
- If Redis is unavailable, return a degraded but successful response with a
  warning flag instead of failing the page.

UI:

- Summary cards: total samples, open circuits, failing providers, Redis status.
- Table grouped by provider, with badges for open/closed circuits.
- Refresh button and 15-30 second auto-refresh.

### 5.6 Audit Logging + Audit Page

Record Admin write operations into the existing `audit_logs` table.

Scope:

- Provider create/update/delete.
- Model create/delete.
- Channel create/delete and bulk channel creation.
- Client profile create/delete.
- Router policy upsert.
- User create/update/delete.
- API key issue/revoke.
- Quota update.
- Reload action.

Audit row fields:

- `actor_id`: null for now because admin auth is a static token.
- `action`: stable action string, for example `provider.create`.
- `target_type`: provider/model/channel/user/etc.
- `target_id`: target UUID when available.
- `diff`: JSON summary. Secrets are redacted.
- `request_id`: from request context when available; otherwise empty.

Backend endpoint:

```text
GET /api/admin/audit-logs
```

Filters:

- action
- target_type
- target_id
- q
- from/to
- limit/offset

UI:

- Add sidebar item **审计日志** under observability.
- Table with filters and pagination.
- Detail drawer showing timestamp, action, target, request ID, and formatted
  redacted diff JSON.

## 6. Architecture Notes

- Keep Admin API as the single backend boundary for all new UI behavior.
- Keep all runtime config writes inside Store transactions and invalidate the
  registry after successful commit.
- Add audit recording inside the same transaction as the write where practical.
  For read-like actions such as reload, record audit after the action succeeds.
- Do not expose provider API keys or API key plaintext in audit diffs.
- Reuse existing UI primitives: `DataTable`, `FormSheet`, `ConfirmDialog`,
  `Sheet`, `Badge`, and TanStack Query.

## 7. Testing

Backend:

- Unit/integration tests for bulk model/channel creation idempotency.
- Admin route tests for health and audit listing.
- Audit tests proving sensitive fields are redacted.
- Existing diagnostics tests continue passing.

Frontend:

- `pnpm lint`.
- `pnpm build`.
- Focused component coverage only where local logic is non-trivial, especially
  bulk sync row state and audit/log detail rendering if test setup exists.

Manual smoke:

- Login page and main admin routes return expected HTTP statuses.
- Provider model sync creates aliases/channels and refreshes Models page.
- Diagnostic history resets per provider/channel and persists after page reload.
- Logs drawer opens from a row.
- Health page renders both normal and Redis-unavailable responses.
- Audit page shows rows after admin writes.

## 8. Rollout Order

1. Bulk model/channel setup.
2. Diagnostic history and result improvements.
3. Log detail drawer and health endpoint/page.
4. Form operation feedback cleanup.
5. Audit recording and audit page.

This order front-loads operator setup and debugging value, then adds governance.

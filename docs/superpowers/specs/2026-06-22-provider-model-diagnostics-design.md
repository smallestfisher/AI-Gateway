# Provider & Model Diagnostics — Design Spec

**Date:** 2026-06-22
**Status:** Approved design -> written spec review next.

> Add operator-facing diagnostics for upstream providers and configured model
> channels. The feature has two modes: direct provider testing and full gateway
> path testing. Both are reachable from the Provider page, and channel-level
> full-path tests are also reachable from the Model page.

---

## 1. Context & Existing Capabilities

- Admin already exposes `GET /api/admin/providers/:id/upstream-models`.
  It calls the provider's `/v1/models` endpoint and returns model IDs.
- The Model page's channel binding form already uses that endpoint to populate
  the `upstream_model` selector, with manual input fallback.
- The proxy plane already exposes OpenAI-compatible `GET /v1/models` for model
  aliases visible to a client.
- There is no admin action that sends a real minimal request to a provider or
  verifies that a configured alias/channel works through the gateway pipeline.

This feature makes model discovery visible on the Provider page and adds real
diagnostic requests for both direct upstream checks and full configured-path
checks.

## 2. Goals

- Let an operator verify a provider before or after enabling it:
  base URL, API key, proxy egress, protocol adapter, and upstream model.
- Let an operator verify that a provider also works as part of the gateway:
  client protocol -> alias -> selected provider/channel -> upstream -> response.
- Make the test entry points available where operators configure the thing they
  are testing: Provider rows and Model channel rows.
- Keep the first version non-streaming and low-risk. Advanced request simulator
  views remain separate future work.

## 3. Non-Goals

- No full request simulator UI with IR/upstream body/header stage tabs in this
  iteration.
- No streaming diagnostics in v1.
- No bulk import of all upstream models into local aliases. Discovery may inform
  manual binding, but mass import is a separate workflow.
- No exposure of upstream API keys, Authorization headers, or full sensitive
  request bodies in responses.

## 4. Decisions

| Decision | Choice |
|----------|--------|
| Test surfaces | Provider page + Model page channel rows |
| Provider test modes | Direct upstream test and gateway-path test |
| Gateway test routing | Default route by alias, or force a selected provider/channel |
| Request shape | Minimal non-streaming prompt (`"ping"`) with optional custom message |
| Persistence | Do not create audit/log rows in v1; show result immediately |
| Security | Never return API keys or full auth headers to the browser |

## 5. Admin API

### 5.1 Existing Model Discovery

Keep the existing endpoint:

```text
GET /api/admin/providers/:id/upstream-models
```

The Provider page uses it to show supported upstream models and to populate
test selectors. The channel binding form continues using it as it does today.

If a provider does not support `/v1/models`, the UI shows a clear failure and
keeps manual model input available.

### 5.2 Direct Provider Test

```text
POST /api/admin/providers/:id/test-upstream
```

Request:

```json
{
  "upstream_model": "gpt-4o-mini",
  "message": "ping",
  "timeout_ms": 30000
}
```

Response:

```json
{
  "ok": true,
  "mode": "upstream",
  "provider_id": "uuid",
  "provider_name": "OpenAI",
  "protocol": "openai_chat",
  "upstream_model": "gpt-4o-mini",
  "latency_ms": 812,
  "http_status": 200,
  "stop_reason": "end_turn",
  "usage": {
    "input_tokens": 8,
    "output_tokens": 3
  },
  "response_preview": "pong"
}
```

Failure response keeps the same shape with `ok:false` and an error block:

```json
{
  "ok": false,
  "mode": "upstream",
  "provider_id": "uuid",
  "protocol": "openai_chat",
  "upstream_model": "bad-model",
  "latency_ms": 420,
  "http_status": 404,
  "error": {
    "code": "upstream_404",
    "message": "upstream returned status 404",
    "body_preview": "{\"error\":{\"message\":\"model not found\"}}"
  }
}
```

Behavior:

- Loads the provider row directly from the database so disabled providers can be
  tested before being enabled.
- Builds a synthetic `registry.Channel` using that provider and selected
  `upstream_model`.
- Sends a minimal `ir.UnifiedRequest` through the provider's adapter and egress
  path. This validates protocol body shaping, auth headers, proxy, timeout, and
  upstream response decoding.
- Does not require the model alias or channel binding to exist.

### 5.3 Gateway Path Test

```text
POST /api/admin/test-gateway
```

Request:

```json
{
  "client_protocol": "openai_chat",
  "alias": "gpt-4o",
  "provider_id": "uuid",
  "upstream_model": "gpt-4o-mini",
  "message": "ping",
  "timeout_ms": 30000
}
```

`provider_id` and `upstream_model` are optional. Omitting `provider_id` means
"use normal routing for this alias"; providing it means "force this configured
provider/channel for the test".

Response:

```json
{
  "ok": true,
  "mode": "gateway",
  "client_protocol": "openai_chat",
  "alias": "gpt-4o",
  "provider_id": "uuid",
  "provider_name": "OpenAI",
  "upstream_model": "gpt-4o-mini",
  "latency_ms": 935,
  "http_status": 200,
  "stop_reason": "end_turn",
  "usage": {
    "input_tokens": 8,
    "output_tokens": 3
  },
  "response_preview": "pong"
}
```

Behavior:

- When `provider_id` is omitted, the test follows normal routing for the alias.
- When `provider_id` is present, the test finds an enabled channel for
  `alias + provider_id`, optionally matching `upstream_model`, and forces that
  channel for the request. This lets a provider row test its own configured
  gateway path even if route policy would normally choose another provider.
- Uses the same runtime snapshot as production routing, so disabled providers,
  disabled aliases, and disabled channels fail as they would for real clients.
- Creates a minimal client-protocol request and decodes it with the selected
  ingress adapter before executing the pipeline/egress path. This catches client
  protocol conversion issues, not only provider connectivity.

## 6. Backend Architecture

Add an admin diagnostics layer beside the existing CRUD store:

```text
internal/admin/diagnostics.go
  - List provider model support (reuse existing Store.ListUpstreamModels)
  - TestProviderUpstream(ctx, providerID, input)
  - TestGatewayPath(ctx, input)
```

Dependencies:

- `Store` for database reads of provider rows in direct upstream tests.
- `adapter.Registry` for ingress and egress adapters.
- `pipeline.Pipeline` or its `Egress` collaborator for full-path execution.
- Runtime registry snapshot for enabled alias/channel lookup.

Implementation notes:

- Reuse the same provider projection rules as runtime config loading, including
  base URL, protocol, API key, proxy, timeout, and metadata headers.
- Use egress code for actual HTTP transport so timeout/proxy/header behavior
  matches production.
- Add a small forced-channel execution helper rather than mutating route policy
  or temporary database rows.
- Convert upstream and decode errors into stable admin error codes:
  `validation_error`, `provider_not_found`, `model_not_found`,
  `channel_not_found`, `protocol_disabled`, `upstream_unreachable`,
  `upstream_<status>`, `decode_failed`.
- Redact secrets before returning diagnostics to the browser.

## 7. Frontend UX

### Provider Page

Each provider row gains a diagnostics action.

The diagnostics sheet has three compact sections:

1. **Supported Models**
   - Calls `/providers/:id/upstream-models`.
   - Shows a searchable list of upstream model IDs.
   - Allows selecting a model for direct testing.
   - If model discovery fails, shows the error and leaves manual input enabled.

2. **Direct Upstream Test**
   - Inputs: upstream model, optional prompt message.
   - Calls `/providers/:id/test-upstream`.
   - Shows status, latency, protocol, HTTP status, response preview, usage, and
     compact error detail.

3. **Gateway Path Test**
   - Selects one of the enabled model channels currently bound to this provider.
   - Inputs: client protocol and optional prompt message.
   - Calls `/api/admin/test-gateway` with `alias`, `provider_id`, and
     `upstream_model`.
   - Shows the actual provider/upstream model used, so operators can verify that
     the configured channel was exercised.

### Model Page

In the expanded channel list, each channel row gains a test action.

- The action opens a focused gateway-path test sheet.
- It pre-fills `alias`, `provider_id`, and `upstream_model` from the row.
- It allows changing only client protocol and prompt message.
- It calls `/api/admin/test-gateway`.

This is the fastest way to validate "this alias routes through this channel".

## 8. Result Display

Both test modes share a result component:

- Status badge: success / failed.
- Latency and HTTP status.
- Provider name and upstream model.
- Client protocol for gateway-path tests.
- Stop reason and token usage when available.
- Response preview capped to a small length.
- Error code and body preview when available.

No raw Authorization headers, API keys, or full upstream bodies are shown.

## 9. Error Handling

- Validation errors are displayed inline before sending.
- `/v1/models` discovery failure does not block manual direct tests.
- Direct upstream tests can run against disabled providers.
- Gateway-path tests require enabled runtime config and fail with a clear
  `channel_not_found` or `model_not_found` if the provider/channel is disabled
  or unbound.
- Timeouts return `upstream_unreachable` with elapsed latency.
- Non-2xx upstream statuses are shown as failed diagnostics, not thrown away.

## 10. Testing Plan

Backend:

- Unit tests for request validation and stable error-code mapping.
- `httptest.Server` tests for provider `/v1/models` and direct upstream test.
- Gateway-path tests with a fake upstream and two channels, verifying:
  normal alias routing can succeed;
  forced provider/channel uses the selected provider;
  disabled/unbound channels return `channel_not_found`;
  response previews are capped and auth headers are redacted.

Frontend:

- Verify Provider page diagnostics action opens the sheet.
- Mock successful and failed model discovery.
- Mock direct test and gateway-path test result rendering.
- Verify Model channel row test pre-fills alias/provider/upstream model.
- Existing `pnpm lint` and `pnpm build` remain required.

## 11. Rollout

1. Backend endpoints and tests.
2. Provider page diagnostics sheet using existing model discovery.
3. Model channel row gateway-path test action.
4. Run Go targeted tests, frontend lint, and frontend build.

This sequence keeps the backend contract stable before adding UI entry points.

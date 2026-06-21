# Config Admin UI — Design Spec

**Date:** 2026-06-21
**Sub-project:** 1 of 2 (Admin Web UI). Followed by the Log Center.
**Status:** Approved design → implementation plan next.

> Build a beautiful, modern **Next.js admin web UI** over the existing
> `/api/admin/*` REST API. This sub-project is **frontend-only** (no Go changes),
> focused on configuration CRUD. The Log Center (which needs a backend logging
> component) is sub-project 2.

---

## 1. Context & gaps addressed

- The Admin API (`internal/admin`) is complete: CRUD over providers, models,
  model-channels, client-profiles, router-policies, plus users / API keys /
  quotas, all behind a single static bearer token (`GATEWAY_ADMIN_TOKEN`).
- `docs/09-frontend.md` defines a full 10-module prototype and the stack
  (Next.js + Tailwind + shadcn/ui + TanStack Query).
- **Gaps this sub-project fills:** there is no `apps/web` scaffold today. We add
  the Next.js app, app shell, auth, design system, and the **configuration**
  pages.
- **Explicitly deferred** (sub-project 2 or later): Log Center, Dashboard,
  Health page, Request Simulator (all need `request_logs` data, which nothing
  writes yet); Protocol management (static builtins); MCP (reserved v2);
  provider test-connection action; Storybook.

## 2. Decisions (from brainstorming)

| Decision | Choice |
|----------|--------|
| Scope / order | Config Admin UI first; Log Center second |
| Visual direction | **Minimal dev-tool** (Linear / Vercel) — flat, monochrome, hairline borders, light/dark |
| Auth | **Token login → httpOnly cookie** (no backend changes) |
| Integration | **Approach A — Next.js Route-Handler proxy (BFF)** |
| Production topology | Separate `web` process; `docker-compose` `web` service |

## 3. Architecture

```
Browser ──(same origin)──▶ Next.js app (apps/web, :3000)
                               │
                               ├─ app/api/admin/[...path]/route.ts
                               │     reads admin_token cookie → forwards as
                               │     Authorization: Bearer
                               ▼
                          Gateway :8080  /api/admin/*
```

- The browser only ever calls same-origin `/api/admin/*`. A catch-all **Route
  Handler** reads the `admin_token` **httpOnly cookie** and forwards to
  `GATEWAY_URL` with `Authorization: Bearer <token>`.
- **No CORS, no gateway changes, token never enters the browser JS.** This is the
  standard backend-for-frontend (BFF) pattern, required by the httpOnly-cookie
  auth decision.
- Stack: **Next.js (App Router) · TypeScript · Tailwind · shadcn/ui (Radix+CVA)
  · TanStack Query v5 · react-hook-form + zod · next-themes · lucide-react.**

### Project layout

```
apps/web/
  app/
    (admin)/             guarded route group (providers, models, profiles, router, users)
    login/               public login page
    api/
      auth/login|logout/route.ts
      admin/[...path]/route.ts   # BFF proxy
    layout.tsx, globals.css
  components/            ui primitives + feature components (provider-form, data-table, ...)
  lib/                   api.ts (typed client), types.ts (mirror Go DTOs), query-keys.ts
  hooks/                 TanStack Query hooks per resource
  middleware.ts          auth guard
  next.config.ts, tailwind.config.ts, components.json (shadcn)
  package.json (pnpm)
```

## 4. Auth flow (single-admin; matches the static-token gateway)

1. `/login` — one field (admin token), submit.
2. `POST /api/auth/login` — server verifies by calling gateway
   `GET /api/admin/config/version` with the token; on 200 sets cookie
   `admin_token`: **httpOnly, Secure in prod, SameSite=Lax, Max-Age 7d**.
3. `middleware.ts` guards everything except `/login` and `/api/auth/*`; no cookie
   → redirect to `/login`.
4. BFF route handler injects `Authorization: Bearer`; on **401 from gateway** it
   clears the cookie and returns 401, which the client turns into a login
   redirect.
5. `POST /api/auth/logout` clears the cookie.

> No per-user distinction (the gateway has one static admin token). Sufficient
> for an operator-facing panel.

## 5. Design system — Minimal dev-tool (Linear / Vercel)

- **Palette:** monochrome zinc/neutral. Near-white canvas (light), near-black
  (dark). **1px hairline borders** at low opacity.
- **Accent:** a single restrained accent reserved for *primary actions, focus
  rings, active nav* (primary button = solid foreground; focus/links = blue).
  Status colors (green / amber / red) muted.
- **Type:** Inter / system stack, 13–14px base, `tabular-nums` on numeric
  columns. Whitespace is a feature.
- **Components:** flat Card (hairline border, no heavy shadow), compact Table,
  Sheet/drawer forms, Badge, Button (ghost/outline/solid), Select, Switch,
  Tooltip, Toast (sonner). Subtle motion (skeleton shimmer, dialog fade);
  respects `prefers-reduced-motion`.
- Theming via **next-themes** + Tailwind CSS variables (light/dark/system).

## 6. App shell

- **Sidebar** (~220px, collapsible): `Configuration` (Providers, Models, Client
  Profiles, Router) + `Access` (Users & Keys). Logs/Health/Protocols/MCP shown
  disabled with a "deferred" tooltip.
- **Topbar:** breadcrumb/title · config-version pill (polls `/config/version`) ·
  theme toggle · admin menu (logout).
- **States:** skeleton shimmer · empty state with CTA · error state with retry.

## 7. Pages (phase-1 scope) — config CRUD, 1:1 to `/api/admin/*`

Each page: list (DataTable) + create/edit (Sheet/drawer) + delete (confirm);
zod validation; mutation → invalidate list → toast.

| Page | Route | Operations | Key form fields |
|------|-------|------------|-----------------|
| Providers | `/providers` | list/create/edit/delete | name, protocol(openai_chat/anthropic_messages/openai_responses), base_url, api_key(write-only), proxy_id, timeouts, retries, weight, priority, hc thresholds, metadata(JSON), enabled |
| Models + Channels | `/models` | alias CRUD; expand row → channel bind/unbind | alias, display_name; channel: provider, upstream_model, weight, priority |
| Client Profiles | `/profiles` | CRUD | scope(default/provider/model), target, user_agent, origin, referer, headers(k-v editor), strip_client_headers, enabled |
| Router | `/router` | global policy + per-model overrides | mode(failover/weighted/auto), params |
| Users & Keys | `/users` | user create/disable; per-user API keys; quota | issue key (shown once), balance, rpm, tpm, model whitelist |

## 8. Data layer

- `lib/types.ts` mirrors the Go DTOs (`Provider`, `Model`, `ModelChannel`,
  `ClientProfile`, `RouterPolicy`, `User`, `APIKey`, quota fields).
- `lib/api.ts` typed fetch over the proxied `/api/admin/*`.
- `hooks/*` TanStack Query: `useProviders`, `useCreateProvider`, …; mutations
  invalidate the right query keys.
- Error normalization: gateway `{error:{code,message}}` → toast; 401 → login.

## 9. Testing & dev workflow

- **Vitest + @testing-library/react:** form/validation + a couple of mocked-API
  hook tests (proportional, not exhaustive). No Go tests this phase.
- **Dev:** `pnpm dev` (gateway running on :8080; paste token at `/login`).
  `docker-compose` gains a `web` service. `pnpm build` / `pnpm lint`.
- **Prerequisite:** Node 20+ / pnpm — verified available before building; if
  missing, surface to the user.

## 10. Out of scope (deferred)

Log Center + Dashboard + Health + Simulator (need `request_logs` writes,
sub-project 2) · Protocol management · MCP · provider test-connection ·
Storybook · single-binary embedding (revisit later).

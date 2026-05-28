# Workspace authentication (canonical)

Single reference for workspace-scoped authentication in agentserver: design choice, shipped implementation, DEV validation, production blockers, and follow-up backlog.

**Related plans (not archived):**

- [`docs/plans/cursor_workspace-subdomain-auth.md`](plans/cursor_workspace-subdomain-auth.md) — implementation plan (Opção A)
- [`docs/plans/2026-05-27-workspace-subdomain-auth.md`](plans/2026-05-27-workspace-subdomain-auth.md) — TDD plan (superseded by merge)

---

## Overview

agentserver is a multi-tenant SaaS where each **workspace** maps to a customer organization. Authentication must answer:

1. **Which workspace** is the user operating in? (tenant context)
2. **Is the user allowed** to act in that workspace? (membership)

Two layers shipped on `main`:

| Layer | PR | Mechanism |
|-------|-----|-----------|
| Session workspace binding | [#53](https://github.com/CarlosSalvador-vtex/agentserver/pull/53) | `auth_tokens.active_workspace_id` — set via API or at login |
| Subdomain login (Opção A) | [#57](https://github.com/CarlosSalvador-vtex/agentserver/pull/57) + [#58](https://github.com/CarlosSalvador-vtex/agentserver/pull/58) | `{slug}.<base-domain>/login` infers workspace from host; cookie host-only per tenant |

**URLs (DEV example):**

| Pattern | Example | Behavior |
|---------|---------|----------|
| Apex | `https://agentserver.analytics.vtex.com/login` | Global login; optional `workspace_slug` in body; picker via `POST /api/auth/session/workspace` |
| Tenant | `https://{slug}.agentserver.analytics.vtex.com/login` | Workspace locked by subdomain; `active_workspace_id` set at login |
| Sandbox | `https://claw-{id}.agentserver.analytics.vtex.com` | Routed to sandboxproxy (not workspace UI) |

**Decision (DD1-A):** One canonical doc — not split across six overlapping files. Historical sources are in [`docs/archive/`](archive/).

---

## Design

**Chosen approach: Opção A — subdomain per workspace** (implemented in PR #57/#58).

Each workspace has a unique **`slug`** (`workspaces.slug`, migration `040`). Users sign in at:

```text
https://<slug>.<BASE_DOMAIN>/login
```

The backend validates membership, sets `active_workspace_id` on the session token, and issues a **host-only** session cookie (no `Domain` attribute) so sessions do not leak across tenant subdomains.

### Why Opção A (vs B/C)

| Criterion | Opção A — Subdomain | Opção B — SSO per workspace | Opção C — Hybrid |
|-----------|---------------------|----------------------------|------------------|
| LOC | ~150 (shipped) | ~600 | ~700 |
| Per-workspace branding (URL) | Yes | Via IdP redirect | Yes |
| Bring-your-own IdP (Google/Okta/SAML) | No | Yes | Yes |
| Multi-workspace user UX | Separate login per subdomain | Single global login | Mixed |
| Time to deliver | 1 sprint | 3+ sprints | 4+ sprints |

**Recommendation retained:** ship A first; evolve to C when enterprise compliance requires SSO (see backlog B4/B5).

Options B and C are **not implemented**. Full comparison tables live in [`docs/archive/workspace-auth-design.md`](archive/workspace-auth-design.md).

### Security principles (Opção A)

- **Slug squatting:** reserved words and prefixes (`www`, `api`, `admin`, `claw`, `hermes`, etc.) — see `internal/db/slug.go`
- **Enumeration:** invalid slug and wrong password both return generic `401 invalid credentials`
- **Cookie scope:** host-only on tenant hosts — never `Domain=.<base>` for tenant login
- **Register:** use apex only — register on tenant subdomain is not supported (cross-tenant risk)

### Predecessor: session-level workspace (PR #53)

Before subdomain auth, the session cookie did not carry workspace context. PR #53 added `auth_tokens.active_workspace_id`:

- Fresh login → `active_workspace_id` is `NULL` → UI shows workspace picker
- `POST /api/auth/session/workspace` sets/clears active workspace (membership validated)
- Middleware exposes `auth.ActiveWorkspaceFromContext(ctx)` to handlers

Subdomain login **builds on** PR #53 by setting `active_workspace_id` during `POST /api/auth/login` when `workspace_slug` is present.

---

## Implementation

### Database

| Item | Detail |
|------|--------|
| Migration **039** | `auth_tokens.active_workspace_id` → `workspaces(id)` (`ON DELETE SET NULL`) |
| Migration **040** | `workspaces.slug` NOT NULL, unique; backfill via `slugify(name)` with `-2`, `-3` on collision |
| Helpers | `internal/db/slug.go` — `ValidateSlug`, `Slugify`, reserved prefixes |

### API

| Endpoint | Behavior |
|----------|----------|
| `POST /api/auth/login` | Optional `workspace_slug`; if omitted, inferred from `Host` via `ResolveWorkspaceSlugFromHost` |
| `POST /api/auth/session/workspace` | Bind/clear `active_workspace_id` (PR #53); `403` if not a member |
| `GET /api/auth/me` | Returns `active_workspace_id` (nullable) |
| `POST /api/workspaces` | Optional `slug`; auto-derived from `name` if omitted; `409` if duplicate |
| OIDC callback | Workspace inferred from tenant host when applicable |

### Backend (auth)

- `Auth.LoginWithWorkspace` — validates `IsWorkspaceMember`, issues token with `active_workspace_id`
- `auth.SetTokenCookieHostOnly` on tenant login (`HostOnlySessionCookie`)
- Generic errors for bad slug / bad password (no tenant enumeration)

### Frontend

| File | Change |
|------|--------|
| `web/src/lib/hostname.ts` | `extractWorkspaceSlug`, `isTenantSubdomain`, `ROOT_HOSTS` |
| `web/src/components/Login.tsx` | Banner “Signing in to workspace …”; sends `workspace_slug` |
| `web/src/components/CreateWorkspaceModal.tsx` | Editable slug + preview URL |
| TopBar | Workspace switcher **hidden** on tenant subdomain |

### Sandboxproxy (PR #58)

Wildcard ingress `*.<base>` previously routed only `claw-*` / `hermes-*` to sandboxproxy; other hosts got 404 on `/login`.

**Fix:** `AGENTSERVER_UPSTREAM` on sandboxproxy reverse-proxies non-sandbox hosts to the main agentserver service (preserves `Host` for slug extraction).

| File | Change |
|------|--------|
| `internal/sandboxproxy/config.go` | `AgentserverUpstream` |
| `internal/sandboxproxy/server.go` | Fallback `ReverseProxy` after claw/hermes routes |
| `deploy/helm/agentserver/templates/sandboxproxy.yaml` | `AGENTSERVER_UPSTREAM` env |

### Auth paths and workspace context

| Path | Workspace context |
|------|-------------------|
| Cookie session (`Auth.Middleware`) | `active_workspace_id` from token |
| Hydra Bearer (TUI/CLI) | No active workspace — pass workspace explicitly |
| Workspace API keys | Scoped by key record |
| codexauth (PKCE) | User-scoped; cross-subdomain cookie policy separate (backlog B8) |

### Key files

| Area | Files |
|------|-------|
| Session / context | `internal/auth/auth.go`, `internal/db/tokens.go`, `internal/db/migrations/039_*.sql` |
| Slug / workspace | `internal/db/slug.go`, `internal/db/workspaces.go`, `internal/db/migrations/040_*.sql` |
| HTTP | `internal/server/auth_login.go`, `internal/server/server.go` |
| Proxy | `internal/sandboxproxy/server.go`, `internal/sandboxproxy/config.go` |

---

## E2E Smoke

**Environment:** DEV cluster `dev-ti-eks-analytics-platform`, `BASE_DOMAIN=agentserver.analytics.vtex.com`  
**Date:** 2026-05-27  
**PRs:** [#57](https://github.com/CarlosSalvador-vtex/agentserver/pull/57) merged, [#58](https://github.com/CarlosSalvador-vtex/agentserver/pull/58) merged (routing fix).

### Preconditions verified

- Migration `040` applied (`Applied migration: 040_workspace_slug.sql` in pod logs)
- PR #53 (`active_workspace_id`) already on `main`
- Images: `agentserver:auth-slug`, `sandboxproxy:tenant-fallback` (manual DEV deploy during validation)

### Smoke results (summary)

| Area | Result |
|------|--------|
| Wildcard routing (`{slug}.<base>/login`, `/api/auth/me`) | **Pass** after PR #58 (was 404 before fix) |
| Apex login + `workspace_slug` in body | **Pass** — `active_workspace_id` correct on `/api/auth/me` |
| Cookie host-only | **Pass** — `FALSE` subdomain inherit on apex cookie |
| Invalid slug / wrong password | **Pass** — both `401 invalid credentials` (no enumeration) |
| Browser tenant login UI | **Pass** — banner, redirect, no workspace switcher |
| Workspace CRUD via API | **Pass** — create, duplicate slug 409, reserved 400, invalid format 400, auto-derive slug |
| Multi-user on tenant subdomain | **Pass** — register → admin adds member → login on tenant subdomain |
| Session isolation across tenants | **Pass** — logout on one tenant does not affect another |

### Tenant subdomain UI (expected)

On `https://{slug}.<base>/login` after login:

| Element | Visible? |
|---------|----------|
| Sidebar (Overview, Sandboxes, Members, …) | Yes |
| User menu (top right) | Yes (may clip on narrow viewports) |
| Workspace switcher | **No** (intentional — change workspace = change URL or use apex picker) |

### Test artifacts (DEV)

Workspaces/users created during smoke may remain in DEV (optional cleanup): see archived smoke doc for IDs.

**Not tested in DEV:** OIDC callback on tenant host (no IdP configured).

Full step-by-step evidence: [`docs/archive/pr-57-pr-58-e2e-smoke-2026-05-27.md`](archive/pr-57-pr-58-e2e-smoke-2026-05-27.md).

---

## Pending Blockers

Items that block or complicate **production** rollout (distinct from feature backlog).

| ID | Item | Detail | Owner |
|----|------|--------|-------|
| **P1** | CI/CD image promotion | `agentserver:auth-slug` and `sandboxproxy:tenant-fallback` were built manually in DEV. Pipeline publishes on `main` push — need staging → prod promotion ([#15](https://github.com/CarlosSalvador-vtex/agentserver/issues/15) staging exists) | Eng/Ops |
| **P2** | Wildcard DNS + ACM cert in PROD | DEV has `*.agentserver.analytics.vtex.com`. Production needs equivalent wildcard + renewable cert | Infra |
| **P3** | Cookie policy review in PROD | Confirm `Secure`, `HttpOnly`, `SameSite`; no `Domain` on tenant login | Security |
| **P4** | Staging smoke before prod | Re-run checklist from E2E section on staging after P1 | QA |

---

## Backlog

Optional follow-ups (multi-tenancy level 2). Handoff prompts may exist under `docs/cursor-handoffs/`.

| ID | Item | Summary | Est. LOC |
|----|------|---------|----------|
| **B1** | Invite-by-email flow | `workspace_invites` table, invite/accept API, email with tenant subdomain URLs | ~200 |
| **B2** | `DELETE /api/workspaces/{id}` | Soft vs hard delete, cascade sandboxes/members | ~80 |
| **B3** | `DELETE /api/users/{id}` | Admin-only; GDPR anonymization; ownership transfer rules | ~120 |
| **B4** | Opção B — SSO per workspace | `workspace_sso_configs`, OAuth/SAML handlers, admin UI | ~600 |
| **B5** | Opção C — hybrid SSO + password fallback | Builds on B4; break-glass password when IdP down | ~150 (+B4) |
| **B6** | Tenant subdomain URLs in all emails | Invites, password reset, notifications use `{slug}.<base>` | ~30 |
| **B7** | Session/workspace audit log | Middleware + `session_audit_events` (or extend draft audit) | ~150 |
| **B8** | codex-auth vs host-only cookies | Reconcile cross-subdomain SSO with tenant isolation | TBD |
| **B9** | Apex “choose workspace” UI | After apex login, picker then redirect to chosen tenant subdomain | ~130 |
| **B10** | Expand reserved slug list | mail, support, billing, legal, etc. — see archived pendencies | ~5 |

### Resolved (tracking)

| ID | Resolution |
|----|------------|
| F1 | PR #56 merged — design doc published |
| F2 | Design status updated post-merge (see archived sources) |
| F3 | Branch `chore/bump-image-auth-session` removed |
| F4 | SQL cleanup of smoke workspaces/users in DEV — **pending** |
| F5 | OIDC subdomain stamp — **pending** IdP setup in DEV |
| F6 | OIDC subdomain E2E — depends on F5 |

Detail for F4–F6 and full B1–B10 narratives: [`docs/archive/workspace-auth-pendencies.md`](archive/workspace-auth-pendencies.md).

---

## Risks & Decisions Pending

| Risk | Mitigation |
|------|------------|
| Corporate slug squatting | Reserved list + optional manual approval for sensitive names |
| Multi-workspace users re-login per subdomain | Accepted for B2B (Slack model); revisit if SSO (B4) lands |
| `claw-*` / `hermes-*` vs workspace slugs | Validator blocks reserved prefixes; keep in sync with sandbox naming |
| Register enabled on tenant subdomain | Do not expose register on `{slug}.<base>/register` |
| Accidental `Domain=` cookie on tenants | Code review `SetTokenCookieHostOnly` before prod (P3) |

| Decision | Status |
|----------|--------|
| Soft vs hard delete for workspaces (B2) | Pending |
| Password fallback toggle scope for Opção C (B5) | Per-workspace vs per-user — pending |
| Email provider for invites (B1) | SES vs Resend vs Mailgun — pending |
| codex-auth cookie model (B8) | Security (A) vs UX (B/C) — pending |
| Redirect token security for apex picker (B9) | Avoid token in URL query string — pending |
| Final reserved slug list (B10) | Pending product/legal input |

---

## Archived

The following files were consolidated into this document and moved out of the active doc tree (2026-05-28, activity B3):

| Former path | Role |
|-------------|------|
| [`docs/archive/workspace-auth-design.md`](archive/workspace-auth-design.md) | Design options A/B/C (pre-implementation) |
| [`docs/archive/workspace-session-auth.md`](archive/workspace-session-auth.md) | PR #53 session `active_workspace_id` spec |
| [`docs/archive/pr-57-workspace-subdomain-auth-status.md`](archive/pr-57-workspace-subdomain-auth-status.md) | PR #57 pre-merge status & DEV deploy notes |
| [`docs/archive/pr-57-pr-58-e2e-smoke-2026-05-27.md`](archive/pr-57-pr-58-e2e-smoke-2026-05-27.md) | Full E2E smoke report (2026-05-27) |
| [`docs/archive/workspace-auth-pendencies.md`](archive/workspace-auth-pendencies.md) | Post-merge pendencies F1–F6 + B1–B10 detail |

**Not archived (remain in place):**

- `docs/plans/cursor_workspace-subdomain-auth.md`
- `docs/plans/2026-05-27-workspace-subdomain-auth.md`

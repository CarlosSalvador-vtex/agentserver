# Workspace subdomain authentication (Opção A)

Canonical plan: [`docs/plans/cursor_workspace-subdomain-auth.md`](plans/cursor_workspace-subdomain-auth.md).

## Summary

- Each workspace has a unique `slug` (migration `040_workspace_slug.sql`).
- Login at `https://<slug>.<base-domain>/login` binds `active_workspace_id` on the session token (PR #53).
- Session cookie is **host-only** on tenant subdomains (no `AGENTSERVER_COOKIE_DOMAIN` leakage across workspaces).
- Apex login without slug uses the existing workspace picker + `POST /api/auth/session/workspace`.

## API

| Endpoint | Change |
|----------|--------|
| `POST /api/auth/login` | Optional `workspace_slug`; inferred from `Host` when omitted |
| `POST /api/workspaces` | Optional `slug`; auto-derived from `name` when omitted |
| `GET /api/auth/me` | Returns `active_workspace_id` |

## v1 limitations

- OIDC/GitHub callback on tenant host stamps workspace; apex SSO unchanged.
- Register ignores `workspace_slug` (use apex + membership).
- codex-auth cross-subdomain may need a separate cookie policy (see plan Task 12).

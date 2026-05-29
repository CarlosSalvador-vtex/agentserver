# Why you always land on one workspace (subdomain → workspace binding)

> Explains why logging into a tenant subdomain always drops you into that tenant's
> workspace — even as a global `admin` — and how to move between workspaces.
> Related: `docs/workspace-auth.md`, `docs/cursor-handoffs/B09-choose-workspace-apex.md`,
> `docs/cursor-handoffs/B08-codex-auth-vs-cookie.md`.

## Observed

```
host:                empresa-custom-teste.agentserver.analytics.vtex.com   (SUBDOMAIN)
role:                admin
active_workspace_id: 22707304-…   (= "Empresa Custom Teste")
```

Entering `https://empresa-custom-teste.agentserver…/…` always lands on
`/w/22707304…`, regardless of being a global admin.

## Why

You enter through the **subdomain** `empresa-custom-teste`, which is **bound** to
workspace `22707304`. It is not because of the admin role — it is the **host**.

Two things pin you to that workspace:

1. **Subdomain → fixed workspace.** `empresa-custom-teste.agentserver…` maps to
   workspace `22707304`. The session cookie is **host-only** (PR #57), so it is valid
   only on that subdomain — the session belongs to that tenant.
2. **`active_workspace_id = 22707304`** — set at login on this subdomain. The landing
   logic (`resolveAuthedLandingPath`) routes to the active workspace, so you always
   land on `/w/22707304`.

**Admin is a global role** — it grants extra powers, but it does **not** undo the
subdomain binding. You are an admin *inside* the tenant `22707304`.

## How to move / switch

- **Workspace dropdown** in the topbar (TopBar) → switches to another `/w/<id>` on the
  same host. Works if you are a member (the API authorizes by membership).
- **Apex** `agentserver.analytics.vtex.com` → neutral domain; with PR #123 it lands on
  the last-used / first workspace + dropdown (but you must log in on the apex —
  separate session, the cookie does not cross from the subdomain).
- **Another subdomain** `<other-tenant>.agentserver…` → enters that tenant's workspace
  directly.

## Design rationale

This is the multi-tenant-by-subdomain model: each company has its own isolated host,
and you "are" the workspace of the host you logged into. The host-only cookie keeps
tenant sessions isolated (anti-XSS cross-tenant — see B08).

For an admin who needs to manage **across** tenants, the path is the **`/admin`**
panel (global) or the **apex**, not a specific tenant's subdomain.

## Summary

| Question | Answer |
|----------|--------|
| Why always `/w/22707304`? | The subdomain `empresa-custom-teste` is bound to that workspace + `active_workspace_id` points there |
| Does admin change it? | No — admin is global; the subdomain still pins the workspace |
| Cookie scope | host-only (per subdomain), does not cross to apex or other subdomains |
| Switch workspace | topbar dropdown (same host), apex landing, or another subdomain |
| Cross-tenant admin | `/admin` panel or apex — not a tenant subdomain |

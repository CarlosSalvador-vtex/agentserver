# Superpowers reference material (upstream)

This directory holds **architecture plans and design specs from the upstream [Superpowers](https://github.com/obra/superpowers) AI workflow project. They were copied into this repository as **read-only reference**, not as the agentserver fork’s internal product roadmap.

## What this is — and what it is not

| | |
|---|---|
| **Is** | Historical and reference documentation for agentserver-related features as they were conceived in the upstream project (Codex gateways, IM bridges, OAuth, sandboxes, marketplace, etc.). |
| **Is not** | A commitment list, sprint backlog, or “todo” for this fork. Many items were never implemented here, were superseded, or diverged from current code. |
| **Do not** | Treat filenames here as tickets to implement without checking code, `docs/docs-organization-backlog.md`, or `docs/improvements.md` first. |

For **actionable internal backlog and ship status**, use:

- [`docs/docs-organization-backlog.md`](../docs-organization-backlog.md) — doc hygiene and tiered work (A/B/C)
- [`docs/improvements.md`](../improvements.md) — shipped improvements index
- [`docs/cursor-handoffs/`](../cursor-handoffs/) — open engineering handoffs (B01–B10, etc.)

## Directory layout

| Path | Contents | Typical use |
|------|----------|-------------|
| [`plans/`](plans/) | **53** implementation-oriented plan documents (`YYYY-MM-DD-<topic>.md`). Step-by-step execution plans, often produced by Superpowers planning skills before coding. |
| [`specs/`](specs/) | **38** design specifications (`YYYY-MM-DD-<topic>-design.md` or similar). Architecture, API contracts, and locked decisions that plans reference. |

**Relationship:** A spec is usually the design source of truth; a plan in `plans/` often references a matching file in `specs/` (same date prefix and topic). Either file can be read alone, but reading the spec first gives context for the plan.

## How to use this material

1. **Research** — Understand why a feature was designed a certain way upstream before changing related code in this repo.
2. **Traceability** — Cross-link from internal docs (e.g. eng specs under `docs/eng/`) when explaining provenance: “originally specified in `docs/superpowers/specs/…`”.
3. **Scope checks** — If a superpowers plan describes behavior that does not exist in this fork, assume the fork diverged or the item was never ported—do not implement from this tree alone.

## Editing policy

**Do not edit files under `docs/superpowers/plans/` or `docs/superpowers/specs/`** as part of normal agentserver documentation work. Changes belong in upstream Superpowers or in fork-specific docs (`docs/eng/`, API reference, runbooks). This README is the only file in this tree intended to be maintained in this repository.

## Provenance

Content mirrors upstream Superpowers planning output (dates in filenames reflect upstream authoring, roughly March–May 2026). This fork may contain a subset of upstream history; newer upstream plans are not automatically synced here.

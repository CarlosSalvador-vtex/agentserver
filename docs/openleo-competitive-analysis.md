# openLEO vs agentserver — competitive analysis

> How openLEO (openleo.ai) resembles and diverges from agentserver. Landscape
> note (gstack three-layer framing). Reference fetched 2026-05-29.
> Companion to `docs/openclaw-packaging-prior-art.md`.

## Headline

**openLEO is also built on OpenClaw** — the same agent framework agentserver wraps.
So this is the closest direct comparable found. Same engine, different product
shape: openLEO is a **personal-productivity assistant on a dedicated VPS per agent**;
agentserver is a **multi-tenant B2B platform with skill authoring**.

## What openLEO is (from openleo.ai)

- "A dedicated AI agent on your own server." Personal/team productivity assistant.
- Built on **OpenClaw**; multi-LLM (OpenAI, Anthropic).
- Channels: Telegram, Slack, email, Discord, WhatsApp.
- Features: name + personality + communication style; **memory persistence**
  (projects, preferences, past conversations); **scheduled automations** (morning
  briefs, inbox triage, follow-up reminders, weekly reports); **skills** that connect
  to APIs/workflows ("teach it your workflows").
- Deployment: **one dedicated VPS per agent — "no shared infrastructure, no data
  mixing between users."** Setup 3–5 min.
- Pricing: 3 tiers €13–€38/mo per instance + enterprise.

## Resembles (where we overlap)

- **Same core engine:** both wrap OpenClaw, unmodified (no fork) — multi-LLM.
- **Persona customization:** name + personality + style ≈ our `soul` entity.
- **Skills as the extension mechanism:** both expose "skills" connecting APIs/workflows.
- **Multi-channel IM:** both target WhatsApp + other IM (we have WhatsApp/weixin/
  telegram/matrix; they add Slack/Discord/email).
- **Memory / persistence + scheduled tasks:** both persist context; both run cron-style
  automations (we have cron/scheduler paths; they market briefs/triage/reports).
- **"Your own server" positioning:** both lean on isolation as a selling point.

## Diverges (where we differ)

| Dimension | openLEO | agentserver |
|-----------|---------|-------------|
| **Isolation model** | **1 dedicated VPS per agent** — hard physical isolation, no shared infra | **multi-tenant K8s** — per-workspace namespace + host-only cookie on a shared cluster |
| **Density / cost** | low density, higher per-instance cost (€13–38/mo each) | high density (many workspaces/sandboxes per cluster) |
| **Target user** | individual / team — personal assistant (inbox, calendar, briefs) | B2B tenant orgs — workspaces, members, quotas, admin |
| **Skill authoring** | "teach it your workflows" — config-level skill connect | **Playground**: edit skill (prompt.md no-code + `.mjs` plugin/tools), dry-run, test sandbox, **publish**, composition, marketplace |
| **Multi-tenancy** | one user ≈ one VPS (no cross-tenant by design) | many tenants on one deploy; subdomain-bound; cross-tenant marketplace |
| **Self-serve setup** | 3–5 min, name + personality | workspace + sandbox + composition (heavier, more capable) |
| **Channels breadth** | Telegram, Slack, email, Discord, WhatsApp (broader) | WhatsApp + weixin/telegram/matrix (IM bridge) |

## Three-layer read (gstack landscape)

- **[Layer 1] Conventional wisdom:** "AI personal assistant on your own server,
  multi-channel, customizable persona" — openLEO sits squarely here, polished and
  productized (pricing, 3-min setup, briefs/triage).
- **[Layer 2] Current discourse:** OpenClaw-based assistants are emerging as a category
  (openLEO, the AWS AgentCore sample, agentserver). The differentiator is no longer the
  engine — it's the surrounding platform (isolation, authoring, multi-tenancy).
- **[Layer 3] First-principles divergence:** agentserver's bet is **multi-tenant SaaS +
  in-product skill authoring**, not one-agent-per-VPS. openLEO optimizes for *isolation
  and simplicity* (a VPS per user); agentserver optimizes for *density + a skill
  platform* (Playground edit→publish→compose, marketplace). These are different
  businesses on the same engine: openLEO sells a managed personal agent; agentserver is
  a platform for orgs to build and run many agents/skills.

## So what (implications)

- **Not a clash today.** openLEO = consumer/prosumer managed assistant; agentserver =
  B2B platform. Same engine, different buyer.
- **Their strengths to study:** channel breadth (Discord/Slack/email), the productized
  automation templates (briefs, inbox triage, weekly reports — these are essentially
  pre-built skills + schedules), and the 3-minute onboarding.
- **Our moat is the authoring loop + multi-tenancy.** openLEO users "teach workflows"
  at config level; agentserver lets an org author, test, publish, and share real
  plugin-tier skills across tenants. That is the harder thing to copy.
- **Isolation framing:** openLEO markets "no shared infrastructure" as trust. For B2B
  tenants with MASTERDATA/PII concerns, our multi-tenant model must answer the same
  question (namespace isolation + credentialproxy) clearly — or offer a dedicated/
  single-tenant deployment tier as an option.

## Open questions

- Does openLEO let users author *new tools* (code), or only connect pre-built ones?
  (Their "skills connect to APIs" reads config-level — likely no `.mjs` authoring.)
- Pricing per-instance (€13–38) vs our per-workspace economics — different unit.
- Would a "dedicated single-tenant" deployment tier for agentserver neutralize
  openLEO's isolation pitch for security-sensitive B2B buyers?

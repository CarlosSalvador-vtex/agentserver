# OpenClaw packaging — prior art (AWS AgentCore sample) vs agentserver

> How another project packages OpenClaw, and how it contrasts with our approach.
> Reference: `aws-samples/sample-host-openclaw-on-amazon-bedrock-agentcore`
> (github.com/aws-samples/sample-host-openclaw-on-amazon-bedrock-agentcore).
> Companion to `docs/skills-system.md` and `docs/skill-anatomy-and-tiers.md`.

## TL;DR

Both projects run **OpenClaw unmodified** — neither forks it. The customization is
an **adapter/wrapper layer**, not a patched OpenClaw. This validates our pattern:
don't fork OpenClaw, wrap it. The difference is the surrounding stack and *when*
skills are attached.

## How the AWS AgentCore sample packages OpenClaw

- **Dockerfile** in `bridge/` — base `node:22-slim` (ARM64); `entrypoint.sh` patches
  Node IPv4 DNS, then starts a "contract server" on port 8080.
- **Skills baked at build time:** pre-installs 5 ClawHub community skills (jina-reader,
  deep-research-pro, telegram-compose, transcript, task-decomposer) + custom skills in
  `bridge/skills/` (s3-user-files, eventbridge-cron, clawhub-manage, api-keys), loaded
  via OpenClaw's `extraDirs` config.
- **Adapter layer (the actual customization):**
  - `agentcore-proxy.js` — translates OpenAI-protocol calls into **Bedrock
    ConverseStream** invocations (so OpenClaw's OpenAI client talks to Bedrock);
    injects multimodal image support + per-user identity.
  - `agentcore-contract.js` — implements Amazon Bedrock **AgentCore** invocation
    protocol; lazy init: scoped STS credentials, start proxy + OpenClaw gateway
    (ports 18790/18789), restore workspace from S3, credential-refresh timers.
- **AWS services abstracted:** Bedrock (ConverseStream), AgentCore Runtime (per-user
  microVM), ECR, IAM (STS session-scoped per user), S3 (workspace persistence + image
  uploads), DynamoDB (identity table, token tracking), Secrets Manager, EventBridge
  Scheduler (cron), CloudWatch, VPC endpoints, optional Bedrock Guardrails.
- **Build/deploy:** `docker build --platform linux/arm64 -t openclaw-bridge:vN bridge/`;
  deploy via CDK 3-phase `./scripts/deploy.sh` (+ AgentCore Starter Toolkit for the
  Runtime lifecycle). `.bedrock_agentcore.yaml` auto-generated from CDK outputs.

## Contrast with agentserver

| Aspect | AWS AgentCore sample | agentserver (this repo) |
|--------|----------------------|-------------------------|
| OpenClaw | as-is, no fork | as-is, no fork (same) |
| Skills attached | **baked into the image** at build time (`extraDirs`) | **mounted at runtime** via ConfigMap / composition (`draft:`/`git:`) |
| Change a skill | rebuild + redeploy the image | edit in Playground → publish, no image rebuild |
| Agent runtime | Bedrock AgentCore Runtime (managed, per-user microVM, serverless) | EKS + agent-sandbox CRD + per-workspace namespaces (self-managed) |
| LLM → Bedrock | `agentcore-proxy.js`: OpenAI → ConverseStream | llmproxy (LiteLLM) + `ANTHROPIC_BASE_URL` injection |
| Identity / isolation | STS session-scoped IAM per microVM | credentialproxy + proxy tokens + namespace per workspace |
| Persistence | S3 (workspace) + DynamoDB | PVC (session volumes) + Postgres |
| Multi-tenant model | per-user microVM (AgentCore) | per-workspace namespace (subdomain-bound) |

## Why it matters for us

- **No-fork pattern confirmed.** Two independent projects wrap OpenClaw behind an
  adapter rather than forking. Our ConfigMap-mount + plugin-sdk-symlink approach is the
  same philosophy (see `docs/skills-system.md`).
- **Runtime-mount is our edge.** The AgentCore sample bakes skills into the image, so
  changing a skill means a rebuild + redeploy. agentserver mounts skills at runtime, so
  the Playground edit → publish loop reflects without rebuilding the image. This is the
  core reason we chose ConfigMap-mount over baking.
- **Managed vs self-hosted trade.** AgentCore abstracts the whole AWS stack (Runtime,
  STS, S3, DynamoDB, Guardrails) — less ops, less control, AWS lock-in. agentserver is
  self-hosted K8s — more control + portability, more ops. Different points on the
  build-vs-buy curve, not better/worse.

## Ideas worth borrowing (not committed)

- **Bedrock Guardrails** (content filtering, PII redaction, prompt-attack detection) —
  relevant to the LGPD/MASTERDATA posture; could sit in llmproxy.
- **Session-scoped credentials** per sandbox (STS-style least privilege) — tighter than
  a shared proxy token.
- **ClawHub community skills** as importable templates into the Playground catalog.

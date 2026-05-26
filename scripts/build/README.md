# Build Scripts

Helper scripts to build and push agentserver images to ECR.

## Prerequisites

- Docker Desktop (with multi-platform / BuildKit support)
- AWS CLI v2, configured with credentials that have ECR push access
- Target registry: `344729309528.dkr.ecr.us-east-1.amazonaws.com`

All images are built for `linux/amd64` (EKS nodes are AMD64; developer machines may be Apple Silicon).

---

## 1. ECR Login

Run once per session (token is valid for 12 hours):

```bash
bash scripts/build/login.sh
```

---

## 2. Build a Single Image

```bash
bash scripts/build/build-one.sh <image-key> [tag]
```

`tag` defaults to `dev`.

Valid image keys and their Dockerfiles:

| image-key            | Dockerfile                    | ECR image name                      |
|----------------------|-------------------------------|-------------------------------------|
| agentserver          | Dockerfile                    | agentserver                         |
| imbridge             | Dockerfile.imbridge           | agentserver-imbridge                |
| llmproxy             | Dockerfile.llmproxy           | agentserver-llmproxy                |
| sandboxproxy         | Dockerfile.sandboxproxy       | agentserver-sandboxproxy            |
| codex-app-gateway    | Dockerfile.codex-app-gateway  | agentserver-codex-app-gateway       |
| codex-exec-gateway   | Dockerfile.codex-exec-gateway | agentserver-codex-exec-gateway      |
| credentialproxy      | Dockerfile.credentialproxy    | agentserver-credentialproxy         |
| claudecode           | Dockerfile.claudecode         | agentserver-claudecode              |
| jupyter              | Dockerfile.jupyter            | agentserver-jupyter                 |
| nanoclaw             | Dockerfile.nanoclaw           | agentserver-nanoclaw                |
| openclaw             | Dockerfile.openclaw           | agentserver-openclaw                |
| opencode             | Dockerfile.opencode           | agentserver-opencode                |

Examples:

```bash
# Build and push agentserver with tag dev
bash scripts/build/build-one.sh agentserver

# Build and push openclaw with an explicit tag
bash scripts/build/build-one.sh openclaw routing-v3
```

After push, the script runs `docker manifest inspect` to confirm the image is accessible in ECR.

---

## 3. Build All Images

```bash
bash scripts/build/build-all.sh [tag]
```

Builds and pushes every image in sequence. A single failure does not abort the run — all images are attempted and a pass/fail summary is printed at the end. The script exits non-zero if any image failed.

---

## Tag Conventions

| Tag pattern      | Meaning                                           |
|------------------|---------------------------------------------------|
| `dev`            | Default local/developer build (not for prod)      |
| `routing-vN`     | Routing feature iteration (e.g. `routing-v1`)     |
| `playground-vN`  | Playground / experimental iteration               |
| `sha-<short>`    | Git-SHA-pinned build for traceability             |
| `latest`         | Latest stable build on main branch                |

Prefer explicit versioned tags (`routing-v2`, `playground-v1`) over `latest` for any non-trivial deployment.

---

## Quick Reference

```bash
# First time in a session
bash scripts/build/login.sh

# Build only what changed
bash scripts/build/build-one.sh openclaw routing-v2

# Rebuild everything with a shared tag
bash scripts/build/build-all.sh playground-v1
```

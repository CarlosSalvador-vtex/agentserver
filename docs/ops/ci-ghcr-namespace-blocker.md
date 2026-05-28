# CI Blocker — GHCR image namespace points at the upstream org

> **Status:** CODE REPOINTED (Option A) — namespace now `ghcr.io/carlossalvador-vtex/*`
> in `build.yml` + `values.yaml`. **Deploy still blocked on OWNER ACTIONS** (PAT
> `write:packages` + package visibility/pull-secret) — see "Owner action still required".
> **First observed:** 2026-05-28, after merging PRs #107/#108/#109 to `main`.
> **Repointed:** 2026-05-28 (this fork's namespace).

## Symptom

`Build and Publish` workflow on `main` is RED. The `build-*` jobs fail at the
`docker push` step:

```
#21 ERROR: failed to push ghcr.io/agentserver/llmproxy:main:
    denied: permission_denied: write_package
ERROR: failed to build: failed to solve:
    failed to push ghcr.io/agentserver/<img>:main: denied: permission_denied: write_package
```

`docker login` **succeeds** (the `GHCR_TOKEN` secret is present and valid). Only
the **push** is denied. `Deploy to dev` is skipped because the builds it depends
on never produce an image. The dev cluster keeps running the previously-deployed
image, so newly merged code (e.g. the publish-without-git feature, PR #107) never
reaches dev.

## Root cause

This repository is a **fork**:

```
CarlosSalvador-vtex/agentserver   →  fork of  agentserver/agentserver
```

`.github/workflows/build.yml` hardcodes the image namespace to the **upstream
organization** `agentserver`:

```yaml
env:
  REGISTRY: ghcr.io
# ...
images: ${{ env.REGISTRY }}/agentserver/agentserver      # and llmproxy, imbridge,
                                                         # sandboxproxy, credentialproxy
helm push agentserver-*.tgz oci://${{ env.REGISTRY }}/agentserver/charts
```

`agentserver` is a separate GitHub **Organization** (verified: `gh api orgs/agentserver`
→ `type=Organization`, public member `imryao`). The fork owner
`CarlosSalvador-vtex` is **not a member** of that org
(`gh api user/memberships/orgs/agentserver` → 404), so no PAT owned by
`CarlosSalvador-vtex` can write packages under `ghcr.io/agentserver/*`.

`docker login` only proves the token is valid; the registry authorizes the
**push** against the target namespace's package permissions, which the fork lacks.
→ A scope change on the PAT alone does **not** fix this. The namespace is wrong.

## Affected lines

`.github/workflows/build.yml`:

| Line (approx) | Image |
|---|---|
| `images: …/agentserver/agentserver` | main server (Go backend + embedded React UI) |
| `images: …/agentserver/llmproxy` | LLM proxy |
| `images: …/agentserver/imbridge` | IM bridge |
| `images: …/agentserver/credentialproxy` | credential proxy |
| `images: …/agentserver/sandboxproxy` | sandbox proxy |
| `helm push … oci://…/agentserver/charts` | Helm chart OCI artifact |

`deploy/helm/agentserver/values.yaml` mirrors the same namespace in 5 `repository:`
fields and must be repointed in lockstep, or the cluster will pull from the
upstream namespace while CI pushes to the fork namespace.

## Fix options

### Option A — Repoint to the fork's own GHCR namespace (recommended)

Use the repo owner as the namespace so any fork works without edits:

```yaml
env:
  REGISTRY: ghcr.io
  IMAGE_NS: ${{ github.repository_owner }}   # → CarlosSalvador-vtex; GHCR lowercases to carlossalvador-vtex
# ...
images: ${{ env.REGISTRY }}/${{ env.IMAGE_NS }}/agentserver
helm push agentserver-*.tgz oci://${{ env.REGISTRY }}/${{ env.IMAGE_NS }}/charts
```

Then update `deploy/helm/agentserver/values.yaml`:

```yaml
repository: ghcr.io/carlossalvador-vtex/agentserver   # llmproxy, imbridge, sandboxproxy, credentialproxy
```

**Owner action still required after the repoint PR:**

1. `GHCR_TOKEN` = a **classic PAT** with `write:packages` + `read:packages` + `repo`,
   owned by `CarlosSalvador-vtex` (write to the fork's own namespace works by default).
2. Image pull on EKS — new packages default to **private**. Choose one:
   - **Make packages public** (GHCR → each package → Package settings → Change
     visibility → Public). Cluster pulls with no secret. Simplest for dev.
   - **Keep private + imagePullSecret** — create a K8s `docker-registry` secret with
     a `read:packages` PAT and reference it in `imagePullSecrets`. More secure.

### Option B — Docker Hub (private)

Possible but worse for this case: Docker Hub free allows only **1 private repo**
(6 images needed → paid plan), enforces tighter pull rate limits, and private always
requires a pull secret. Only sensible if a paid Docker Hub plan already exists.

### Option C — Gain write access to the upstream `agentserver` org

Only viable if the upstream maintainer (`imryao`) adds `CarlosSalvador-vtex` as a
member with `write:packages` on those packages. Out of this fork's control; not
recommended.

## Recommendation

**Option A with public packages** for the dev environment — unlimited free private
packages on GHCR, the PAT is already in play, no Docker Hub account, and public
packages drop the pull-secret requirement. Flip to private + pull-secret later if
the images must not be world-readable (no secrets are baked into the images, so the
exposure is compiled code only).

## Verification after fix

```bash
# 1. CI: build-* jobs green, images pushed to the fork namespace
gh run list --branch main --limit 1
# 2. Image exists in your namespace
#    ghcr.io/carlossalvador-vtex/agentserver:main
# 3. Dev deploy rolls the new image; publish-without-git UI appears
#    (the "Publish" button replaces "Promote → PR" in the playground editor)
```

## Related

- PR #107 — publish-without-git feature (the code waiting to reach dev)
- `.github/workflows/build.yml` — `# FIXME(ci-ghcr-namespace-blocker)` markers
- `deploy/helm/agentserver/values.yaml` — image `repository:` fields to repoint

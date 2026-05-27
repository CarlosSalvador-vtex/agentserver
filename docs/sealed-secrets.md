# Sealed Secrets — DB credential management

## Context

Before 2026-05-27, agentserver DB connection strings were stored as plain k8s Secrets
created manually (or synced from AWS Secrets Manager via ESO). Both approaches left
credentials in AWS with associated IAM roles and SM objects.

Sealed Secrets (Bitnami) replaced this: credentials live in the git repo as
asymmetrically-encrypted blobs. Zero AWS objects required.

## How it works

```
kubeseal (local)                cluster
───────────────                 ──────────────────────────────────
plaintext secret                sealed-secrets-controller
      │                               │
      ▼ encrypt with cluster           │
SealedSecret YAML ──── git ──── apply ▶ controller decrypts
(safe to commit)                       ▼
                                  k8s Secret (in-memory only)
```

- Encryption uses the cluster's RSA public key (fetched via `kubeseal --fetch-cert`)
- Decryption happens only inside the cluster — the private key never leaves `kube-system`
- SealedSecrets are **namespace + name bound**: a YAML sealed for `agentserver/agentserver-db-secret`
  cannot be decrypted in any other namespace or under a different name

## Secrets managed

| SealedSecret | k8s Secret key | Source |
|---|---|---|
| `agentserver-db-secret` | `database-url` | agentserver main DB |
| `agentserver-hydra-db-secret` | `dsn` | Hydra OAuth DB |
| `agentserver-llmproxy-db-secret` | `database-url` | llmproxy DB |

Source files: `deploy/helm/agentserver/sealed-secrets/*.yaml`
Helm templates: `deploy/helm/agentserver/templates/sealed-secret-*.yaml`
Enabled via: `sealedSecrets.enabled: true` in values

## Controller

Installed in `kube-system` via Helm:

```bash
helm upgrade --install sealed-secrets sealed-secrets/sealed-secrets \
  -n kube-system \
  -f /tmp/sealed-secrets-values.yaml   # includes internal-workers toleration
```

Requires `internal-workers: true / NoSchedule` toleration on this cluster.

## Re-encrypting (new cluster or key rotation)

The encrypted blobs are tied to the controller's RSA key pair. After key rotation or
cluster migration, re-encrypt all files:

```bash
# 1. Fetch new cluster cert
kubeseal --controller-name=sealed-secrets-controller \
  --controller-namespace=kube-system \
  --fetch-cert > new-cluster.pem

# 2. For each secret, unseal + reseal
# (requires access to the old cluster to read the plaintext)
kubectl get secret agentserver-db-secret -n agentserver -o yaml | \
  kubeseal --cert new-cluster.pem --format yaml \
  > deploy/helm/agentserver/sealed-secrets/agentserver-db-secret.yaml

# Repeat for hydra and llmproxy, then commit + helm upgrade
```

## Adding or rotating a credential

```bash
# 1. Fetch current cert (or reuse saved cert)
kubeseal --controller-name=sealed-secrets-controller \
  --controller-namespace=kube-system \
  --fetch-cert > /tmp/sealed-secrets.pem

# 2. Generate new SealedSecret
kubectl create secret generic agentserver-db-secret \
  --namespace agentserver \
  --from-literal=database-url="postgres://user:newpass@host/db" \
  --dry-run=client -o yaml | \
kubeseal --cert /tmp/sealed-secrets.pem --format yaml \
  > deploy/helm/agentserver/sealed-secrets/agentserver-db-secret.yaml

# 3. Commit + helm upgrade
git add deploy/helm/agentserver/sealed-secrets/agentserver-db-secret.yaml
git commit -m "chore: rotate agentserver DB credential"
helm upgrade agentserver ./deploy/helm/agentserver -n agentserver -f values-dev-eks.yaml
```

## What was removed

- AWS Secrets Manager: `prd/rds/k8s-metadata-instance/agentserver*` (3 secrets, force-deleted)
- IAM role: `agentserver-external-secrets-role` + inline policy `AgentserverSecretsManagerRead`
- ESO ExternalSecrets + SecretStore + ServiceAccount from `agentserver` namespace
- `values-dev-eks.yaml`: `externalSecrets.enabled` → false

ESO itself remains installed cluster-wide (`external-secrets` namespace) for other workloads.

## Caveats

- **Cluster loss = key loss**: if the cluster is destroyed without backing up the Sealed Secrets
  controller key pair, the SealedSecret YAMLs cannot be decrypted. Back up the key:
  ```bash
  kubectl get secret -n kube-system -l sealedsecrets.bitnami.com/sealed-secrets-key \
    -o yaml > sealed-secrets-master-key-backup.yaml
  # Store this file securely (NOT in git)
  ```
- Key rotation is manual — the controller does not auto-rotate.
- SealedSecrets in this repo are bound to `dev-ti-eks-analytics-platform`. Staging/prod
  clusters need their own SealedSecret files generated with their respective controller certs.

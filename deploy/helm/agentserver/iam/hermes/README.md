# IAM — Hermes Sandbox Bedrock IRSA

Role: `dev-ti-eks-analytics-platform-hermes-bedrock-role`
Cluster: `dev-ti-eks-analytics-platform` (us-east-1)
Bound to: `system:serviceaccount:agent-ws-*:hermes` (wildcard — every workspace namespace)

Hermes sandbox pods assume this role via IRSA to call Bedrock directly (provider=bedrock in hermes config.yaml). No LiteLLM, no GLM key needed when the primary provider is Bedrock; GLM is configured as fallback only.

## Create

```bash
aws iam create-role \
  --role-name dev-ti-eks-analytics-platform-hermes-bedrock-role \
  --assume-role-policy-document file://trust-policy.json \
  --region us-east-1

aws iam put-role-policy \
  --role-name dev-ti-eks-analytics-platform-hermes-bedrock-role \
  --policy-name bedrock-invoke-hermes \
  --policy-document file://policy.json
```

## Permissions

| Model | ARN |
|---|---|
| Inference Profile (Sonnet 4.6 cross-region) | `arn:aws:bedrock:us-east-1:344729309528:application-inference-profile/62r7btpf0s40` |
| claude-sonnet-4-6 (us-east-1/2, us-west-2) | foundation-model ARNs in all 3 regions |
| claude-3-sonnet (us-east-1) | foundation-model ARN |
| claude-3-haiku (us-east-1) | foundation-model ARN |

`Converse` and `ConverseStream` actions are included because hermes uses Bedrock Converse API for chat-format calls.

## Trust policy

Uses `StringLike` on the OIDC `sub` claim with wildcard `system:serviceaccount:agent-ws-*:hermes` so any workspace namespace can host a hermes ServiceAccount that assumes this role — without per-workspace IAM changes.

ServiceAccount is auto-created by agentserver in each workspace namespace on the first hermes sandbox create (`hermes` name, annotated with this role ARN).

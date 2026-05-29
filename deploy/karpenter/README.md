# Karpenter manifests

Karpenter `NodePool` / `EC2NodeClass` resources for the EKS clusters that run
agentserver's OpenClaw sandboxes.

## `dev-default-nodepool.yaml`

The `default` pool for **dev-ti-eks-analytics-platform**, mirroring the prod
`default` pool with dev-specific AWS resources (node role instance profile, node
security group, private subnets) and a reduced capacity ceiling.

It hosts OpenClaw sandbox pods: the pool is untainted, sandbox pods carry
tolerations but no `nodeSelector`, so Karpenter provisions capacity here when the
`internal-workers` managed node group is full.

### Apply

```bash
kubectl --context arn:aws:eks:us-east-1:344729309528:cluster/dev-ti-eks-analytics-platform \
  apply -f deploy/karpenter/dev-default-nodepool.yaml
```

### Verify

```bash
kubectl --context <dev> get nodepool default ec2nodeclass default
# both should report READY=True
```

Creating the pool launches no EC2 until a matching pending pod appears; Karpenter
provisions on demand and consolidates when nodes go empty/underutilized.

### dev-specific values (vs prod)

| Field | dev | prod |
|-------|-----|------|
| instanceProfile | `karpenter-instance-profile-e7e396e` | `karpenter-instance-profile-2c1a9b3` |
| node security group | `sg-083605992e64fa3b5` | `sg-01e617f7911f4b427` |
| subnets | `subnet-05731d48fa430d313` / `-0b5819dd9b85fab28` / `-0a53f8474451c3b70` (shared VPC) | same 3 |
| limits | 200 vCPU / 200Gi | 1000 vCPU / 1000Gi |
| amiSelectorTerms | `al2023@latest` | dated alias |

Everything else (instance families, sizes, capacity types, disruption policy)
matches prod. The prod pool is managed by ArgoCD (`app: node-pools`); this dev
manifest is applied manually until dev joins the same GitOps flow.

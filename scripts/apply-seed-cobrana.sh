#!/usr/bin/env bash
# Create ConfigMaps and apply the seed Job in the dev cluster.
# Run AFTER deploy-dev.sh — only needed once (or after skill content changes).
#
# Usage:
#   ./scripts/apply-seed-cobrana.sh [--context <kube-context>]

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

DEV_CONTEXT="${DEV_CONTEXT:-arn:aws:eks:us-east-1:344729309528:cluster/dev-ti-eks-analytics-platform}"
NAMESPACE="agentserver"
SKILL_DIR="deploy/helm/agentserver/skills/cobranca"

# Parse optional --context flag
while [[ $# -gt 0 ]]; do
  case "$1" in
    --context) DEV_CONTEXT="$2"; shift 2 ;;
    *) echo "Unknown arg: $1"; exit 1 ;;
  esac
done

KUBECTL="kubectl --context $DEV_CONTEXT -n $NAMESPACE"

echo "==> Creating ConfigMap: seed-cobrana-script"
$KUBECTL create configmap seed-cobrana-script \
  --from-file=seed-cobrana-templates.py=scripts/seed-cobrana-templates.py \
  --dry-run=client -o yaml | $KUBECTL apply -f -

echo "==> Creating ConfigMap: seed-cobrana-skill-files"
# Note: references/leads.json is embedded inline in the Python script
# (ConfigMap keys cannot contain '/' so it cannot be mounted as a file).
$KUBECTL create configmap seed-cobrana-skill-files \
  --from-file="index.mjs=$SKILL_DIR/index.mjs" \
  --from-file="prompt.md=$SKILL_DIR/prompt.md" \
  --from-file="package.json=$SKILL_DIR/package.json" \
  --from-file="openclaw.plugin.json=$SKILL_DIR/openclaw.plugin.json" \
  --dry-run=client -o yaml | $KUBECTL apply -f -

echo "==> Deleting old job (if any)"
$KUBECTL delete job seed-cobrana-templates --ignore-not-found

echo "==> Applying seed Job"
$KUBECTL apply -f scripts/seed-cobrana-job.yaml

echo "==> Waiting for Job to complete..."
$KUBECTL wait --for=condition=complete job/seed-cobrana-templates --timeout=120s

echo "==> Logs:"
$KUBECTL logs job/seed-cobrana-templates

echo ""
echo "Done. Templates visible at /marketplace in the UI."

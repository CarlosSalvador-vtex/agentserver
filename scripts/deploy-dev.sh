#!/usr/bin/env bash
# Build, push (ECR), and deploy agentserver to the dev EKS cluster.
#
# Prerequisites:
#   - aws cli configured with access to account 344729309528
#   - kubectl context set or passed via DEV_CONTEXT env var
#   - docker with buildx support
#
# Usage:
#   ./scripts/deploy-dev.sh [IMAGE_TAG]
#
# Examples:
#   ./scripts/deploy-dev.sh             # tags as playground-v2 (from values-dev-eks.yaml)
#   ./scripts/deploy-dev.sh my-tag      # override tag

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

ECR_REGISTRY="344729309528.dkr.ecr.us-east-1.amazonaws.com"
ECR_REPO="$ECR_REGISTRY/agentserver"
DEV_CONTEXT="${DEV_CONTEXT:-arn:aws:eks:us-east-1:344729309528:cluster/dev-ti-eks-analytics-platform}"
NAMESPACE="agentserver"

# Resolve image tag from values-dev-eks.yaml if not provided
DEFAULT_TAG=$(grep 'tag:' values-dev-eks.yaml | head -1 | awk '{print $2}')
IMAGE_TAG="${1:-$DEFAULT_TAG}"

echo "==> Building $ECR_REPO:$IMAGE_TAG (linux/amd64)"
docker build \
  --platform linux/amd64 \
  -t "$ECR_REPO:$IMAGE_TAG" \
  -f Dockerfile \
  .

echo "==> Logging into ECR"
aws ecr get-login-password --region us-east-1 | \
  docker login --username AWS --password-stdin "$ECR_REGISTRY"

echo "==> Pushing $ECR_REPO:$IMAGE_TAG"
docker push "$ECR_REPO:$IMAGE_TAG"

echo "==> Helm upgrade (namespace: $NAMESPACE, context: $DEV_CONTEXT)"
helm upgrade --install agentserver ./deploy/helm/agentserver \
  --namespace "$NAMESPACE" \
  --create-namespace \
  -f values-dev-eks.yaml \
  --kube-context "$DEV_CONTEXT" \
  --wait \
  --timeout 5m

echo "==> Rollout status"
kubectl rollout status deployment/agentserver \
  -n "$NAMESPACE" \
  --context "$DEV_CONTEXT"

echo ""
echo "Done. Deployed $ECR_REPO:$IMAGE_TAG to dev cluster."
echo ""
echo "Next: seed marketplace templates (run once after first deploy):"
echo "  kubectl apply -f scripts/seed-cobrana-job.yaml --context $DEV_CONTEXT"
echo "  kubectl logs -n $NAMESPACE job/seed-cobrana-templates -f --context $DEV_CONTEXT"

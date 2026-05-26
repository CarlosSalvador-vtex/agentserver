#!/usr/bin/env bash
# scripts/build/build-one.sh — Build and push a single image to ECR
#
# Usage: build-one.sh <image-key> [tag]
#
# image-key must be one of:
#   agentserver | imbridge | llmproxy | sandboxproxy |
#   credentialproxy | openclaw
#
# tag defaults to: dev
set -euo pipefail

REGION="us-east-1"
REGISTRY="344729309528.dkr.ecr.${REGION}.amazonaws.com"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# ---------- lookup tables (parallel arrays — bash 3.2 compatible) -----------
ALL_KEYS=(
  agentserver
  imbridge
  llmproxy
  sandboxproxy
  credentialproxy
  openclaw
)

ALL_DOCKERFILES=(
  Dockerfile
  Dockerfile.imbridge
  Dockerfile.llmproxy
  Dockerfile.sandboxproxy
  Dockerfile.credentialproxy
  Dockerfile.openclaw
)

ALL_IMAGE_NAMES=(
  agentserver
  agentserver-imbridge
  agentserver-llmproxy
  agentserver-sandboxproxy
  agentserver-credentialproxy
  agentserver-openclaw
)

# ---------- lookup function -------------------------------------------------
lookup_key() {
  local key="$1"
  local i
  for i in "${!ALL_KEYS[@]}"; do
    if [[ "${ALL_KEYS[$i]}" == "$key" ]]; then
      echo "$i"
      return 0
    fi
  done
  return 1
}

# ---------- arg parsing -----------------------------------------------------
if [[ $# -lt 1 ]]; then
  echo "Usage: $(basename "$0") <image-key> [tag]" >&2
  echo "Valid keys: ${ALL_KEYS[*]}" >&2
  exit 1
fi

IMAGE_KEY="$1"
TAG="${2:-dev}"

INDEX=""
if ! INDEX="$(lookup_key "${IMAGE_KEY}")"; then
  echo "Error: unknown image key '${IMAGE_KEY}'" >&2
  echo "Valid keys: ${ALL_KEYS[*]}" >&2
  exit 1
fi

DOCKERFILE="${REPO_ROOT}/${ALL_DOCKERFILES[$INDEX]}"
IMAGE_NAME="${ALL_IMAGE_NAMES[$INDEX]}"
FULL_IMAGE="${REGISTRY}/${IMAGE_NAME}:${TAG}"

if [[ ! -f "${DOCKERFILE}" ]]; then
  echo "Error: Dockerfile not found at ${DOCKERFILE}" >&2
  exit 1
fi

# ---------- build -----------------------------------------------------------
echo "▶ Building ${IMAGE_KEY} → ${FULL_IMAGE}"
echo "  Dockerfile : ${DOCKERFILE}"
echo "  Platform   : linux/amd64"
echo "  Context    : ${REPO_ROOT}"
echo ""

docker build \
  --platform linux/amd64 \
  --file "${DOCKERFILE}" \
  --tag "${FULL_IMAGE}" \
  "${REPO_ROOT}"

echo ""
echo "▶ Build complete: ${FULL_IMAGE}"

# ---------- push ------------------------------------------------------------
echo "▶ Pushing ${FULL_IMAGE} ..."
if docker push "${FULL_IMAGE}"; then
  echo "▶ Push succeeded. Verifying manifest ..."
  if docker manifest inspect "${FULL_IMAGE}" > /dev/null 2>&1; then
    echo "Manifest verified for ${FULL_IMAGE}"
  else
    echo "Warning: manifest inspect failed — image may not be accessible yet." >&2
  fi
else
  echo "Warning: push failed (not logged in to ECR?). Image was built locally as ${FULL_IMAGE}." >&2
  exit 1
fi

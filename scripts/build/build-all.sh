#!/usr/bin/env bash
# scripts/build/build-all.sh — Build and push every image to ECR in sequence
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD_ONE="${SCRIPT_DIR}/build-one.sh"
TAG="${1:-dev}"

IMAGES=(
  agentserver
  imbridge
  llmproxy
  sandboxproxy
  credentialproxy
  openclaw
)

FAILED=()
SUCCEEDED=()

echo "============================================================"
echo " build-all.sh — tag: ${TAG}"
echo " images: ${#IMAGES[@]}"
echo "============================================================"
echo ""

for key in "${IMAGES[@]}"; do
  echo "------------------------------------------------------------"
  echo "▶ Starting: ${key}  [tag=${TAG}]"
  echo "------------------------------------------------------------"
  if bash "${BUILD_ONE}" "${key}" "${TAG}"; then
    SUCCEEDED+=("${key}")
    echo ""
    echo "OK: ${key}"
  else
    FAILED+=("${key}")
    echo ""
    echo "FAILED: ${key}" >&2
  fi
  echo ""
done

echo "============================================================"
echo " build-all.sh complete"
echo " Succeeded (${#SUCCEEDED[@]}): ${SUCCEEDED[*]:-none}"
echo " Failed    (${#FAILED[@]}):    ${FAILED[*]:-none}"
echo "============================================================"

if [[ ${#FAILED[@]} -gt 0 ]]; then
  exit 1
fi

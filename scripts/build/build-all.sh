#!/usr/bin/env bash
# scripts/build/build-all.sh — Build and push every image to ECR in sequence
#
# Usage: build-all.sh [tag]
#   tag defaults to: dev
#
# Individual failures are collected and reported at the end; the script does
# NOT abort on a single image failure so all images are attempted.
set -uo pipefail   # note: no -e so we can collect failures ourselves

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD_ONE="${SCRIPT_DIR}/build-one.sh"
TAG="${1:-dev}"

IMAGES=(
  agentserver
  imbridge
  llmproxy
  sandboxproxy
  codex-app-gateway
  codex-exec-gateway
  credentialproxy
  claudecode
  jupyter
  nanoclaw
  openclaw
  opencode
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

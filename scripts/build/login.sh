#!/usr/bin/env bash
# scripts/build/login.sh — Authenticate Docker to ECR (us-east-1)
set -euo pipefail

REGION="us-east-1"
REGISTRY="344729309528.dkr.ecr.${REGION}.amazonaws.com"

echo "▶ Logging in to ECR: ${REGISTRY}"
aws ecr get-login-password --region "${REGION}" \
  | docker login --username AWS --password-stdin "${REGISTRY}"

echo "Login succeeded."

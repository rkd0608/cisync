#!/usr/bin/env bash
# Sauron AWS kit — build & push all 5 images to ECR.
# Usage: AWS_REGION=us-east-1 ./push-images.sh [--tag <git-sha>]
#   (default tag: current git short SHA; `latest` also maintained)
# Requires: docker, aws cli v2, repo checkout at the commit you want shipped.
set -euo pipefail

AWS_REGION="${AWS_REGION:?set AWS_REGION (must match terraform var.aws_region)}"
TAG="$(git rev-parse --short HEAD)"
[[ "${1:-}" == "--tag" ]] && TAG="${2:?--tag needs a value}"

ACCOUNT_ID="$(aws sts get-caller-identity --query Account --output text)"
REGISTRY="${ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com"
REPO_PREFIX="sauron-${ENVIRONMENT:-prod}"

# service -> build context. Existing Dockerfiles are REUSED verbatim:
# Go services embed migrations/ (auto-migrate at boot); web needs NO build
# args — the /api/sauron/* proxy is runtime-configured via env (SPEC §3).
readonly IMAGES=(
  "ingest:services/ingest"
  "control-plane:services/control-plane"
  "runner-fleet:services/runner-fleet"
  "github-connector:services/github-connector"
  "web:apps/web"
)

aws ecr get-login-password --region "$AWS_REGION" \
  | docker login --username AWS --password-stdin "$REGISTRY"

for entry in "${IMAGES[@]}"; do
  svc="${entry%%:*}"
  ctx="${entry#*:}"
  repo="$REGISTRY/${REPO_PREFIX}-${svc}"
  echo "==> building ${svc} (${ctx}) -> ${repo}:${TAG}"
  docker build -t "${repo}:${TAG}" -t "${repo}:latest" "$ctx"
  docker push "${repo}:${TAG}"
  docker push "${repo}:latest"
done

echo "Done. Deploy with: terraform apply -var image_tag=${TAG} ..."

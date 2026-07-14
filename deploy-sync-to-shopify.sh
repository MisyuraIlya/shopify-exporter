#!/usr/bin/env bash
set -euo pipefail

REGION="europe-west8-docker.pkg.dev"
PROJECT_ID="compute-dev-ilia"
REPO="emanuel-repo"
SERVICE="shopify-exporter-sync"
ZONE="europe-west8-b"
INSTANCE="instance-emanuel"
REMOTE_ENV="/home/spetsar/shopify-exporter.env"
REMOTE_LOG_DIR="/home/spetsar/shopify-exporter-logs"
CONTAINER_LOG_DIR="/var/log/shopify-exporter"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOCAL_TAG="${SERVICE}:latest"
REMOTE_IMAGE="${REGION}/${PROJECT_ID}/${REPO}/${SERVICE}:latest"

# The VM service account CANNOT pull from Artifact Registry (scope devstorage.read_only,
# no artifactregistry.reader) — VM-side `docker pull` fails with `denied: downloadArtifacts`.
# So we ship the image straight to the VM via `docker save | scp | docker load`. The 6h cron
# (/home/spetsar/run-shopify-exporter.sh) runs this locally-loaded ${LOCAL_TAG}, so the tag
# must be preserved on the VM (do NOT prune it). See FIXES.md 2026-07-14.
IMAGE_TARBALL="$(mktemp -t shopify-exporter.XXXXXX.tar.gz)"
trap 'rm -f "${IMAGE_TARBALL}"' EXIT

echo "🔨 Building local image ${LOCAL_TAG} (linux/amd64, clean single-arch for save/load)"
docker build --platform linux/amd64 --provenance=false -t "${LOCAL_TAG}" "${SCRIPT_DIR}"

# Push to Artifact Registry too, as the canonical record (uses your local user token, which
# — unlike the VM SA — can write). Best-effort: the VM does not consume this copy.
echo "🔖 Tagging ${LOCAL_TAG} → ${REMOTE_IMAGE}"
docker tag "${LOCAL_TAG}" "${REMOTE_IMAGE}"
echo "🔐 Logging in to Artifact Registry"
gcloud auth print-access-token \
  | docker login -u oauth2accesstoken --password-stdin "https://${REGION}"
echo "🚀 Pushing ${REMOTE_IMAGE} (record copy; non-fatal if it fails)"
docker push "${REMOTE_IMAGE}" || echo "⚠ push failed — continuing (VM uses the save/load copy)"

echo "💾 Saving ${LOCAL_TAG} → ${IMAGE_TARBALL}"
docker save "${LOCAL_TAG}" | gzip > "${IMAGE_TARBALL}"

echo "📤 Copying image to ${INSTANCE}:/tmp/shopify-exporter.tar.gz"
gcloud compute scp "${IMAGE_TARBALL}" "${INSTANCE}:/tmp/shopify-exporter.tar.gz" \
  --zone="${ZONE}" \
  --project="${PROJECT_ID}" \
  --tunnel-through-iap

echo "🔑 Deploying ${LOCAL_TAG} to ${INSTANCE}…"
gcloud compute ssh "${INSTANCE}" \
  --zone="${ZONE}" \
  --project="${PROJECT_ID}" \
  --tunnel-through-iap \
  --command "
    set -e

    echo '— Loading image (no registry pull; VM SA cannot pull)'
    sudo docker load < /tmp/shopify-exporter.tar.gz
    rm -f /tmp/shopify-exporter.tar.gz

    echo '— Removing old container (if exists)'
    sudo docker rm -f ${SERVICE} >/dev/null 2>&1 || true

    echo '— Ensuring log directory exists'
    sudo mkdir -p ${REMOTE_LOG_DIR}

    echo '— Starting new container (immediate run)'
    sudo docker run -d --rm \
      --name ${SERVICE} \
      --env-file ${REMOTE_ENV} \
      --env LOG_FILE_DIR=${CONTAINER_LOG_DIR} \
      --volume ${REMOTE_LOG_DIR}:${CONTAINER_LOG_DIR} \
      ${LOCAL_TAG}

    echo '— Pruning dangling images only (keep ${LOCAL_TAG} for the 6h cron)'
    sudo docker image prune -f

    echo '✅ ${SERVICE} is now running ${LOCAL_TAG}; 6h cron will reuse the loaded image'
  "

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

echo "🔨 Building local image ${LOCAL_TAG}"
docker build -t "${LOCAL_TAG}" "${SCRIPT_DIR}"

echo "🔖 Tagging ${LOCAL_TAG} → ${REMOTE_IMAGE}"
docker tag "${LOCAL_TAG}" "${REMOTE_IMAGE}"

echo "🔐 Logging in to Artifact Registry"
gcloud auth print-access-token \
  | docker login -u oauth2accesstoken --password-stdin "https://${REGION}"

echo "🚀 Pushing ${REMOTE_IMAGE}"
docker push "${REMOTE_IMAGE}"

echo "🔑 Deploying ${SERVICE}:latest to ${INSTANCE}…"
gcloud compute ssh "${INSTANCE}" \
  --zone="${ZONE}" \
  --project="${PROJECT_ID}" \
  --tunnel-through-iap \
  --command "
    set -e

    echo '🔐 On VM: configuring Docker credentials as root'
    sudo sh -c 'gcloud auth print-access-token \
      | docker login -u oauth2accesstoken --password-stdin https://${REGION}'

    echo '— Pulling latest image'
    sudo docker pull ${REMOTE_IMAGE}

    echo '— Removing old container (if exists)'
    sudo docker rm -f ${SERVICE} >/dev/null 2>&1 || true

    echo '— Ensuring log directory exists'
    sudo mkdir -p ${REMOTE_LOG_DIR}

    echo '— Starting new container'
    sudo docker run -d --rm \
      --name ${SERVICE} \
      --env-file ${REMOTE_ENV} \
      --env LOG_FILE_DIR=${CONTAINER_LOG_DIR} \
      --volume ${REMOTE_LOG_DIR}:${CONTAINER_LOG_DIR} \
      ${REMOTE_IMAGE}

    echo '— Pruning unused images'
    sudo docker image prune -a -f

    echo '✅ ${SERVICE} is now running :latest'
  "

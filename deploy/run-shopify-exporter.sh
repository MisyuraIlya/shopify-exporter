#!/usr/bin/env bash
# Runs the Shopify exporter. Called by root cron under flock so runs never overlap.
# Installed to /home/spetsar/run-shopify-exporter.sh by deploy-sync-to-shopify.sh —
# edit it HERE, not on the VM, or the next deploy overwrites your change.
#
# Usage: run-shopify-exporter.sh [STEPS] [MODE]
#   STEPS : value for SYNC_ONLY_STEPS (e.g. "syncStocks"); empty = full sync (all steps)
#   MODE  : short label used for the container name + log line (default "full")
#
# Scheduling (root crontab):
#   every 6h  -> STEPS="syncStocks"  MODE=stock  (fast, ~10-15 min; keeps stock fresh)
#   daily 03h -> STEPS=""            MODE=full   (products/categories/prices/etc.)
#
# Self-heal: the image is loaded locally because the VM service account cannot pull
# from Artifact Registry. Any backend/frontend deploy that runs
# `docker image prune -a -f` deletes it — nothing references it between cron ticks —
# and every run then failed with "pull access denied" until someone noticed. That cost
# 7 days of stale stock in July 2026. So if the image is missing we reload it from the
# tarball the deploy leaves behind. See FIXES.md 2026-08-04.
set -euo pipefail

STEPS="${1:-}"
MODE="${2:-full}"

IMAGE="shopify-exporter-sync:latest"
IMAGE_TARBALL="/home/spetsar/shopify-exporter-sync.tar.gz"
ENV_FILE="/home/spetsar/shopify-exporter.env"
LOG_DIR="/home/spetsar/shopify-exporter-logs"
CONTAINER_LOG_DIR="/var/log/shopify-exporter"
NAME="shopify-exporter-${MODE}"

echo "[$(date -u +%FT%TZ)] shopify-exporter cron run starting mode=${MODE} steps=${STEPS:-<all>}"

if ! docker image inspect "${IMAGE}" >/dev/null 2>&1; then
  echo "[$(date -u +%FT%TZ)] WARNING image ${IMAGE} missing (likely pruned by a backend/frontend deploy)"
  if [ -f "${IMAGE_TARBALL}" ]; then
    echo "[$(date -u +%FT%TZ)] reloading ${IMAGE} from ${IMAGE_TARBALL}"
    docker load < "${IMAGE_TARBALL}"
  else
    echo "[$(date -u +%FT%TZ)] ERROR no image and no tarball at ${IMAGE_TARBALL} — run deploy-sync-to-shopify.sh" >&2
    exit 1
  fi
fi

# Clear any stale container from a previous crashed run of this mode.
docker rm -f "${NAME}" >/dev/null 2>&1 || true
mkdir -p "${LOG_DIR}"

ENV_ARGS=()
if [ -n "${STEPS}" ]; then
  ENV_ARGS+=(--env "SYNC_ONLY_STEPS=${STEPS}")
fi

# Foreground run: flock holds the lock for the full run so the next tick of the
# same mode cannot start a second overlapping run.
docker run --rm \
  --name "${NAME}" \
  --env-file "${ENV_FILE}" \
  --env LOG_FILE_DIR="${CONTAINER_LOG_DIR}" \
  "${ENV_ARGS[@]}" \
  --volume "${LOG_DIR}:${CONTAINER_LOG_DIR}" \
  "${IMAGE}"

echo "[$(date -u +%FT%TZ)] shopify-exporter cron run finished mode=${MODE}"

#!/usr/bin/env bash
# Runs the Shopify exporter. Called by root cron under flock so runs never overlap.
# Installed to /home/spetsar/run-shopify-exporter.sh by deploy-sync-to-shopify.sh —
# edit it HERE, not on the VM, or the next deploy overwrites your change.
#
# Usage: run-shopify-exporter.sh [STEPS] [MODE]
#   STEPS : value for SYNC_ONLY_STEPS (e.g. "syncStocks"); empty = full sync (all steps)
#   MODE  : short label used for the container name + log line (default "full").
#           MODE=delta additionally runs the stock step in delta mode and silences the
#           "nothing changed" report — see below.
#
# Scheduling (root crontab). Every stock-touching job shares ONE lock file, so a delta
# tick can never overlap the daily full sync and race it on the snapshot:
#
#   */5 * * * * /usr/bin/flock -n /var/lock/shopify-exporter-stock.lock \
#     /home/spetsar/run-shopify-exporter.sh "syncStocks" delta \
#     >> /home/spetsar/shopify-exporter-logs/cron-stock.log 2>&1
#
#   0 3 * * *   /usr/bin/flock -n /var/lock/shopify-exporter-stock.lock \
#     /home/spetsar/run-shopify-exporter.sh "" full \
#     >> /home/spetsar/shopify-exporter-logs/cron-full.log 2>&1
#
# `flock -n` skips the tick rather than queueing it: a stacked queue of stock runs all
# pushing the same numbers helps nobody.
#
#   delta (*/5)  -> only SKUs whose ERP quantity moved since the last successful run.
#                   Usually a handful of SKUs and a few API calls, so the storefront is
#                   at most ~5 minutes behind Hashavshevet instead of ~6 hours.
#   full  (daily) -> everything, including the reconciliation the delta cannot do:
#                   Shopify's on_hand also moves on its own when an order is fulfilled,
#                   and only a full pass notices that the ERP number needs re-pushing.
#                   DO NOT drop this job — delta alone will slowly drift.
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

# The frequent tick pushes only what the ERP changed, and mails only when a step
# actually fails.
#
# ONLY_ON_CHANGE was tried first and was not enough: a live catalogue moves at least one
# SKU on nearly every tick, so it still sent ~288 mails a day and the recipients stopped
# reading them. ONLY_ON_FAILURE keeps the inbox at one scheduled mail a day — from the
# daily full run, which mails unconditionally — while a broken delta tick still lands
# immediately. Liveness for THIS job is the cron log, not the inbox.
if [ "${MODE}" = "delta" ]; then
  ENV_ARGS+=(--env "SYNC_STOCK_MODE=delta")
  ENV_ARGS+=(--env "REPORT_EMAIL_ONLY_ON_FAILURE=true")
fi

# Foreground run: flock holds the lock for the full run so the next tick of the
# same mode cannot start a second overlapping run.
#
# The volume below is load-bearing for delta mode, not just for logs: the snapshot of
# the last pushed quantities defaults to stock-state.json inside LOG_FILE_DIR, so it
# lands on the host and survives this one-shot `docker run`. Without the mount every
# tick would find no snapshot and push the whole catalogue.
docker run --rm \
  --name "${NAME}" \
  --env-file "${ENV_FILE}" \
  --env LOG_FILE_DIR="${CONTAINER_LOG_DIR}" \
  "${ENV_ARGS[@]}" \
  --volume "${LOG_DIR}:${CONTAINER_LOG_DIR}" \
  "${IMAGE}"

echo "[$(date -u +%FT%TZ)] shopify-exporter cron run finished mode=${MODE}"

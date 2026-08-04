# RUNBOOK — shopify-exporter (Emanuel / חשבשבת ↔ Shopify)

How this repo is actually operated and debugged. The top-level `README.md` shows an
*aspirational* structure that does not match the code — use THIS file for real commands.

## What it does
Syncs data from the חשבשבת ("ApiHasav" / API X) ERP to a Shopify store. ERP DB name is
always `EMANUEL` (sent in every request body). Entry points under `cmd/`:

| Binary | Purpose | Needs MySQL? |
|--------|---------|--------------|
| `cmd/sync-to-shopify` | Full push: categories, products, prices, stock, related, order | yes (some steps) |
| `cmd/sync-stock-and-price` | Daily job: **prices + stock only** (`LoadForDailySync`) | **no** |
| `cmd/sync-orders` | Shopify orders → DB → ERP | yes |
| `cmd/send-test-report` | Mails one sample report to check the SMTP config — touches neither ERP nor Shopify | **no** |
| `cmd/wipe-shopify` | Destructive teardown of Shopify catalog — **do not run** unless rebuilding | — |

## Config / secrets
`internal/config/env.go` auto-loads `.env` from the **current working directory** (must run
from repo root). `.env` is gitignored; keys are in `.env.example`. Prod creds in `.env` point
at the **live** Emanuel Shopify store and ERP — any run writes to production.

## Schedule (root crontab on instance-emanuel)
```
0 */6 * * *  flock -n /run/shopify-stock.lock /home/spetsar/run-shopify-exporter.sh syncStocks stock  >> .../cron-stock.log
0 3   * * *  flock -n /run/shopify-full.lock  /home/spetsar/run-shopify-exporter.sh "" full           >> .../cron-full.log
```
Stock every 6h at 00/06/12/18 UTC (~10-15 min), full sync daily at 03:00 UTC (~2h). The
runner is version-controlled at `deploy/run-shopify-exporter.sh` and installed by the
deploy script — **edit it there, not on the VM**, or the next deploy overwrites it.

### Is it actually running?
Cron firing is not the same as the job running: for 7 days in July 2026 it fired on time
and died instantly because a site deploy had pruned the image (FIXES.md 2026-08-04).
1. **Check the report inbox first.** Every run mails a report, failures included, so a
   missing report for a scheduled slot means the job never started.
2. `sudo tail -20 /home/spetsar/shopify-exporter-logs/cron-stock.log`.
3. `sudo docker images | grep shopify-exporter` — the tag must exist locally; the VM
   service account cannot pull it.

## Run a sync manually
```bash
cd /home/ilya/projects/emannuel/shopify-exporter
go build -o sync-stock-and-price ./cmd/sync-stock-and-price   # binary is gitignored
SYNC_ONLY_STEPS=syncStocks ./sync-stock-and-price             # one step
```
On the VM, against the real env file (this is usually what you want):
```bash
sudo docker run --rm --env-file /home/spetsar/shopify-exporter.env \
  --env SYNC_ONLY_STEPS=syncStocks shopify-exporter-sync:latest
```
Output goes to stdout AND to a file under `LOG_FILE_DIR` (per `.env`); `*.log` is gitignored.

### Scoping / debug env vars (`internal/debugsync/trace.go`)
| Var | Effect |
|-----|--------|
| `SYNC_ONLY_STEPS=syncStocks\|syncPrices\|syncCategories…` | run only these steps (comma/;/\| separated) |
| `SYNC_ONLY_SKUS=DRA-1,CAJ-4` | process only these SKUs (price/stock paths) |
| `SYNC_TRACE_SKUS=DRA-1` | verbose per-SKU trace logging |

Example — price-only, traced, for two SKUs:
```bash
SYNC_ONLY_STEPS=syncPrices SYNC_TRACE_SKUS=CAJ-4,CSX-1 go run ./cmd/sync-stock-and-price
```

## Deploy (so a scheduled/container run carries code changes)
`./deploy-sync-to-shopify.sh` builds a Docker image, pushes to GCP Artifact Registry
(`europe-west8`, project `compute-dev-ilia`, repo `emanuel-repo`) and runs it on GCE VM
`instance-emanuel` (zone `europe-west8-b`) via `docker run --rm` with `--env-file`.
A **manual local run applies the fix immediately to prod, but the deployed image stays
old** until you redeploy — redeploy if the change must persist for scheduled runs.

`--no-run` ships the image and the cron runner without starting an immediate sync — use it
when a sync is already in flight.

⚠️ The VM service account **cannot pull from Artifact Registry**, so the image is shipped
`docker save | scp | docker load` and the deploy leaves a tarball at
`/home/spetsar/shopify-exporter-sync.tar.gz` that the cron runner reloads from if the image
goes missing. Never "clean up" that tarball.

## Email report
Every run mails what changed (stock and price before → after per SKU, new products,
failures) to `REPORT_EMAIL_TO`, with the full list attached as CSV. Config lives in the env
file: `REPORT_*` and `SMTP_*`, documented in `.env.example`. Code: `internal/report`
(collect + render + send) and `internal/app/reporting` (wiring, used by both binaries).
Check the settings without running a sync:
```bash
sudo docker run --rm --env-file /home/spetsar/shopify-exporter.env \
  --entrypoint /app/send-test-report shopify-exporter-sync:latest
```
Note `LOG_OUTPUT` is unset in the prod env file, so it defaults to `stdout` and the
`TELEGRAM_*` credentials there are **not** in use — set it to `both` if you want Telegram
alerts as well.

## Where the knowledge lives (read before re-debugging a complaint)
- `FIXES.md` — chronological log of every non-obvious fix (newest at top).
- `.claude/STOCK_ISSUE_KNOWN_ROOT_CAUSE.md` — "out of stock shows available" (skip→zero bug).
- `.claude/PRICE_ISSUE_KNOWN_ROOT_CAUSE.md` — USD price drift (Shopify Payments single-currency).

## Debugging principle that keeps paying off
Client tickets here usually propose the **wrong** root cause (warehouse, buffer, "price bug").
Verify against the **live ERP** (`/stocksProducts`, `/prices-latest`) and the **live Shopify
Admin API** before trusting the theory or touching code. The two `*_KNOWN_ROOT_CAUSE.md` docs
both exist because the real cause differed from what was reported.

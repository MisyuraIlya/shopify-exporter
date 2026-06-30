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
| `cmd/wipe-shopify` | Destructive teardown of Shopify catalog — **do not run** unless rebuilding | — |

## Config / secrets
`internal/config/env.go` auto-loads `.env` from the **current working directory** (must run
from repo root). `.env` is gitignored; keys are in `.env.example`. Prod creds in `.env` point
at the **live** Emanuel Shopify store and ERP — any run writes to production.

## Run a sync manually (this is how it's done today — no reliable cron)
```bash
cd /home/ilya/projects/shopify-exporter
go build -o sync-stock-and-price ./cmd/sync-stock-and-price   # binary is gitignored
SYNC_ONLY_STEPS=syncStocks ./sync-stock-and-price             # one step
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

⚠️ **No reliable cron since 2026-05-27** (see `.claude/PRICE_ISSUE_KNOWN_ROOT_CAUSE.md`).
Treat syncs as manual until ops confirms a timer exists.

## Where the knowledge lives (read before re-debugging a complaint)
- `FIXES.md` — chronological log of every non-obvious fix (newest at top).
- `.claude/STOCK_ISSUE_KNOWN_ROOT_CAUSE.md` — "out of stock shows available" (skip→zero bug).
- `.claude/PRICE_ISSUE_KNOWN_ROOT_CAUSE.md` — USD price drift (Shopify Payments single-currency).

## Debugging principle that keeps paying off
Client tickets here usually propose the **wrong** root cause (warehouse, buffer, "price bug").
Verify against the **live ERP** (`/stocksProducts`, `/prices-latest`) and the **live Shopify
Admin API** before trusting the theory or touching code. The two `*_KNOWN_ROOT_CAUSE.md` docs
both exist because the real cause differed from what was reported.

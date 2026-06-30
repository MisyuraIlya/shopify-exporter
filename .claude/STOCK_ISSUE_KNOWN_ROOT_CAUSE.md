# KNOWN ROOT CAUSE — "out of stock in חשבשבת but shows AVAILABLE on the website"

**Read this FIRST on any Emanuel stock / availability complaint** (item is 0 in the ERP
but the Shopify storefront still shows it in stock; the B2B "ישיר" site shows it correctly).
The cause is almost never the warehouse selection or the `-3` buffer the client suspects —
it was a **skip-instead-of-zero** bug in the stock mapping. Don't re-debug from scratch.

## Triage (read-only, ~1 min)
Use creds from `.env` (`set -a; . ./.env; set +a`).

1. **ERP truth** — what חשבשבת actually reports for the SKU:
   ```bash
   curl -s -X POST "$API_BASE_URL/stocksProducts" -H "Content-Type: application/json" \
     -H "Authorization: $API_TOKEN" -d '{"dbName":"EMANUEL"}' \
     | python3 -c 'import sys,json;[print(v["ITEMKEY"],v["ITEMWARHBAL"]) for v in json.load(sys.stdin)["items"] if v["ITEMKEY"].strip() in {"HAO-1","CUP-2","CUJ-10"}]'
   ```
   `ITEMWARHBAL` = warehouse balance. ≤ 0 (or ≤ 3 with the reserve) means the ERP says "out of stock".
2. **Shopify truth** — what the storefront is serving:
   ```bash
   curl -s -X POST "https://$SHOPIFY_SHOP_DOMAIN/admin/api/$SHOPIFY_API_VERSION/graphql.json" \
     -H "X-Shopify-Access-Token: $SHOPIFY_ACCESS_TOKEN" -H "Content-Type: application/json" \
     --data '{"query":"{ productVariants(first:1, query:\"sku:HAO-1\"){ nodes{ sku inventoryQuantity inventoryItem{tracked} } } }"}'
   ```
   If ERP balance ≤ 0 but Shopify `inventoryQuantity > 0` → the out-of-stock signal was dropped.

## Root cause (fixed 2026-06-30)
Out-of-stock SKUs were **skipped instead of zeroed**:
- `internal/adapters/apix/stock.go` `dtoMap` returned `quantity - 3` (the client's 3-unit
  reserve buffer) → negative for any balance ≤ 2, and the ERP itself returns negatives.
- `internal/app/usecases/sync_stocks.go:66` dropped negative stock (`skippedNegative++; continue`)
  → it was never pushed to Shopify → Shopify kept the last positive quantity → "available".

Live data at the time: 3080 / 6498 SKUs (47%) were in the skip path (2014 at balance exactly 0).
A run from 2026-04-26 logged `Stock sync completed … skipped_negative=2894`.

### The fix
`dtoMap` now clamps at 0 (`stock := quantity - 3; if stock < 0 { stock = 0 }`). Out-of-stock
items push **0** (zeroed = unavailable); balance ≤ 3 still counts as out of stock per the reserve.
After fixing, a sync logs `skipped_negative=0` and the affected variants read `inventoryQuantity=0`.

## NOT the cause (so you don't chase it)
- **"Take stock only from warehouses 1+3 / maybe Gila's warehouse"** — warehouse aggregation
  happens server-side in the חשבשבת `/stocksProducts` API; we send only `{"dbName":"EMANUEL"}`,
  no warehouse param. For the reported SKUs the ERP already returned ≤ 0, so selection was fine.
  Only chase this if step 1 shows the ERP returning a *positive* balance for a truly-empty item —
  then it's a question for whoever maintains the חשבשבת API, not this code.
- **The `-3` buffer itself** — it makes more items read "out of stock", not fewer. It is an
  intentional reserve; do not remove it without the client's explicit OK.

## Apply the fix to the live catalog
The code fix only takes effect when a stock sync runs. See `.claude/RUNBOOK.md`:
```bash
go build -o sync-stock-and-price ./cmd/sync-stock-and-price
SYNC_ONLY_STEPS=syncStocks ./sync-stock-and-price
```
Expect `Stock sync completed sku=<N> skipped_negative=0`. `skipped_missing` = SKUs in the ERP
but not in the Shopify catalog (truncated/placeholder SKUs like `HDC-`, `11-`) — non-fatal.

⚠️ Staleness caveat: per `.claude/PRICE_ISSUE_KNOWN_ROOT_CAUSE.md`, the exporter has had no
reliable cron/timer on `instance-emanuel` since 2026-05-27 — syncs are run manually. If stock
looks stale across the board, the sync simply hasn't run; running it (above) is the fix.

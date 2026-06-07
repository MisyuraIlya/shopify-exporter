# KNOWN ROOT CAUSE — USD price drift / "price not updating" complaints

**Read this FIRST on any Emanuel price complaint (USD doesn't match ERP / drifts day-to-day).**
It is almost always the Shopify Payments single-currency block — NOT a bug in this exporter or the
cron. Don't re-debug the sync from scratch.

## Triage (all read-only, ~1 min)
Use creds from `.env` (SHOPIFY_SHOP_DOMAIN, SHOPIFY_ACCESS_TOKEN, SHOPIFY_API_VERSION):
1. `shop{ currencyCode enabledPresentmentCurrencies }` → if only `["ILS"]`, USD is impossible.
2. `shopifyPaymentsAccount{ activated }` → if **null**, Shopify Payments is OFF → multi-currency blocked.
3. `productVariants(query:"sku:CAJ-4"){ contextualPricing(context:{country:US}){ price{amount currencyCode} } }`
   → returns **ILS** (not USD) = no USD price list reaching US shoppers.
4. ERP check: `POST $API_BASE_URL/prices-latest` (body `{"dbName":"EMANUEL"}`) → confirms ERP USD is fine.

## Why the price "drifts" (e.g. CAJ-4 ERP $77.01 but site shows 78–81)
A fixed USD price never drifts. The drift IS the proof: the US storefront shows the **ILS base
price (231) auto-converted at the live daily FX rate** (231/78≈2.95, 231/81≈2.85), via a converter
app. ERP USD = 231/3.0 = 77.01 (fixed). They never match.

## Root cause
- `shopifyPaymentsAccount = null` → Shopify Payments not active; store uses a 3rd-party (Israeli)
  gateway. Shopify then **rejects** setting any market to USD:
  `marketUpdate failed: input.currencySettings.baseCurrency: The shop's payment gateway does not
  support enabling more than one currency.`
- The "International" market currently has `currencySettings = null` and no web presence — it's an
  empty shell, not actually serving USD.
- It worked on 2026-05-27 (USD writes succeeded) → multi-currency was active then, disabled after.

## The fix is ACCOUNT-SIDE (client/Emanuel), not code
Enable Shopify Payments multi-currency + add USD presentment currency to the International market,
then re-run the price sync — the exporter already creates+populates the USD price list correctly.
If they keep the 3rd-party IL gateway, USD is impossible; it can only ever be an FX approximation.

## Two compounding issues found 2026-06-07
1. The exporter had not run on `instance-emanuel` since 2026-05-27 (no container/image/cron/timer).
   Base ILS prices were 11 days stale until run manually:
   `SYNC_ONLY_STEPS=syncPrices SYNC_TRACE_SKUS=CAJ-4,CSX-1 go run ./cmd/sync-stock-and-price`
   (no MySQL needed; writes to prod Shopify).
2. The USD-path failure is **swallowed as a WARNING** in `internal/adapters/shopify/prices.go`
   (~L220, "international catalog unavailable, USD fixed prices skipped") while the run still logs
   `Price sync completed` success. THIS is why every prior check said "all OK". Candidate fix:
   surface it (Telegram alert / non-zero exit) instead of a silent warning.

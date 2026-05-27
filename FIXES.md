# FIXES

A log of non-obvious issues and how they were resolved. Add a new entry at the top for each fix.

---

## 2026-05-27 — Collections (categories) missing in Shopify (Bestsellers, Personal Dedications)

### Symptom
ERP `/custom-categories` lists products under categories like **Bestsellers** (191 products)
and **Personal Dedications** (44 products), but those collections did **not exist** in Shopify.

### Root cause
The collections were simply **never created by a sync run** — and a fragility in the category
sync explains why a run can leave collections half-created:

- **One lookup error aborted the entire sync.** In `internal/app/usecases/sycnc_categories.go`,
  if a single `CheckCategoryExist` call errored (transient Shopify throttle/5xx after retries),
  the goroutine called `cancel()` and the function returned early — **skipping all remaining
  category creates, the static-collection block, AND every product attachment.**
- **Create/update happen fire-and-forget.** The usecase ignores the result of
  `CreateCategory`/`UpdateCategory` (they return nothing). Errors are logged inside the adapter
  (`shopify category create failed`) but never stop or surface at the usecase level.

Everything in the parse → existence-check → create path was verified working in isolation
(see method below), so the cause was the run never reaching/creating them, not a parse or API bug.

### How we diagnosed it (all read-only except the final create)
1. **ERP data** — `curl -s -X POST $API_BASE_URL/custom-categories -H "Authorization: $API_TOKEN"
   -d '{"dbName":"EMANUEL"}'` → confirmed BBE-1→Havdallah+Bestsellers, ZZ-100→Personal Dedications.
   Note the DTO maps `kef`→SKU, `NoteEnglish`/`NoteHebrew`→title (`dto/category.go`).
2. **Existence check** — replicated `findCollectionByTitle` (`collections(query:"title:<t>")`):
   returned **empty** for both → code *should* call `CreateCategory`.
3. **Create** — ran `collectionCreate` for both titles → **succeeded, zero userErrors**.
   Proved create works; the collections were just never created by a run.

### The fix (code — surgical)
`internal/app/usecases/sycnc_categories.go`: a per-category `CheckCategoryExist` error now
**logs and skips that one category** instead of `cancel()`-ing the whole run. Removed the
`errCh`/`cancel()` abort plumbing and the post-`wg.Wait()` `return err`. Result: remaining
creates, the static collections (Best Sellers / Personal Dedications / sale), and product
attachment always run even if one lookup fails. Then re-ran the sync to create + attach.

### Known follow-up (separate issue, surfaced during the re-run)
Product attachment logged **~368 warnings**: `collectionAddProducts failed: productIds:
Error adding <product> to collection` — including for existing manual collections
(Candlesticks, Painted Wood, Kiddush) whose products were already attached from a prior run.
The code already catches this and **skips non-fatally** (`isCollectionAddUserError` →
`logWarning`, `products.go:354`). Likely re-adding already-member products on a manually
sorted collection. Candidate fix for later: switch to `collectionAddProductsV2` (async) or
skip products already in the collection. Not blocking — collections still populate.

### For next time — triage checklist when a collection is "missing" in Shopify
1. **Confirm ERP has it** — the curl above; check exact English/Hebrew title casing
   (ERP uses `Bestsellers` one word, code's static list uses `Best Sellers` two words — these
   are different collections).
2. **Replicate the existence check** — `collections(query:"title:<t>")`. Empty = create should
   fire; a fuzzy match to a similar title = the lookup is the bug.
3. **Try `collectionCreate` directly** — if it succeeds with no userErrors, the collection was
   never created by a run; re-run `SYNC_ONLY_STEPS=syncCategories go run ./cmd/sync-to-shopify`.
4. **Watch for the abort pattern** — category create/attach errors are logged, not fatal; grep
   the run log for `attachment skipped` / `category create failed`.

### Note
Category sync has **no per-SKU filter** (unlike prices — `SYNC_ONLY_SKUS` only applies there).
The only scoping is `SYNC_ONLY_STEPS=syncCategories`, which still processes all ~7376 products.

---

## 2026-05-27 — International (USD) price shows lower than ERP price (DRA-1: 19.80 vs 23.36)

### Symptom
`/api/prices-latest` reports DRA-1 USD = **23.36** (PriceListNumber 7), but the Shopify
**International catalog** displayed **19.80** after sync. Looked like the price sync was
writing the wrong value.

### Root cause — NOT a sync bug
It was **Shopify stripping 18% Israeli VAT**, not the exporter:

```
23.36 ÷ 1.18 = 19.80   (18% VAT)
```

- `SHOPIFY_BASE_CURRENCY=ILS` and ILS prices are **tax-inclusive**.
- The International market handle is `united-states-and-canada`, which owes no Israeli VAT.
- Shopify setting **Settings → Taxes and duties → "Include or exclude tax based on your
  customer's country"** automatically removes the 18% VAT from the displayed USD price.

The sync had already written the correct **23.36** into the International USD price list
(`PriceList/20888781016`); Shopify was only *displaying* 19.80.

### How we diagnosed it
Ran a scoped, price-only sync with tracing for the one SKU:

```bash
SYNC_ONLY_STEPS=syncPrices SYNC_ONLY_SKUS=DRA-1 SYNC_TRACE_SKUS=DRA-1 \
  go run ./cmd/sync-stock-and-price
```

Trace confirmed the correct value was pushed with no userErrors:

```
trace price prepared sku=DRA-1 usd=23.36 usd_pl=7 ils=70.00 ils_pl=10
price fixed mutation price_list_id=.../PriceList/20888781016 currency=USD amount=23.36 compare_at=0.00
shopify price updated sku=DRA-1 usd=23.36   ✅ no userErrors
```

### The fix (Shopify only — no code change, no re-sync)
1. **Settings → Taxes and duties**
2. In the **Tax calculations** section
3. **Uncheck** ☐ **"Include or exclude tax based on your customer's country"**
4. Save

Result: tax-inclusive price displays as-is → International catalog shows **23.36**.
No re-sync needed; the stored price-list value was already 23.36.

### For next time — triage checklist when an international price looks "wrong"
1. **Compare the ratio first.** If Shopify price ÷ ERP price ≈ `1 / 1.18` (~0.847), it's the
   18% VAT strip, not a bug. (Other ratios: 50% = DiscountCode `5`.)
2. **Run the scoped trace** (command above) for the affected SKU. Look at the
   `price fixed mutation ... currency=USD amount=...` line:
   - Amount = ERP value, no userErrors → **code is correct**, the difference is a Shopify
     display/tax setting. Fix in admin, not in code.
   - Amount ≠ ERP value → it's a sync-side issue; investigate `sync_prices.go`
     (price-list selection: USD pref list = 7, ILS pref list = 10) and `shopify/prices.go`.
3. **Don't gross-up prices in code** to compensate for VAT — it makes the admin price list
   read wrong (27.56) and breaks if the VAT rate changes. Fix the tax setting instead.

### Useful debug env vars (`internal/debugsync/trace.go`)
- `SYNC_ONLY_STEPS=syncPrices|syncStocks` — run only certain steps
- `SYNC_ONLY_SKUS=DRA-1,...` — process only these SKUs
- `SYNC_TRACE_SKUS=DRA-1,...` — verbose per-SKU trace logging

# FIXES

A log of non-obvious issues and how they were resolved. Add a new entry at the top for each fix.

---

## 2026-08-04 — Stock was up to 6 hours stale by design: delta sync every 5 minutes

### Symptom
PM: customers order items the ERP already shows as out of stock. Not a broken sync this
time — the 6h cron was firing and succeeding. The window itself was the bug: a balance
that dropped one minute after a run stayed wrong on the storefront for nearly 6 hours.

### Why the interval could not simply be shortened
A stock run cost ~10,700 GraphQL calls, and every one of them serialises on the
process-wide lock in `adminGraphQLLimiter` (`products.go:649` holds it across the whole
round-trip), so the 4 goroutines in `sync_stocks.go` were never parallel against Shopify.
Three separate sources of waste:

1. `lookupInventoryItemIDBySKU` searched **one request per SKU** — ~6,500 of them.
2. `inventoryActivate` fired for **every** resolved SKU on **every** run, even when the
   item had been active at that location for months. ~4,200 pointless mutations a run.
3. All ~4,174 resolved SKUs were re-pushed whether or not the quantity had moved. The
   before-quantity needed to skip them was already being read — only for the report.

### The fix
**Resolution is now bulk.** `buildInventoryLookup` paginates every variant once
(`inventoryVariantSelection`, 100 a page) into `sku -> {inventoryItemID, tracked,
hasLevel, on_hand}`: ~42 requests instead of ~6,500. Below
`bulkInventoryLookupThreshold` (40 SKUs) it still searches per SKU, because paginating
the catalogue to find three SKUs is the worse trade — that is the delta case.

**Writes are now conditional.** A SKU whose Shopify `on_hand` already equals the target
*and* is tracked *and* has a level at the location is counted and skipped, no mutation.
`inventoryActivate` runs only when the lookup returned no `inventoryLevel` — the exact
condition it would create. Page size is 100, not the 250 the price lookup uses, because
this selection nests `inventoryItem -> inventoryLevel -> quantities` and Shopify bills a
connection at `first` × per-node cost; 250 risks the per-query cost ceiling outright.

**`SYNC_STOCK_MODE=delta`** diffs the ERP feed against `stock-state.json` (last pushed
quantities, written atomically, `internal/infra/stockstate`) and resolves only the SKUs
that moved. A quiet tick costs **one ERP request and zero Shopify calls**. Cron is now
`*/5` delta + daily full, both under one `flock` file so they cannot race the snapshot.

Net: ~43,000 Shopify calls/day at a 6h cadence became roughly 2,000/day at 5 minutes.
More frequent *and* lighter, because the old run's cost was almost entirely waste.

### Why the snapshot records the ERP and not Shopify
Tempting to cache Shopify's `on_hand` and skip the reads. Wrong: Shopify's on-hand moves
on its own when an order is **fulfilled**, so a cached copy goes stale and the sync would
skip a SKU that genuinely needs correcting. The snapshot answers only "did Hashavshevet
change this number since we pushed it". Drift in the other direction is what the **daily
full run** exists to reconcile — delta alone will slowly drift, so do not drop it.

### Also fixed here
- **`apix/stock.go` swallowed every error.** A failed `httpClient.Do` left `resp` nil and
  the next line's `defer resp.Body.Close()` panicked; a malformed body returned an empty
  slice, and `sync_stocks` then logged `no valid SKUs` and returned **success** having
  synced nothing. Rare at 4 runs a day, routine at 288. All paths return errors now.
- **`inventoryPolicy` was never set.** Nothing asserted it, so a variant switched to
  "continue selling when out of stock" (by hand in the admin, or by an app) stayed
  oversellable forever, and no amount of stock accuracy helps: pushing 0 still leaves it
  buyable. Now sent as `DENY` next to `tracked` on every create and update.
- **The stock sync force-tracked `ZZ-*` service items** if they ever appeared in the feed,
  while the product sync sets them untracked — the two would have fought every run. The
  stock push now skips the untracked prefixes. A no-op today (they are not in
  `/stocksProducts`), which is precisely why it was worth pinning down.
- **Report noise.** `REPORT_EMAIL_ONLY_ON_CHANGE=true` (set by the delta cron) suppresses
  the mail only when a run succeeded *and* changed nothing. The daily full run still mails
  unconditionally, so "empty inbox = the scheduler is dead" still holds for it.

### Verifying it without writing to the live store
`SYNC_STOCK_DRY_RUN=true` does every read and no write: it resolves the location, builds
the inventory map, computes the delta, then logs and reports each SKU that **would**
move (`before -> after`) plus the `inventoryItemUpdate` / `inventoryActivate` calls that
were withheld, and returns. The emailed CSV carries the full list, so a change of this
size is reviewable before it reaches a storefront.
```bash
SYNC_ONLY_STEPS=syncStocks SYNC_STOCK_DRY_RUN=true go run ./cmd/sync-stock-and-price
```
It is enforced in the **adapter** (`stock.go`), not the use case, so no other caller of
`SetOnHandQuantities` can write during a dry run. Two things it deliberately does not do:
- **It never writes the snapshot.** Recording proposals as pushed would make the next
  real delta skip all of them and go quiet while Shopify stayed wrong. This is the one
  failure mode that would turn a safety feature into the outage it was meant to prevent,
  so it is covered by a test.
- **It covers the stock step only.** The product sync still writes; there is no global
  dry run, because half the product mutations return IDs the next call depends on and
  stubbing them out fails in more confusing ways than it prevents.

### Before shortening the interval further
`/stocksProducts` returns ~6,500 rows in one request and is the only ERP load here, but
it is likely a heavy aggregation — `API_DURATION_MS` is 10s. Think in duty cycle, not
call count: an 8s query every 5 min is ~2.7% load, every 2 min is ~7%. Measure first:
```bash
for i in 1 2 3; do curl -s -o /dev/null -w "%{time_total}s\n" -X POST "$API_BASE_URL/stocksProducts" \
  -H "Content-Type: application/json" -H "Authorization: $API_TOKEN" -d '{"dbName":"EMANUEL"}'; sleep 5; done
```
An endpoint returning "changes since <timestamp>" would make this free and drops straight
into the same delta path.

### Still open
- The global `adminGraphQLLimiter` lock still serialises every Shopify request. It matters
  far less now that a run makes tens of calls instead of ten thousand; narrowing it to the
  rate-limit bookkeeping needs real token-bucket accounting and was left alone deliberately.
- The 3-unit reserve is still global (`apix/stock.go` `dtoMap`). With a 5-minute window it
  covers all but the fastest movers; per-SKU reserves would be the next step.
- Zero oversell is still not guaranteed — two databases, one window. Only checkout-time
  validation against the ERP (`POST /stocks`, single SKU) via a Shopify Function closes it.

---

## 2026-08-04 — Cron ran on time and failed every time: a backend/frontend deploy deletes the exporter image (Monday 12709784941)

### Symptom
PM: "B2B stock 0, Shopify stock 1 — customers order products that are no longer in
stock." Overselling on the Shopify storefront.

### Root cause — `docker image prune -a -f` in the *other* CI scripts
The scheduler installed on 2026-07-14 was present and firing exactly on time, but every
invocation died in under a second:

```
[2026-07-28T12:00:01Z] shopify-exporter cron run starting mode=stock steps=syncStocks
Unable to find image 'shopify-exporter-sync:latest' locally
docker: Error response from daemon: pull access denied for shopify-exporter-sync ...
```

`backend/backend_ci.sh:58` and `frontend/frontend_ci.sh:57` (in the **monorepo**, not
this repo) both ended with `sudo docker image prune -a -f`. `-a` removes every image not
referenced by a **running** container. The exporter is a one-shot `docker run --rm`
container that only exists during a cron tick, so it is idle — and therefore pruned —
whenever anyone deploys the site. The VM service account cannot pull the image back
(`devstorage.read_only`, no `artifactregistry.reader`), so nothing recovered on its own.

Timeline, from `cron-stock.log`:
- Jul 19 12:00 last good run → **Jul 19 18:00 first failure** (a deploy that afternoon).
- Jul 27: image restored while working ticket 12634541256 → runs healthy again.
- **Jul 28 11:03** `symfony-frontend` container recreated (frontend deploy) → image gone.
- Jul 28 12:00 → Aug 4 06:00: **59 consecutive failed stock runs, 7 days of frozen stock.**

Nothing surfaced it. `LOG_OUTPUT` is absent from `/home/spetsar/shopify-exporter.env`, so
it defaults to `stdout` and the configured `TELEGRAM_*` credentials were never used. A
dead cron and a healthy cron produced identical evidence: nothing.

### The fix — three layers, because one wasn't enough
1. **Stop deleting the image.** Both CI scripts now `docker image prune -f` (dangling
   only). Replacing `:latest` already leaves the old layer dangling, so disk is still
   reclaimed. Same change in both files, with the reason inline.
2. **Self-heal anyway** — the prune fix lives in a repo other people deploy from, so the
   runner no longer trusts it. `deploy/run-shopify-exporter.sh` `docker image inspect`s
   first and reloads from `/home/spetsar/shopify-exporter-sync.tar.gz` (left behind by
   the deploy) when the image is missing. The runner is now **version-controlled here**
   and installed by `deploy-sync-to-shopify.sh`; it previously existed only on the VM.
3. **Make silence impossible** — every run now emails a report (below). No report in the
   inbox means the job never started, which is the only signal that would have caught
   this on day one instead of day seven.

### Email report (`internal/report`, `internal/app/reporting`)
Both sync binaries build a `report.Run`, time each step, and mail an HTML report
(Hebrew/RTL, English technical footer) with every change attached as CSV. Sent on
failure too. Recipients/relay via `REPORT_EMAIL_TO` + `SMTP_*` — see `.env.example`.

The report shows **real before → after per SKU**, not "4,174 SKUs pushed". The before
values ride along on lookups the sync already performs, so this costs no extra API calls:
- **stock** — `lookupInventoryItemIDBySKU` (`stock.go`) already queries every SKU one by
  one; it now also selects `inventoryItem.inventoryLevel(locationId:).quantities(names:
  ["on_hand"])`. A SKU whose quantity is unchanged is counted, never listed.
- **prices** — `buildVariantLookup` (`prices.go`) already paginates every variant; it now
  also selects `price` and the `custom.usd_price` product metafield. `addFixedPrices`
  deliberately does not report — it writes the same money as `updateBasePrices` and would
  double every row.
- **products** — created and failed are listed per SKU; "updated" is a count only, since
  every existing product is re-pushed on every run.

`BeforeKnown=false` distinguishes "Shopify had no inventory level / no price" from "it was
zero" — worth keeping, it is the difference between a new item and a sell-out.

### Gotchas found while building it
- The alpine image had **no `ca-certificates`**, so the SMTP TLS handshake fails with an
  unknown-authority error. Added to the Dockerfile.
- `REPORT_TIMEZONE=Asia/Jerusalem` cannot resolve from a scratch alpine either; the
  reporting package imports `_ "time/tzdata"` to embed the database rather than relying
  on the OS.
- Office 365 negotiates **AUTH LOGIN**, which Go's `net/smtp` does not implement.
  `pickAuth` prefers PLAIN when advertised and falls back to a `loginAuth` type.
- Hebrew + emoji in the Subject must be RFC 2047 encoded (`mime.BEncoding`), and the CSV
  needs a UTF-8 BOM or Excel mangles the Hebrew product titles.

### Verify it without waiting for a sync
```bash
sudo docker run --rm --env-file /home/spetsar/shopify-exporter.env \
  --entrypoint /app/send-test-report shopify-exporter-sync:latest
```
Sends one sample report through the real relay. Locally: `go run ./cmd/send-test-report`.

### Triage checklist — "stock is stale again"
1. `sudo tail -20 /home/spetsar/shopify-exporter-logs/cron-stock.log` — is it
   `Unable to find image`? Then something pruned it again; the runner should now self-heal,
   so check the tarball exists at `/home/spetsar/shopify-exporter-sync.tar.gz`.
2. Check the report inbox. **No mail for a scheduled slot = the job never ran.** Mail with
   a failed step = the job ran and Shopify or the ERP rejected it.
3. `sudo docker images | grep shopify-exporter` and `sudo crontab -l`.

---

## 2026-07-27 — Inventory tracking never set by the product sync (Monday 12634541256)

### Symptom
PM: some products arrive in Shopify **with** inventory and others with **no inventory
tracking at all**, so a customer can buy an item that is already out of stock.

### Root cause — tracking was only ever a side effect of the stock sync
`ensureInventoryItemTracked` (`internal/adapters/shopify/stock.go:201`) was the **only**
place in the exporter that set `tracked: true`, and it runs solely for SKUs present in
the ERP `/stocksProducts` feed. The product sync never set it:
`updatePrimaryVariantIdentifiers` (`internal/adapters/shopify/products.go:743`) sent
`inventoryItem: { sku }` and nothing else — on **both** the create (`products.go:200`)
and update (`products.go:255`) paths, so a re-sync did not repair it either.

So a variant's tracking depended entirely on whether the stock sync had ever happened to
cover that SKU. Evidence from the ERP (read-only, 2026-07-27):
- `/products` (`noteIds 17,78,79,80,81`) → **4,262** catalogue SKUs — what we push.
- `/stocksProducts` → 6,551 rows, covering **4,174** of them.
- ⇒ **88 catalogue SKUs have no stock row at all** and could never become tracked.
- Aggravated by the missing 6h scheduler below: between 2026-06-10 and 2026-07-14 the
  stock sync never ran, so anything created in that window stayed untracked.

### The ZZ-* carve-out (do not "fix" this)
Of those 88, **45 are published — and all 45 are `ZZ-*`**: 44 × "נא פנו אלינו לביצוע
הזמנה" plus `ZZ-99` "תשלום בכרטיס אשראי - הזמנות מיוחדות". These are Hashavshevet
service/placeholder items with no stock by design. Tracking them would pin them at
quantity 0 and make them **unbuyable**, breaking the special-order flow. They must stay
untracked. (The remaining 43 are unpublished and never reach the storefront.)

### The fix
`updatePrimaryVariantIdentifiers` now sends `tracked` alongside the SKU, on create and
update, so every product-sync run enforces the correct state independent of the stock
feed — and corrects a variant that is already wrong, in either direction:

```go
variantInput["inventoryItem"] = map[string]any{
    "sku":     product.Sku,
    "tracked": c.shouldTrackInventory(product.Sku),
}
```

`shouldTrackInventory` tracks everything except the configured prefixes, from
`SHOPIFY_UNTRACKED_SKU_PREFIXES` (default `ZZ-`, see `internal/config/config.go`).
Set it to an empty value to track every SKU. Covered by
`internal/adapters/shopify/products_tracking_test.go`.

### Verify after deploying

⚠ **Do NOT use `query: "inventory_tracked:false"`.** Shopify does not support it as a
search field — it returns `"Invalid search field for this query" (code: invalid_field)`
in `extensions.search[].warnings` and **silently ignores the filter**, so you get the
first 250 variants regardless of tracking and it looks like a huge problem. The warning
is easy to miss because the query still returns HTTP 200 with plausible-looking data.

Paginate instead and read `inventoryItem.tracked` per variant:
```graphql
query($cursor: String) {
  productVariants(first: 250, after: $cursor) {
    pageInfo { hasNextPage endCursor }
    nodes { sku inventoryItem { tracked } }
  }
}
```
The catalogue is ~4,263 variants ⇒ 18 pages. Expect **every untracked SKU to be `ZZ-*`**.
Spot-check a normal SKU reads `tracked: true`:
`{ productVariants(first:1, query:"sku:HVM-1"){ nodes{ sku inventoryItem{tracked} } } }`
(`sku:` *is* a valid search field — it is only `inventory_tracked` that is not.)

Baseline measured 2026-07-27, immediately before this fix was deployed:
`TRACKED = 4130 · UNTRACKED = 133 (45 ZZ-* + 88 real merchandise SKUs)`.

---

## 2026-07-14 — Stock stale in Shopify: the "every 6 hours" sync never existed (CMG-28)

### Symptom
Shopify showed CMG-28 = **102** while the ERP `/stocks` returned `ITEMWARHBAL: 250`.
Believed the exporter ran every 6h on `instance-emanuel`; stock "wouldn't update" across
repeated attempts.

### Root cause — no scheduler at all
There was **no cron, no systemd timer, and no exporter container** on the VM. The exporter
is a one-shot batch container (`docker run --rm`, `main()` runs once and exits); the deploy
script only starts it once and then `docker image prune -a -f` removes the image. Nothing
re-ran it. Evidence:
- Root crontab had only the two symfony jobs (`CronManager` 2am, `CronImageUploader` 4am).
  No spetsar crontab, no `/etc/cron.d` entry, no systemd timer/unit for the exporter.
- Last stock run was **2026-06-10** (`sync-to-shopify-20260610-101417.log`); prior runs
  Apr 23 / Apr 26 / May 27 ×3 were sporadic manual runs — never a 6h cadence.
- 102 = 105 − 3 reserve: a stale value from an old run. ERP had since risen to 250.
- The env file had **no** `SYNC_ONLY_*` filters (that suspect was cleared).
- Bonus: the 2026-06-30 clamp fix had also never run in prod for the same reason.

### The fix
1. **Ran a sync now** — pushed current balances (CMG-28 → 247, verified via scoped trace
   `SYNC_ONLY_STEPS=syncStocks SYNC_ONLY_SKUS=CMG-28 SYNC_TRACE_SKUS=CMG-28`).
2. **Installed the missing scheduler** — root cron every 6h (00/06/12/18 UTC):
   `0 */6 * * * /usr/bin/flock -n /run/shopify-exporter.lock /home/spetsar/run-shopify-exporter.sh >> /home/spetsar/shopify-exporter-logs/cron.log 2>&1`
   `run-shopify-exporter.sh` runs the container in the foreground (flock prevents overlap),
   env-file `/home/spetsar/shopify-exporter.env`, logs volume-mounted as before.

### Image delivery gotcha (important for future deploys)
The VM service account (`...-compute@developer.gserviceaccount.com`, scope
`devstorage.read_only`) **cannot pull from Artifact Registry** — `docker pull` on the VM
fails with `denied: ...downloadArtifacts ... Unauthenticated request`. So the cron runs a
**locally-loaded** image, shipped via `docker save | scp | docker load` (no VM-side registry
auth). `deploy-sync-to-shopify.sh` still does `docker pull` on the VM and will FAIL until it
is switched to save/load (or the SA is granted `roles/artifactregistry.reader` **and** the VM
scope widened to `cloud-platform`, which needs a VM stop/start — avoided; prod symfony runs here).

### Triage checklist — "stock won't update"
1. `sudo crontab -l` on the VM — is there a `run-shopify-exporter.sh` line? Check
   `/home/spetsar/shopify-exporter-logs/cron.log` and the newest `sync-to-shopify-*.log`.
2. Confirm ERP truth: POST `/stocks` (single SKU) or `/stocksProducts` (bulk, `dbName` only).
   Shopify target = `ITEMWARHBAL − 3`, clamped at 0.
3. Scoped trace one SKU: `SYNC_ONLY_STEPS=syncStocks SYNC_ONLY_SKUS=<sku> SYNC_TRACE_SKUS=<sku>`.

---

## 2026-06-30 — Out-of-stock products show as AVAILABLE on storefront (HAO-1, CUP-2, CUJ-10)

### Symptom
Items that are out of stock in חשבשבת appear **in stock** on the Shopify website, while
the B2B "ישיר" site (reads ERP directly) shows them correctly. Client suspected we pull
stock from the wrong warehouse ("Gila's") instead of warehouses 1+3, or that the `-3`
reserve was the cause.

### Root cause — NOT the warehouse/buffer theory
Out-of-stock SKUs were **skipped instead of zeroed**, so Shopify kept the last positive
quantity and kept showing them as available. Two interacting pieces:
- `internal/adapters/apix/stock.go` `dtoMap` returned `quantity - 3` (the 3-unit reserve),
  which goes negative for any balance ≤ 2 — and the ERP itself returns negative balances.
- `internal/app/usecases/sync_stocks.go:66` `if item.Stock < 0 { skippedNegative++; continue }`
  dropped negatives entirely → they were never pushed to Shopify → stale positive quantity.

Verified with **live ERP** `/stocksProducts` (2026-06-30): HAO-1 balance=-1, CUJ-10=0,
CUP-2=-1 → all computed negative → all skipped. **3080 of 6498 SKUs (47%)** were in this
skip path (2014 at balance exactly 0). Prior log evidence: 2026-04-26 run logged
`Stock sync completed … skipped_negative=2894`. The warehouse selection is correct for
these examples — the ERP already returns ≤0; the code just discarded the "out of stock" signal.

### The fix (surgical — keeps the 3-unit reserve)
`internal/adapters/apix/stock.go` `dtoMap`: clamp the computed stock at 0
(`stock := quantity - 3; if stock < 0 { stock = 0 }`). Out-of-stock items now push **0**
to Shopify (zeroed = unavailable), while balance ≤ 3 still counts as out-of-stock per the
client's reserve. `sync_stocks.go` skip-negative branch is left as a defensive guard
(now unreachable for this path). Re-run a stock sync to apply across the catalog.

### Open follow-ups (not code)
1. **Run the stock sync.** Per the price ticket note, the exporter may have no cron/timer
   on `instance-emanuel` (last full stock run was 2026-04-26) — stale stock compounds this.
   Run: `SYNC_ONLY_STEPS=syncStocks go run ./cmd/sync-stock-and-price`.
2. **Images not pulling** (raised in same ticket) — separate media-sync issue, investigate apart.

### Triage curl (read-only) — confirm a SKU's true ERP balance
```bash
set -a; . ./.env; set +a
curl -s -X POST "$API_BASE_URL/stocksProducts" -H "Content-Type: application/json" \
  -H "Authorization: $API_TOKEN" -d '{"dbName":"EMANUEL"}' \
  | python3 -c 'import sys,json;[print(v["ITEMKEY"],v["ITEMWARHBAL"]) for v in json.load(sys.stdin)["items"] if v["ITEMKEY"].strip() in {"HAO-1","CUP-2","CUJ-10"}]'
```

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

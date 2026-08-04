package shopify

import (
	"context"
	"errors"
	"fmt"
	"shopify-exporter/internal/adapters/shopify/dto"
	"strings"
)

type StockService interface {
	SetOnHandQuantity(ctx context.Context, input StockInput) error
	SetOnHandQuantities(ctx context.Context, inputs []StockInput) error
}

type StockInput struct {
	SKU      string
	Quantity int
}

type resolvedStockInput struct {
	SKU             string
	InventoryItemID string
	Quantity        int
	Tracked         bool
	// HasLevel is whether Shopify already holds an inventory level for this item at
	// the location, i.e. whether inventoryActivate has anything left to do.
	HasLevel bool
	// BeforeQuantity is the on-hand quantity Shopify held before this run, read in
	// the same lookup that resolves the inventory item — no extra API call. It is
	// what turns "4,174 SKUs pushed" into the handful that actually moved.
	BeforeQuantity int
	BeforeKnown    bool
}

// variantInventory is what one SKU lookup yields.
type variantInventory struct {
	InventoryItemID string
	Tracked         bool
	OnHand          int
	OnHandKnown     bool
	// HasLevel distinguishes "Shopify has no inventory level at this location yet"
	// from "the level exists and reads zero". Only the former needs inventoryActivate.
	HasLevel bool
}

const (
	maxStockBatchSize = 100
	// maxInventoryPageSize sits below the 250 the price lookup uses: this selection
	// nests inventoryItem -> inventoryLevel -> quantities, and Shopify bills a
	// connection at `first` times the per-node cost, so a larger page risks tripping
	// the per-query cost ceiling outright rather than merely throttling.
	maxInventoryPageSize = 100
	// bulkInventoryLookupThreshold is where paginating the whole catalogue starts to
	// beat searching SKU by SKU. ~4,200 variants at 100 a page is ~42 requests, so
	// below that a per-SKU search is cheaper — which is exactly the delta case.
	bulkInventoryLookupThreshold = 40
)

// inventoryVariantSelection is the shared selection set for both inventory lookups.
// $locationId must be bound by the enclosing query.
const inventoryVariantSelection = `
				id
				sku
				inventoryItem {
					id
					tracked
					inventoryLevel(locationId: $locationId) {
						id
						quantities(names: ["on_hand"]) { name quantity }
					}
				}`

type variantInventoryListData struct {
	ProductVariants struct {
		Nodes    []dto.VariantInventoryNode `json:"nodes,omitempty"`
		PageInfo dto.ShopifyPageInfo        `json:"pageInfo,omitempty"`
	} `json:"productVariants"`
}

func (c *Client) SetOnHandQuantity(ctx context.Context, input StockInput) error {
	return c.SetOnHandQuantities(ctx, []StockInput{input})
}

func (c *Client) SetOnHandQuantities(ctx context.Context, inputs []StockInput) error {
	if c == nil {
		return errors.New("shopify client is nil")
	}
	if len(inputs) == 0 {
		return nil
	}

	locationID, err := c.primaryLocationID(ctx)
	if err != nil {
		return err
	}

	// Deduplicated but order-preserving: the caller sorts its input so two runs can be
	// diffed line by line in a trace, and iterating the map directly would shuffle that
	// back into Go's randomised order.
	unique := make(map[string]StockInput, len(inputs))
	order := make([]string, 0, len(inputs))
	skippedUntracked := 0
	for _, input := range inputs {
		sku := strings.TrimSpace(input.SKU)
		if sku == "" {
			continue
		}
		if input.Quantity < 0 {
			return fmt.Errorf("shopify stock quantity must be non-negative sku=%s", sku)
		}
		// Service/placeholder SKUs (ZZ-* by default) must stay untracked and buyable.
		// The product sync sets tracked=false for them; pushing a quantity here would
		// force tracking back on and the two syncs would fight over every run. The
		// prefixes are not expected in /stocksProducts at all, so this is normally a
		// no-op — it exists so that an ERP change cannot silently pin them at 0.
		if !c.shouldTrackInventory(sku) {
			skippedUntracked++
			continue
		}
		if _, seen := unique[sku]; !seen {
			order = append(order, sku)
		}
		unique[sku] = StockInput{
			SKU:      sku,
			Quantity: input.Quantity,
		}
	}

	if len(unique) == 0 {
		if skippedUntracked > 0 {
			c.logWarning(fmt.Sprintf("stock sync skipped: untracked service skus=%d", skippedUntracked))
		}
		return nil
	}

	lookup, err := c.inventoryLookup(ctx, len(unique), locationID)
	if err != nil {
		return err
	}

	resolved := make([]resolvedStockInput, 0, len(unique))
	skippedMissing := 0
	unchanged := 0
	for _, sku := range order {
		input := unique[sku]
		variant, found, err := c.resolveVariantInventory(ctx, input.SKU, locationID, lookup)
		if err != nil {
			return err
		}
		if !found || variant.InventoryItemID == "" {
			skippedMissing++
			continue
		}

		// Nothing to do when Shopify already holds this quantity and the item is in
		// the right tracking/activation state. This is what makes a frequent cron
		// cheap: a full catalogue push of 4,174 unchanged SKUs becomes zero writes.
		if variant.OnHandKnown && variant.OnHand == input.Quantity && variant.Tracked && variant.HasLevel {
			unchanged++
			// Recorded so the report's unchanged tally stays honest even though no
			// mutation was sent — Shopify genuinely holds this value.
			c.reportStockSeen(resolvedStockInput{
				SKU:            input.SKU,
				Quantity:       input.Quantity,
				BeforeQuantity: variant.OnHand,
				BeforeKnown:    true,
			})
			c.traceSKU(
				input.SKU,
				"stock unchanged inventory_item_id=%s location_id=%s quantity=%d",
				variant.InventoryItemID,
				locationID,
				input.Quantity,
			)
			continue
		}

		resolved = append(resolved, resolvedStockInput{
			SKU:             input.SKU,
			InventoryItemID: variant.InventoryItemID,
			Quantity:        input.Quantity,
			Tracked:         variant.Tracked,
			HasLevel:        variant.HasLevel,
			BeforeQuantity:  variant.OnHand,
			BeforeKnown:     variant.OnHandKnown,
		})
		c.traceSKU(
			input.SKU,
			"stock resolved inventory_item_id=%s location_id=%s tracked=%t has_level=%t before_quantity=%d before_known=%t quantity=%d",
			variant.InventoryItemID,
			locationID,
			variant.Tracked,
			variant.HasLevel,
			variant.OnHand,
			variant.OnHandKnown,
			input.Quantity,
		)
	}

	// The ERP stock feed covers ~6,500 rows against ~4,200 published SKUs, so roughly
	// 40% of every run has no Shopify variant — that is normal here, not a problem.
	// Aggregated into one line rather than one warning per SKU: at a five-minute
	// cadence the per-SKU form buries every other line in the log.
	if skippedMissing > 0 {
		c.logWarning(fmt.Sprintf("stock sync missing variants=%d", skippedMissing))
	}
	if skippedUntracked > 0 {
		c.logWarning(fmt.Sprintf("stock sync skipped untracked service skus=%d", skippedUntracked))
	}

	c.reportIncr("stock", "unchanged", int64(unchanged))
	c.reportIncr("stock", "skipped_missing_variant", int64(skippedMissing))
	c.reportIncr("stock", "skipped_untracked", int64(skippedUntracked))

	if len(resolved) == 0 {
		c.logSuccess(fmt.Sprintf(
			"shopify stock already current items=%d skipped_missing=%d skipped_untracked=%d",
			unchanged,
			skippedMissing,
			skippedUntracked,
		))
		return nil
	}

	// Dry run stops here, having done every read and no write. The resolved list is
	// still handed to the report, because "these 812 SKUs would move, here is each
	// before -> after" is the entire deliverable — it is what makes a change of this
	// size checkable before it touches a live storefront.
	if c.config.StockDryRun {
		return c.reportDryRun(resolved, locationID, unchanged, skippedMissing, skippedUntracked)
	}

	query := `
	mutation inventorySetOnHandQuantities($input: InventorySetOnHandQuantitiesInput!) {
		inventorySetOnHandQuantities(input: $input) {
			userErrors { field message }
		}
	}`

	for start := 0; start < len(resolved); start += maxStockBatchSize {
		end := start + maxStockBatchSize
		if end > len(resolved) {
			end = len(resolved)
		}
		batch := resolved[start:end]
		for _, item := range batch {
			if err := c.ensureInventoryItemTracked(ctx, item.InventoryItemID, item.Tracked); err != nil {
				return err
			}
			// Skipped when the lookup already returned a level for this location:
			// inventoryActivate would be a no-op, and it used to run once per SKU on
			// every single run — ~4,000 pointless mutations an hour.
			if !item.HasLevel {
				if err := c.ensureInventoryItemActive(ctx, item.InventoryItemID, locationID); err != nil {
					return err
				}
			}
		}
		payload := make([]map[string]any, 0, len(batch))
		for _, item := range batch {
			c.traceSKU(
				item.SKU,
				"stock mutation inventory_item_id=%s location_id=%s quantity=%d",
				item.InventoryItemID,
				locationID,
				item.Quantity,
			)
			payload = append(payload, map[string]any{
				"inventoryItemId": item.InventoryItemID,
				"locationId":      locationID,
				"quantity":        item.Quantity,
			})
		}
		var data dto.InventorySetOnHandQuantitiesData
		if err := c.graphqlRequest(ctx, query, map[string]any{
			"input": map[string]any{
				"reason":        "correction",
				"setQuantities": payload,
			},
		}, &data); err != nil {
			return err
		}
		if err := userErrorsToDetailedError("inventorySetOnHandQuantities", data.InventorySetOnHandQuantities.UserErrors); err != nil {
			return err
		}
		for _, item := range batch {
			// Recorded only after the mutation succeeded, so the report describes
			// what Shopify actually holds rather than what we intended to send.
			c.reportStockSeen(item)
			c.traceSKU(
				item.SKU,
				"stock synced inventory_item_id=%s location_id=%s quantity=%d",
				item.InventoryItemID,
				locationID,
				item.Quantity,
			)
		}
	}

	c.reportIncr("stock", "pushed", int64(len(resolved)))
	c.logSuccess(fmt.Sprintf(
		"shopify stock updated items=%d unchanged=%d skipped_missing=%d skipped_untracked=%d",
		len(resolved),
		unchanged,
		skippedMissing,
		skippedUntracked,
	))
	return nil
}

// reportDryRun logs and records everything the run would have written, and returns
// without sending a mutation. Each line names the specific writes that were withheld,
// so the log answers "what exactly would have changed" rather than just "nothing ran".
func (c *Client) reportDryRun(
	resolved []resolvedStockInput,
	locationID string,
	unchanged, skippedMissing, skippedUntracked int,
) error {
	wouldTrack := 0
	wouldActivate := 0

	for _, item := range resolved {
		writes := make([]string, 0, 3)
		if !item.Tracked {
			wouldTrack++
			writes = append(writes, "inventoryItemUpdate{tracked:true}")
		}
		if !item.HasLevel {
			wouldActivate++
			writes = append(writes, "inventoryActivate")
		}
		writes = append(writes, "inventorySetOnHandQuantities")

		before := "unknown"
		if item.BeforeKnown {
			before = fmt.Sprintf("%d", item.BeforeQuantity)
		}
		c.logWarning(fmt.Sprintf(
			"DRY RUN would change sku=%s %s -> %d inventory_item_id=%s writes=%s",
			item.SKU,
			before,
			item.Quantity,
			item.InventoryItemID,
			strings.Join(writes, ","),
		))

		// Recorded so the emailed CSV carries every intended move. The dry_run counter
		// below is what tells the reader these are proposals, not history.
		c.reportStockSeen(item)
		c.traceSKU(
			item.SKU,
			"stock dry-run inventory_item_id=%s location_id=%s before=%s quantity=%d",
			item.InventoryItemID,
			locationID,
			before,
			item.Quantity,
		)
	}

	c.reportIncr("stock", "dry_run", 1)
	c.reportIncr("stock", "dry_run_would_push", int64(len(resolved)))
	c.reportIncr("stock", "dry_run_would_track", int64(wouldTrack))
	c.reportIncr("stock", "dry_run_would_activate", int64(wouldActivate))
	c.reportWarning("stock", fmt.Sprintf(
		"DRY RUN: nothing was written to Shopify. %d SKUs would change.",
		len(resolved),
	))

	c.logSuccess(fmt.Sprintf(
		"DRY RUN complete, no writes sent: would_push=%d would_track=%d would_activate=%d unchanged=%d skipped_missing=%d skipped_untracked=%d",
		len(resolved),
		wouldTrack,
		wouldActivate,
		unchanged,
		skippedMissing,
		skippedUntracked,
	))
	return nil
}

// inventoryLookup returns a sku -> inventory map for the whole catalogue when the run
// touches enough SKUs to justify paginating it, and nil when a per-SKU search is
// cheaper. Nil is not an error: resolveVariantInventory falls back to the search.
func (c *Client) inventoryLookup(ctx context.Context, skuCount int, locationID string) (map[string]variantInventory, error) {
	if skuCount < bulkInventoryLookupThreshold {
		return nil, nil
	}
	return c.buildInventoryLookup(ctx, locationID)
}

// resolveVariantInventory reads one SKU out of the bulk map, or searches for it when
// no map was built. A SKU absent from the map has no Shopify variant.
func (c *Client) resolveVariantInventory(
	ctx context.Context,
	sku, locationID string,
	lookup map[string]variantInventory,
) (variantInventory, bool, error) {
	if lookup != nil {
		variant, ok := lookup[sku]
		return variant, ok, nil
	}

	variant, err := c.lookupInventoryItemIDBySKU(ctx, sku, locationID)
	if err != nil {
		if missing, ok := isVariantNotFoundError(err); ok {
			c.logWarning(missing.Error())
			return variantInventory{}, false, nil
		}
		return variantInventory{}, false, err
	}
	return variant, variant.InventoryItemID != "", nil
}

// buildInventoryLookup paginates every variant once and returns sku -> inventory,
// including the live on-hand quantity at the location. This replaces one search
// request per SKU: ~42 requests for the catalogue instead of ~6,500.
func (c *Client) buildInventoryLookup(ctx context.Context, locationID string) (map[string]variantInventory, error) {
	locationID = strings.TrimSpace(locationID)
	if locationID == "" {
		return nil, errors.New("shopify location id is required")
	}

	query := `
	query inventoryVariants($first: Int!, $after: String, $locationId: ID!) {
		productVariants(first: $first, after: $after) {
			nodes {` + inventoryVariantSelection + `
			}
			pageInfo { hasNextPage endCursor }
		}
	}`

	lookup := make(map[string]variantInventory)
	after := ""
	pages := 0
	for {
		vars := map[string]any{
			"first":      maxInventoryPageSize,
			"locationId": locationID,
		}
		if after != "" {
			vars["after"] = after
		}

		var data variantInventoryListData
		if err := c.graphqlRequest(ctx, query, vars, &data); err != nil {
			return nil, err
		}
		pages++

		for _, node := range data.ProductVariants.Nodes {
			sku := strings.TrimSpace(node.SKU)
			if sku == "" || node.InventoryItem == nil {
				continue
			}
			if _, exists := lookup[sku]; exists {
				continue
			}
			onHand, onHandKnown := node.InventoryItem.InventoryLevel.OnHand()
			lookup[sku] = variantInventory{
				InventoryItemID: strings.TrimSpace(node.InventoryItem.ID),
				Tracked:         node.InventoryItem.Tracked,
				OnHand:          onHand,
				OnHandKnown:     onHandKnown,
				HasLevel:        node.InventoryItem.InventoryLevel != nil,
			}
		}

		if !data.ProductVariants.PageInfo.HasNextPage {
			break
		}
		after = strings.TrimSpace(data.ProductVariants.PageInfo.EndCursor)
		if after == "" {
			break
		}
	}

	c.logSuccess(fmt.Sprintf("shopify inventory lookup built skus=%d pages=%d", len(lookup), pages))
	return lookup, nil
}

// lookupInventoryItemIDBySKU resolves one SKU's inventory item and, for the given
// location, its current on-hand quantity. Used for small runs (a delta tick), where
// searching a handful of SKUs beats paginating the whole catalogue.
func (c *Client) lookupInventoryItemIDBySKU(ctx context.Context, sku, locationID string) (variantInventory, error) {
	sku = strings.TrimSpace(sku)
	if sku == "" {
		return variantInventory{}, errors.New("shopify sku is required")
	}

	query := `
	query inventoryItemBySku($first: Int!, $query: String!, $locationId: ID!) {
		productVariants(first: $first, query: $query) {
			nodes {` + inventoryVariantSelection + `
			}
		}
	}`

	var data dto.VariantInventoryQueryData
	if err := c.graphqlRequest(ctx, query, map[string]any{
		"first":      1,
		"query":      buildSearchQuery("sku", sku),
		"locationId": strings.TrimSpace(locationID),
	}, &data); err != nil {
		return variantInventory{}, err
	}
	if len(data.ProductVariants.Nodes) == 0 {
		return variantInventory{}, &variantNotFoundError{SKU: sku}
	}
	node := data.ProductVariants.Nodes[0]
	if node.InventoryItem == nil {
		return variantInventory{}, fmt.Errorf("shopify inventory item missing for sku %s", sku)
	}
	onHand, onHandKnown := node.InventoryItem.InventoryLevel.OnHand()
	return variantInventory{
		InventoryItemID: strings.TrimSpace(node.InventoryItem.ID),
		Tracked:         node.InventoryItem.Tracked,
		OnHand:          onHand,
		OnHandKnown:     onHandKnown,
		HasLevel:        node.InventoryItem.InventoryLevel != nil,
	}, nil
}

func (c *Client) ensureInventoryItemTracked(ctx context.Context, inventoryItemID string, tracked bool) error {
	inventoryItemID = strings.TrimSpace(inventoryItemID)
	if inventoryItemID == "" || tracked {
		return nil
	}

	query := `
	mutation inventoryItemUpdate($id: ID!, $input: InventoryItemInput!) {
		inventoryItemUpdate(id: $id, input: $input) {
			inventoryItem { id tracked }
			userErrors { field message }
		}
	}`

	var data dto.InventoryItemUpdateData
	if err := c.graphqlRequest(ctx, query, map[string]any{
		"id": inventoryItemID,
		"input": map[string]any{
			"tracked": true,
		},
	}, &data); err != nil {
		return err
	}
	if err := userErrorsToDetailedError("inventoryItemUpdate", data.InventoryItemUpdate.UserErrors); err != nil {
		return err
	}
	return nil
}

func (c *Client) ensureInventoryItemActive(ctx context.Context, inventoryItemID, locationID string) error {
	inventoryItemID = strings.TrimSpace(inventoryItemID)
	locationID = strings.TrimSpace(locationID)
	if inventoryItemID == "" || locationID == "" {
		return errors.New("shopify inventory item id and location id are required")
	}

	query := `
	mutation inventoryActivate($inventoryItemId: ID!, $locationId: ID!) {
		inventoryActivate(inventoryItemId: $inventoryItemId, locationId: $locationId) {
			inventoryLevel { id }
			userErrors { field message }
		}
	}`

	var data dto.InventoryActivateData
	if err := c.graphqlRequest(ctx, query, map[string]any{
		"inventoryItemId": inventoryItemID,
		"locationId":      locationID,
	}, &data); err != nil {
		return err
	}
	if err := userErrorsToDetailedError("inventoryActivate", data.InventoryActivate.UserErrors); err != nil {
		return err
	}
	return nil
}

func (c *Client) primaryLocationID(ctx context.Context) (string, error) {
	if c == nil {
		return "", errors.New("shopify client is nil")
	}

	c.locationMu.Lock()
	if c.locationID != "" {
		locationID := c.locationID
		c.locationMu.Unlock()
		return locationID, nil
	}
	c.locationMu.Unlock()

	query := `
	query locations($first: Int!) {
		locations(first: $first) {
			nodes { id name isActive }
		}
	}`

	var data dto.LocationsQueryData
	if err := c.graphqlRequest(ctx, query, map[string]any{"first": 50}, &data); err != nil {
		return "", err
	}
	locationID := ""
	for _, location := range data.Locations.Nodes {
		if location.ID == "" {
			continue
		}
		if location.IsActive {
			locationID = location.ID
			break
		}
	}
	if locationID == "" && len(data.Locations.Nodes) > 0 {
		locationID = data.Locations.Nodes[0].ID
	}
	if locationID == "" {
		return "", errors.New("shopify location not found")
	}

	c.locationMu.Lock()
	c.locationID = locationID
	c.locationMu.Unlock()
	return locationID, nil
}

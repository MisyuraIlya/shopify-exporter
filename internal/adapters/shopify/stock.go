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
}

const (
	maxStockBatchSize = 100
)

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

	unique := make(map[string]StockInput, len(inputs))
	for _, input := range inputs {
		sku := strings.TrimSpace(input.SKU)
		if sku == "" {
			continue
		}
		if input.Quantity < 0 {
			return fmt.Errorf("shopify stock quantity must be non-negative sku=%s", sku)
		}
		unique[sku] = StockInput{
			SKU:      sku,
			Quantity: input.Quantity,
		}
	}

	if len(unique) == 0 {
		return nil
	}

	resolved := make([]resolvedStockInput, 0, len(unique))
	skippedMissing := 0
	for _, input := range unique {
		variant, err := c.lookupInventoryItemIDBySKU(ctx, input.SKU, locationID)
		if err != nil {
			if missing, ok := isVariantNotFoundError(err); ok {
				skippedMissing++
				c.logWarning(missing.Error())
				c.reportWarning("stock", missing.Error())
				continue
			}
			return err
		}
		if variant.InventoryItemID == "" {
			skippedMissing++
			message := fmt.Sprintf("shopify inventory item not found for sku %s", input.SKU)
			c.logWarning(message)
			c.reportWarning("stock", message)
			continue
		}
		resolved = append(resolved, resolvedStockInput{
			SKU:             input.SKU,
			InventoryItemID: variant.InventoryItemID,
			Quantity:        input.Quantity,
			Tracked:         variant.Tracked,
			BeforeQuantity:  variant.OnHand,
			BeforeKnown:     variant.OnHandKnown,
		})
		c.traceSKU(
			input.SKU,
			"stock resolved inventory_item_id=%s location_id=%s tracked=%t before_quantity=%d before_known=%t quantity=%d",
			variant.InventoryItemID,
			locationID,
			variant.Tracked,
			variant.OnHand,
			variant.OnHandKnown,
			input.Quantity,
		)
	}

	if len(resolved) == 0 {
		if skippedMissing > 0 {
			c.logWarning(fmt.Sprintf("stock sync skipped: missing variants=%d", skippedMissing))
		}
		return nil
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
			if err := c.ensureInventoryItemActive(ctx, item.InventoryItemID, locationID); err != nil {
				return err
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
	c.reportIncr("stock", "skipped_missing_variant", int64(skippedMissing))
	c.logSuccess(fmt.Sprintf("shopify stock updated items=%d skipped_missing=%d", len(resolved), skippedMissing))
	return nil
}

// lookupInventoryItemIDBySKU resolves one SKU's inventory item and, for the given
// location, its current on-hand quantity. The quantity rides along on the lookup the
// sync already performs per SKU, so before/after reporting costs no extra requests.
func (c *Client) lookupInventoryItemIDBySKU(ctx context.Context, sku, locationID string) (variantInventory, error) {
	sku = strings.TrimSpace(sku)
	if sku == "" {
		return variantInventory{}, errors.New("shopify sku is required")
	}

	query := `
	query inventoryItemBySku($first: Int!, $query: String!, $locationId: ID!) {
		productVariants(first: $first, query: $query) {
			nodes {
				id
				sku
				inventoryItem {
					id
					tracked
					inventoryLevel(locationId: $locationId) {
						id
						quantities(names: ["on_hand"]) { name quantity }
					}
				}
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

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
		inventoryItemID, tracked, err := c.lookupInventoryItemIDBySKU(ctx, input.SKU)
		if err != nil {
			if missing, ok := isVariantNotFoundError(err); ok {
				skippedMissing++
				c.logWarning(missing.Error())
				continue
			}
			return err
		}
		if inventoryItemID == "" {
			skippedMissing++
			c.logWarning(fmt.Sprintf("shopify inventory item not found for sku %s", input.SKU))
			continue
		}
		resolved = append(resolved, resolvedStockInput{
			SKU:             input.SKU,
			InventoryItemID: inventoryItemID,
			Quantity:        input.Quantity,
			Tracked:         tracked,
		})
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
	}

	c.logSuccess(fmt.Sprintf("shopify stock updated items=%d skipped_missing=%d", len(resolved), skippedMissing))
	return nil
}

func (c *Client) lookupInventoryItemIDBySKU(ctx context.Context, sku string) (string, bool, error) {
	sku = strings.TrimSpace(sku)
	if sku == "" {
		return "", false, errors.New("shopify sku is required")
	}

	query := `
	query inventoryItemBySku($first: Int!, $query: String!) {
		productVariants(first: $first, query: $query) {
			nodes { id sku inventoryItem { id tracked } }
		}
	}`

	var data dto.VariantInventoryQueryData
	if err := c.graphqlRequest(ctx, query, map[string]any{
		"first": 1,
		"query": buildSearchQuery("sku", sku),
	}, &data); err != nil {
		return "", false, err
	}
	if len(data.ProductVariants.Nodes) == 0 {
		return "", false, &variantNotFoundError{SKU: sku}
	}
	node := data.ProductVariants.Nodes[0]
	if node.InventoryItem == nil {
		return "", false, fmt.Errorf("shopify inventory item missing for sku %s", sku)
	}
	return strings.TrimSpace(node.InventoryItem.ID), node.InventoryItem.Tracked, nil
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

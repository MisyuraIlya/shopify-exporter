package shopify

import (
	"context"
	"errors"
	"fmt"
	"shopify-exporter/internal/adapters/shopify/dto"
	"strconv"
	"strings"
)

type ProductOrderService interface {
	ReorderCollectionProductsByCategory(ctx context.Context, categoryTitle string, orderItems []CollectionOrderItem) error
}

const maxCollectionReorderMoves = 250

type CollectionOrderItem struct {
	SKU         string
	OrderNumber int
}

type collectionReorderProductsData struct {
	CollectionReorderProducts struct {
		UserErrors []dto.ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"collectionReorderProducts"`
}

type collectionSortOrderUpdateData struct {
	CollectionUpdate struct {
		UserErrors []dto.ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"collectionUpdate"`
}

func (c *Client) ReorderCollectionProductsByCategory(ctx context.Context, categoryTitle string, orderItems []CollectionOrderItem) error {
	if c == nil {
		return errors.New("shopify client is nil")
	}

	title := strings.TrimSpace(categoryTitle)
	if title == "" {
		return errors.New("shopify category title is required")
	}
	if len(orderItems) == 0 {
		return nil
	}

	collectionID, err := c.findCollectionByTitle(ctx, title)
	if err != nil {
		return err
	}
	if collectionID == "" {
		c.logWarning(fmt.Sprintf("shopify category not found for order sync category=%s", title))
		return nil
	}
	if c.logger != nil {
		c.logger.LogSuccess(fmt.Sprintf(
			"shopify order sync category resolved category=%s id=%s incoming_skus=%d",
			title,
			collectionID,
			len(orderItems),
		))
	}

	if err := c.setCollectionSortOrderManual(ctx, collectionID); err != nil {
		return err
	}
	if c.logger != nil {
		c.logger.LogSuccess(fmt.Sprintf("shopify category sort order set to MANUAL category=%s id=%s", title, collectionID))
	}

	seen := make(map[string]struct{}, len(orderItems))
	type moveInput struct {
		ProductID   string
		NewPosition int
	}
	resolvedMoves := make([]moveInput, 0, len(orderItems))
	for _, item := range orderItems {
		trimmedSKU := strings.TrimSpace(item.SKU)
		if trimmedSKU == "" {
			continue
		}
		if item.OrderNumber < 0 {
			c.logWarning(fmt.Sprintf("shopify negative order number skipped category=%s sku=%s order=%d", title, trimmedSKU, item.OrderNumber))
			continue
		}
		normalized := strings.ToLower(trimmedSKU)
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}

		productID, err := c.lookupProductIDBySKU(ctx, trimmedSKU)
		if err != nil {
			return err
		}
		if productID == "" {
			c.logWarning(fmt.Sprintf("shopify product not found for order sync category=%s sku=%s", title, trimmedSKU))
			continue
		}

		if err := c.addProductToCollection(ctx, collectionID, productID); err != nil && !isCollectionAddUserError(err) {
			c.logWarning(fmt.Sprintf("shopify add product to category failed category=%s sku=%s: %s", title, trimmedSKU, err.Error()))
		}

		resolvedMoves = append(resolvedMoves, moveInput{
			ProductID:   productID,
			NewPosition: item.OrderNumber,
		})
	}

	if len(resolvedMoves) == 0 {
		return nil
	}

	reorderedCount := 0
	for start := 0; start < len(resolvedMoves); start += maxCollectionReorderMoves {
		end := start + maxCollectionReorderMoves
		if end > len(resolvedMoves) {
			end = len(resolvedMoves)
		}
		batch := resolvedMoves[start:end]
		moves := make([]map[string]any, 0, len(batch))
		for _, move := range batch {
			moves = append(moves, map[string]any{
				"id":          move.ProductID,
				"newPosition": strconv.Itoa(move.NewPosition),
			})
		}
		if err := c.collectionReorderProducts(ctx, collectionID, moves); err != nil {
			return err
		}
		reorderedCount += len(moves)
		if c.logger != nil {
			c.logger.LogSuccess(fmt.Sprintf(
				"shopify category reorder batch applied category=%s batch=%d moved=%d",
				title,
				(start/maxCollectionReorderMoves)+1,
				len(moves),
			))
		}
	}

	if c.logger != nil {
		c.logger.LogSuccess(fmt.Sprintf(
			"shopify category reorder completed category=%s reordered=%d",
			title,
			reorderedCount,
		))
	}

	return nil
}

func (c *Client) setCollectionSortOrderManual(ctx context.Context, collectionID string) error {
	collectionID = strings.TrimSpace(collectionID)
	if collectionID == "" {
		return errors.New("shopify collection id is required")
	}

	query := `
	mutation collectionUpdate($input: CollectionInput!) {
		collectionUpdate(input: $input) {
			userErrors { field message }
		}
	}`

	var data collectionSortOrderUpdateData
	if err := c.graphqlRequest(ctx, query, map[string]any{
		"input": map[string]any{
			"id":        collectionID,
			"sortOrder": "MANUAL",
		},
	}, &data); err != nil {
		return err
	}
	return userErrorsToError("collectionUpdate", data.CollectionUpdate.UserErrors)
}

func (c *Client) collectionReorderProducts(ctx context.Context, collectionID string, moves []map[string]any) error {
	collectionID = strings.TrimSpace(collectionID)
	if collectionID == "" {
		return errors.New("shopify collection id is required")
	}
	if len(moves) == 0 {
		return nil
	}

	query := `
	mutation collectionReorderProducts($id: ID!, $moves: [MoveInput!]!) {
		collectionReorderProducts(id: $id, moves: $moves) {
			userErrors { field message }
		}
	}`

	var data collectionReorderProductsData
	if err := c.graphqlRequest(ctx, query, map[string]any{
		"id":    collectionID,
		"moves": moves,
	}, &data); err != nil {
		return err
	}
	return userErrorsToError("collectionReorderProducts", data.CollectionReorderProducts.UserErrors)
}

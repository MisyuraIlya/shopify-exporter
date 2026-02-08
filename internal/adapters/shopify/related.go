package shopify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"shopify-exporter/internal/adapters/shopify/dto"
	"strings"
)

type RelatedService interface {
	EnsureRelatedProductsMetafieldDefinition(ctx context.Context) error
	UpsertRelatedProductsBySKU(ctx context.Context, sku string, relatedSKUs []string) error
}

const (
	relatedNamespace = "custom"
	relatedKey       = "related_products"
	relatedName      = "Related products"
	relatedType      = "list.product_reference"
)

type metafieldsSetRelatedData struct {
	MetafieldsSet struct {
		UserErrors []dto.ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"metafieldsSet"`
}

func (c *Client) EnsureRelatedProductsMetafieldDefinition(ctx context.Context) error {
	if c == nil {
		return errors.New("shopify client is nil")
	}

	definitions, err := c.listProductMetafieldDefinitions(ctx, relatedNamespace)
	if err != nil {
		return err
	}
	for _, definition := range definitions {
		if strings.EqualFold(strings.TrimSpace(definition.Key), relatedKey) {
			return nil
		}
	}

	query := `
	mutation metafieldDefinitionCreate($definition: MetafieldDefinitionInput!) {
		metafieldDefinitionCreate(definition: $definition) {
			userErrors { field message }
		}
	}`

	payload := map[string]any{
		"definition": map[string]any{
			"name":      relatedName,
			"namespace": relatedNamespace,
			"key":       relatedKey,
			"type":      relatedType,
			"ownerType": metafieldOwnerProduct,
		},
	}

	var resp struct {
		MetafieldDefinitionCreate struct {
			UserErrors []dto.ShopifyUserError `json:"userErrors,omitempty"`
		} `json:"metafieldDefinitionCreate"`
	}
	if err := c.graphqlRequest(ctx, query, payload, &resp); err != nil {
		return err
	}
	return userErrorsToError("metafieldDefinitionCreate", resp.MetafieldDefinitionCreate.UserErrors)
}

func (c *Client) UpsertRelatedProductsBySKU(ctx context.Context, sku string, relatedSKUs []string) error {
	if c == nil {
		return errors.New("shopify client is nil")
	}

	sku = strings.TrimSpace(sku)
	if sku == "" {
		return errors.New("shopify sku is required")
	}

	productID, err := c.lookupProductIDBySKU(ctx, sku)
	if err != nil {
		return err
	}
	if productID == "" {
		c.logWarning(fmt.Sprintf("shopify product not found for related sync sku=%s", sku))
		return nil
	}

	relatedIDs := make([]string, 0, len(relatedSKUs))
	seen := make(map[string]struct{}, len(relatedSKUs))
	for _, relatedSKU := range relatedSKUs {
		trimmedSKU := strings.TrimSpace(relatedSKU)
		if trimmedSKU == "" || strings.EqualFold(trimmedSKU, sku) {
			continue
		}
		normalized := strings.ToLower(trimmedSKU)
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}

		relatedProductID, err := c.lookupProductIDBySKU(ctx, trimmedSKU)
		if err != nil {
			return err
		}
		if relatedProductID == "" {
			c.logWarning(fmt.Sprintf("shopify related product not found sku=%s related_sku=%s", sku, trimmedSKU))
			continue
		}
		relatedIDs = append(relatedIDs, relatedProductID)
	}

	valueBytes, err := json.Marshal(relatedIDs)
	if err != nil {
		return fmt.Errorf("marshal related product references: %w", err)
	}

	query := `
	mutation metafieldsSet($metafields: [MetafieldsSetInput!]!) {
		metafieldsSet(metafields: $metafields) {
			userErrors { field message }
		}
	}`
	payload := map[string]any{
		"metafields": []map[string]any{
			{
				"ownerId":   productID,
				"namespace": relatedNamespace,
				"key":       relatedKey,
				"type":      relatedType,
				"value":     string(valueBytes),
			},
		},
	}

	var data metafieldsSetRelatedData
	if err := c.graphqlRequest(ctx, query, payload, &data); err != nil {
		return err
	}
	return userErrorsToError("metafieldsSet", data.MetafieldsSet.UserErrors)
}

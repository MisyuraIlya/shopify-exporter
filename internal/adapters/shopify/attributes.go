package shopify

import (
	"context"
	"errors"
	"fmt"
	"shopify-exporter/internal/adapters/shopify/dto"
	"strings"
)

type AttributeService interface {
	EnsureProductMetafieldDefinitions(ctx context.Context, definitions []ProductMetafieldDefinitionInput) error
	UpsertProductMetafields(ctx context.Context, sku string, fields []ProductMetafieldInput) error
}

type ProductMetafieldDefinitionInput struct {
	Namespace   string
	Key         string
	NameEnglish string
	NameHebrew  string
}

type ProductMetafieldInput struct {
	Namespace    string
	Key          string
	ValueEnglish string
	ValueHebrew  string
}

const (
	metafieldTypeText      = "single_line_text_field"
	metafieldOwnerProduct  = "PRODUCT"
	metafieldQueryPageSize = 100
	metafieldsSetBatchSize = 25
)

type metafieldTranslationResourceData struct {
	TranslatableResource *struct {
		TranslatableContent []struct {
			Key    string `json:"key,omitempty"`
			Digest string `json:"digest,omitempty"`
			Locale string `json:"locale,omitempty"`
		} `json:"translatableContent"`
	} `json:"translatableResource"`
}

func (c *Client) UpsertProductMetafields(ctx context.Context, sku string, fields []ProductMetafieldInput) error {
	if c == nil {
		return errors.New("shopify client is nil")
	}
	sku = strings.TrimSpace(sku)
	if sku == "" {
		return errors.New("shopify sku is required")
	}
	if len(fields) == 0 {
		return nil
	}

	productID, err := c.lookupProductIDBySKU(ctx, sku)
	if err != nil {
		c.logError("shopify product lookup failed", err)
		return err
	}
	if productID == "" {
		c.logWarning(fmt.Sprintf("shopify product not found sku=%s", sku))
		return nil
	}

	sanitized := make([]ProductMetafieldInput, 0, len(fields))
	for _, field := range fields {
		namespace := strings.TrimSpace(field.Namespace)
		key := strings.TrimSpace(field.Key)
		valueEnglish := strings.TrimSpace(field.ValueEnglish)
		valueHebrew := strings.TrimSpace(field.ValueHebrew)
		if namespace == "" || key == "" {
			continue
		}
		if valueEnglish == "" {
			valueEnglish = valueHebrew
		}
		if valueEnglish == "" {
			continue
		}
		sanitized = append(sanitized, ProductMetafieldInput{
			Namespace:    namespace,
			Key:          key,
			ValueEnglish: valueEnglish,
			ValueHebrew:  valueHebrew,
		})
	}

	if len(sanitized) == 0 {
		return nil
	}

	for start := 0; start < len(sanitized); start += metafieldsSetBatchSize {
		end := start + metafieldsSetBatchSize
		if end > len(sanitized) {
			end = len(sanitized)
		}
		batch := sanitized[start:end]

		payload := make([]map[string]any, 0, len(batch))
		for _, field := range batch {
			payload = append(payload, map[string]any{
				"ownerId":   productID,
				"namespace": field.Namespace,
				"key":       field.Key,
				"type":      metafieldTypeText,
				"value":     field.ValueEnglish,
			})
		}

		query := `
		mutation metafieldsSet($metafields: [MetafieldsSetInput!]!) {
			metafieldsSet(metafields: $metafields) {
				metafields { id namespace key value type }
				userErrors { field message }
			}
		}`

		var data dto.MetafieldsSetData
		if err := c.graphqlRequest(ctx, query, map[string]any{"metafields": payload}, &data); err != nil {
			return err
		}
		if err := userErrorsToError("metafieldsSet", data.MetafieldsSet.UserErrors); err != nil {
			return err
		}

		if len(data.MetafieldsSet.Metafields) == 0 {
			continue
		}

		inputMap := make(map[string]ProductMetafieldInput, len(batch))
		for _, field := range batch {
			inputMap[metafieldKey(field.Namespace, field.Key)] = field
		}

		for _, metafield := range data.MetafieldsSet.Metafields {
			field, ok := inputMap[metafieldKey(metafield.Namespace, metafield.Key)]
			if !ok {
				continue
			}
			if !shouldUpdateTranslation(field.ValueEnglish, field.ValueHebrew) {
				continue
			}
			if err := c.updateTranslation(ctx, metafield.ID, "value", field.ValueHebrew); err != nil {
				c.logError("shopify metafield translation update failed", err)
			}
		}
	}

	return nil
}

func (c *Client) EnsureProductMetafieldDefinitions(ctx context.Context, definitions []ProductMetafieldDefinitionInput) error {
	if c == nil {
		return errors.New("shopify client is nil")
	}
	if len(definitions) == 0 {
		return nil
	}

	byNamespace := make(map[string]map[string]ProductMetafieldDefinitionInput)
	for _, definition := range definitions {
		namespace := strings.TrimSpace(definition.Namespace)
		key := strings.TrimSpace(definition.Key)
		nameEnglish := strings.TrimSpace(definition.NameEnglish)
		nameHebrew := strings.TrimSpace(definition.NameHebrew)
		if namespace == "" || key == "" {
			continue
		}
		if nameEnglish == "" {
			nameEnglish = nameHebrew
		}
		if nameEnglish == "" {
			continue
		}
		if byNamespace[namespace] == nil {
			byNamespace[namespace] = make(map[string]ProductMetafieldDefinitionInput)
		}
		byNamespace[namespace][key] = ProductMetafieldDefinitionInput{
			Namespace:   namespace,
			Key:         key,
			NameEnglish: nameEnglish,
			NameHebrew:  nameHebrew,
		}
	}

	for namespace, definitionMap := range byNamespace {
		existing, err := c.listProductMetafieldDefinitions(ctx, namespace)
		if err != nil {
			return err
		}
		if c.logger != nil {
			c.logger.Log(fmt.Sprintf("Shopify metafield definitions namespace=%s existing=%d incoming=%d", namespace, len(existing), len(definitionMap)))
		}
		existingMap := make(map[string]dto.MetafieldDefinitionNode, len(existing))
		for _, node := range existing {
			existingMap[strings.ToLower(node.Key)] = node
		}

		createdCount := 0
		for key, definition := range definitionMap {
			if node, ok := existingMap[strings.ToLower(key)]; ok {
				if shouldUpdateTranslation(definition.NameEnglish, definition.NameHebrew) {
					if err := c.updateTranslation(ctx, node.ID, "name", definition.NameHebrew); err != nil {
						c.logError("shopify metafield definition translation update failed", err)
					}
				}
				continue
			}

			created, err := c.createProductMetafieldDefinition(ctx, definition)
			if err != nil {
				return err
			}
			if created != nil {
				createdCount++
				if shouldUpdateTranslation(definition.NameEnglish, definition.NameHebrew) {
					if err := c.updateTranslation(ctx, created.ID, "name", definition.NameHebrew); err != nil {
						c.logError("shopify metafield definition translation update failed", err)
					}
				}
			}
		}
		if c.logger != nil {
			c.logger.Log(fmt.Sprintf("Shopify metafield definitions namespace=%s created=%d", namespace, createdCount))
		}
	}

	return nil
}

func (c *Client) listProductMetafieldDefinitions(ctx context.Context, namespace string) ([]dto.MetafieldDefinitionNode, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return nil, errors.New("shopify metafield namespace is required")
	}

	query := `
	query metafieldDefinitions($first: Int!, $after: String, $ownerType: MetafieldOwnerType!, $namespace: String!) {
		metafieldDefinitions(first: $first, after: $after, ownerType: $ownerType, namespace: $namespace) {
			nodes { id name namespace key }
			pageInfo { hasNextPage endCursor }
		}
	}`

	var (
		cursor  string
		results []dto.MetafieldDefinitionNode
	)
	for {
		var data dto.MetafieldDefinitionsQueryData
		variables := map[string]any{
			"first":     metafieldQueryPageSize,
			"ownerType": metafieldOwnerProduct,
			"namespace": namespace,
		}
		if cursor != "" {
			variables["after"] = cursor
		}
		if err := c.graphqlRequest(ctx, query, variables, &data); err != nil {
			return nil, err
		}
		results = append(results, data.MetafieldDefinitions.Nodes...)
		if !data.MetafieldDefinitions.PageInfo.HasNextPage || data.MetafieldDefinitions.PageInfo.EndCursor == "" {
			break
		}
		cursor = data.MetafieldDefinitions.PageInfo.EndCursor
	}

	return results, nil
}

func (c *Client) createProductMetafieldDefinition(ctx context.Context, definition ProductMetafieldDefinitionInput) (*dto.MetafieldDefinitionNode, error) {
	namespace := strings.TrimSpace(definition.Namespace)
	key := strings.TrimSpace(definition.Key)
	name := strings.TrimSpace(definition.NameEnglish)
	if namespace == "" || key == "" || name == "" {
		return nil, nil
	}

	query := `
	mutation metafieldDefinitionCreate($definition: MetafieldDefinitionInput!) {
		metafieldDefinitionCreate(definition: $definition) {
			createdDefinition { id name namespace key }
			userErrors { field message }
		}
	}`

	payload := map[string]any{
		"definition": map[string]any{
			"name":      name,
			"namespace": namespace,
			"key":       key,
			"type":      metafieldTypeText,
			"ownerType": metafieldOwnerProduct,
		},
	}

	var data dto.MetafieldDefinitionCreateData
	if err := c.graphqlRequest(ctx, query, payload, &data); err != nil {
		return nil, err
	}
	if err := userErrorsToError("metafieldDefinitionCreate", data.MetafieldDefinitionCreate.UserErrors); err != nil {
		return nil, err
	}
	return data.MetafieldDefinitionCreate.CreatedDefinition, nil
}

func (c *Client) updateTranslation(ctx context.Context, resourceID string, key string, translatedValue string) error {
	resourceID = strings.TrimSpace(resourceID)
	key = strings.TrimSpace(key)
	translatedValue = strings.TrimSpace(translatedValue)
	if resourceID == "" || key == "" || translatedValue == "" {
		return nil
	}

	translationDigest := c.getTranslationDigest(ctx, resourceID, key)
	if translationDigest == "" {
		return nil
	}

	query := `
	mutation translationsRegister($resourceId: ID!, $translations: [TranslationInput!]!) {
		translationsRegister(resourceId: $resourceId, translations: $translations) {
			userErrors { message field }
		}
	}`

	payload := map[string]any{
		"resourceId": resourceID,
		"translations": []map[string]any{
			{
				"locale":                    "he",
				"key":                       key,
				"value":                     translatedValue,
				"translatableContentDigest": translationDigest,
			},
		},
	}

	var data dto.TranslationsRegisterData
	if err := c.graphqlRequest(ctx, query, payload, &data); err != nil {
		return err
	}
	return userErrorsToError("translationsRegister", data.TranslationsRegister.UserErrors)
}

func (c *Client) getTranslationDigest(ctx context.Context, resourceID string, key string) string {
	resourceID = strings.TrimSpace(resourceID)
	key = strings.TrimSpace(key)
	if resourceID == "" || key == "" {
		return ""
	}

	query := `
	query ($id: ID!) {
		translatableResource(resourceId: $id) {
			resourceId
			translatableContent { key digest locale }
		}
	}`

	var data metafieldTranslationResourceData
	if err := c.graphqlRequest(ctx, query, map[string]any{"id": resourceID}, &data); err != nil {
		return ""
	}
	if data.TranslatableResource == nil {
		return ""
	}
	for _, item := range data.TranslatableResource.TranslatableContent {
		if item.Key == key && item.Locale == "en" {
			return item.Digest
		}
	}
	return ""
}

func metafieldKey(namespace string, key string) string {
	return strings.ToLower(strings.TrimSpace(namespace)) + ":" + strings.ToLower(strings.TrimSpace(key))
}

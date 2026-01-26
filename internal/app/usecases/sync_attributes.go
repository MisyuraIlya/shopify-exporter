package usecases

import (
	"context"
	"fmt"
	"shopify-exporter/internal/adapters/apix"
	"shopify-exporter/internal/adapters/shopify"
	"shopify-exporter/internal/domain/model"
	"shopify-exporter/internal/logging"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

type SyncAttributesService interface {
	Run(ctx context.Context) error
}

type ClientAttribute struct {
	apixClient    apix.AttributeService
	shopifyClient shopify.AttributeService
	logger        logging.LoggerService
}

func NewSyncAttributes(apixClient apix.AttributeService, shopifyClient shopify.AttributeService, logger logging.LoggerService) SyncAttributesService {
	return &ClientAttribute{
		apixClient:    apixClient,
		shopifyClient: shopifyClient,
		logger:        logger,
	}
}

const (
	maxConcurrent              = 4
	metafieldNamespace         = "attributes"
	metafieldKeyMaxLength      = 30
	metafieldKeyFallbackPrefix = "attr_"
)

func (c *ClientAttribute) Run(ctx context.Context) error {
	if c.logger != nil {
		c.logger.Log("Attribute sync started")
	}

	attributes, err := c.apixClient.AttributesList(ctx)
	if err != nil {
		if c.logger != nil {
			c.logger.LogError("Error fetch api attributes", err)
		}
		return err
	}

	attributeProducts, err := c.apixClient.AttributeProductList(ctx)
	if err != nil {
		if c.logger != nil {
			c.logger.LogError("Error fetch api attribute products", err)
		}
		return err
	}

	if c.logger != nil {
		c.logger.Log(fmt.Sprintf(
			"Attribute sync fetched attributes=%d attribute_products=%d",
			len(attributes),
			len(attributeProducts),
		))
	}

	attributeMap := make(map[int]model.Attribute, len(attributes))
	for _, attribute := range attributes {
		if attribute.ID == 0 {
			continue
		}
		attributeMap[attribute.ID] = attribute
	}
	attributeKeys := buildAttributeKeys(attributes)
	definitions := buildMetafieldDefinitions(attributes, attributeKeys)
	if len(definitions) > 0 {
		if err := c.shopifyClient.EnsureProductMetafieldDefinitions(ctx, definitions); err != nil {
			if c.logger != nil {
				c.logger.LogError("Error ensure metafield definitions", err)
			}
			return err
		}
	}

	fieldsBySKU := make(map[string]map[string]shopify.ProductMetafieldInput)
	skippedEmptySKU := 0
	skippedMissingAttribute := 0
	skippedEmptyValue := 0
	skippedInvalidKey := 0

	for _, item := range attributeProducts {
		sku := strings.TrimSpace(item.Sku)
		if sku == "" {
			skippedEmptySKU++
			continue
		}

		if _, ok := attributeMap[item.AttributeID]; !ok {
			skippedMissingAttribute++
			continue
		}

		key := attributeKeys[item.AttributeID]
		if key == "" {
			skippedInvalidKey++
			continue
		}

		englishValue := strings.TrimSpace(item.ValueEnglish)
		hebrewValue := strings.TrimSpace(item.ValueHebrew)
		if englishValue == "" {
			englishValue = hebrewValue
		}
		if englishValue == "" {
			skippedEmptyValue++
			continue
		}

		skuFields := fieldsBySKU[sku]
		if skuFields == nil {
			skuFields = make(map[string]shopify.ProductMetafieldInput)
			fieldsBySKU[sku] = skuFields
		}

		skuFields[key] = shopify.ProductMetafieldInput{
			Namespace:    metafieldNamespace,
			Key:          key,
			ValueEnglish: englishValue,
			ValueHebrew:  hebrewValue,
		}
	}

	if len(fieldsBySKU) == 0 {
		if c.logger != nil {
			c.logger.LogWarning("Attribute sync skipped: no fields to sync")
		}
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, maxConcurrent)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	var synced atomic.Int64

	for sku, fieldMap := range fieldsBySKU {
		sku := sku
		keys := make([]string, 0, len(fieldMap))
		for key := range fieldMap {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		fields := make([]shopify.ProductMetafieldInput, 0, len(keys))
		for _, key := range keys {
			fields = append(fields, fieldMap[key])
		}
		if len(fields) == 0 {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			if err := c.shopifyClient.UpsertProductMetafields(ctx, sku, fields); err != nil {
				if c.logger != nil {
					c.logger.LogError("Error sync attributes", err)
				}
				select {
				case errCh <- err:
					cancel()
				default:
				}
				return
			}
			synced.Add(1)
		}()
	}

	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
	}

	if c.logger != nil {
		c.logger.LogSuccess(fmt.Sprintf(
			"Attribute sync completed products=%d synced=%d skipped_empty_sku=%d skipped_missing_attribute=%d skipped_empty_value=%d skipped_invalid_key=%d",
			len(fieldsBySKU),
			synced.Load(),
			skippedEmptySKU,
			skippedMissingAttribute,
			skippedEmptyValue,
			skippedInvalidKey,
		))
	}

	return nil
}

func buildAttributeKeys(attributes []model.Attribute) map[int]string {
	keys := make(map[int]string, len(attributes))
	used := make(map[string]int, len(attributes))

	for _, attribute := range attributes {
		if attribute.ID == 0 {
			continue
		}
		base := slugifyMetafieldKey(attribute.EnglishName)
		if base == "" {
			base = fmt.Sprintf("%s%d", metafieldKeyFallbackPrefix, attribute.ID)
		}
		key := trimMetafieldKey(base)
		if existingID, ok := used[key]; ok && existingID != attribute.ID {
			key = trimMetafieldKey(fmt.Sprintf("%s%d", metafieldKeyFallbackPrefix, attribute.ID))
		}
		used[key] = attribute.ID
		keys[attribute.ID] = key
	}

	return keys
}

func buildMetafieldDefinitions(attributes []model.Attribute, attributeKeys map[int]string) []shopify.ProductMetafieldDefinitionInput {
	definitions := make([]shopify.ProductMetafieldDefinitionInput, 0, len(attributes))
	seen := make(map[string]struct{}, len(attributes))
	for _, attribute := range attributes {
		if attribute.ID == 0 {
			continue
		}
		key := attributeKeys[attribute.ID]
		if key == "" {
			continue
		}
		englishName := strings.TrimSpace(attribute.EnglishName)
		hebrewName := strings.TrimSpace(attribute.HebrewName)
		if englishName == "" {
			englishName = hebrewName
		}
		if englishName == "" {
			continue
		}
		seenKey := strings.ToLower(key)
		if _, ok := seen[seenKey]; ok {
			continue
		}
		seen[seenKey] = struct{}{}
		definitions = append(definitions, shopify.ProductMetafieldDefinitionInput{
			Namespace:   metafieldNamespace,
			Key:         key,
			NameEnglish: englishName,
			NameHebrew:  hebrewName,
		})
	}
	return definitions
}

func slugifyMetafieldKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(value))
	underscore := false
	for _, r := range value {
		isAlpha := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		if isAlpha || isDigit {
			b.WriteRune(r)
			underscore = false
			continue
		}
		if !underscore {
			b.WriteRune('_')
			underscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func trimMetafieldKey(key string) string {
	key = strings.Trim(key, "_")
	if key == "" {
		return key
	}
	if len(key) <= metafieldKeyMaxLength {
		return key
	}
	key = key[:metafieldKeyMaxLength]
	return strings.TrimRight(key, "_")
}

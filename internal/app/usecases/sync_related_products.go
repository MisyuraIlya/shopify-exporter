package usecases

import (
	"context"
	"fmt"
	"shopify-exporter/internal/adapters/apix"
	"shopify-exporter/internal/adapters/shopify"
	"shopify-exporter/internal/logging"
	"strings"
	"sync"
	"sync/atomic"
)

type SyncRelatedProductsService interface {
	Run(ctx context.Context) error
}

type ClientRelatedProducts struct {
	apixClient    apix.RelatedService
	shopifyClient shopify.RelatedService
	logger        logging.LoggerService
}

const relatedConcurrent = 4

func NewSyncRelatedProducts(apixClient apix.RelatedService, shopifyClient shopify.RelatedService, logger logging.LoggerService) SyncRelatedProductsService {
	return &ClientRelatedProducts{
		apixClient:    apixClient,
		shopifyClient: shopifyClient,
		logger:        logger,
	}
}

func (c *ClientRelatedProducts) Run(ctx context.Context) error {
	if c.logger != nil {
		c.logger.Log("Related products sync started")
	}

	relatedItems, err := c.apixClient.RelatedList(ctx)
	if err != nil {
		if c.logger != nil {
			c.logger.LogError("Error fetch api related products", err)
		}
		return err
	}

	if err := c.shopifyClient.EnsureRelatedProductsMetafieldDefinition(ctx); err != nil {
		if c.logger != nil {
			c.logger.LogError("Error ensure related products metafield definition", err)
		}
		return err
	}

	relatedBySKU := make(map[string][]string, len(relatedItems))
	skippedEmptySKU := 0
	for _, item := range relatedItems {
		sku := strings.TrimSpace(item.Sku)
		if sku == "" {
			skippedEmptySKU++
			continue
		}

		seen := make(map[string]struct{}, len(item.Similar))
		filtered := make([]string, 0, len(item.Similar))
		for _, similarSKU := range item.Similar {
			trimmed := strings.TrimSpace(similarSKU)
			if trimmed == "" || strings.EqualFold(trimmed, sku) {
				continue
			}
			normalized := strings.ToLower(trimmed)
			if _, ok := seen[normalized]; ok {
				continue
			}
			seen[normalized] = struct{}{}
			filtered = append(filtered, trimmed)
		}

		relatedBySKU[sku] = filtered
	}

	if len(relatedBySKU) == 0 {
		if c.logger != nil {
			c.logger.LogWarning("Related products sync skipped: no valid SKU rows")
		}
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, relatedConcurrent)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	var synced atomic.Int64

	for sku, relatedSKUs := range relatedBySKU {
		sku := sku
		relatedSKUs := relatedSKUs
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			if err := c.shopifyClient.UpsertRelatedProductsBySKU(ctx, sku, relatedSKUs); err != nil {
				if c.logger != nil {
					c.logger.LogError("Error sync related products", err)
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
			"Related products sync completed rows=%d synced=%d skipped_empty_sku=%d",
			len(relatedBySKU),
			synced.Load(),
			skippedEmptySKU,
		))
	}

	return nil
}

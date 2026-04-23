package usecases

import (
	"context"
	"fmt"
	"shopify-exporter/internal/adapters/apix"
	"shopify-exporter/internal/adapters/shopify"
	"shopify-exporter/internal/debugsync"
	"shopify-exporter/internal/logging"
	"strings"
)

type SyncPricesService interface {
	Run(ctx context.Context) error
}

type ClientPrice struct {
	apixClient    apix.PriceService
	shopifyClient shopify.PriceService
	logger        logging.LoggerService
}

func NewSyncPrices(apixClient apix.PriceService, shopifyClient shopify.PriceService, logger logging.LoggerService) SyncPricesService {
	return &ClientPrice{
		apixClient:    apixClient,
		shopifyClient: shopifyClient,
		logger:        logger,
	}
}

func (c *ClientPrice) Run(ctx context.Context) error {
	if c.logger != nil {
		c.logger.Log("Price sync started")
	}

	prices, err := c.apixClient.PriceList(ctx)
	if err != nil {
		if c.logger != nil {
			c.logger.LogError("Error fetch api prices", err)
		}
		return err
	}

	type skuPrices struct {
		USD     float64
		ILS     float64
		HasUSD  bool
		HasILS  bool
		SkuTrim string
	}

	priceMap := make(map[string]*skuPrices)
	filteredOut := 0
	for _, price := range prices {
		sku := strings.TrimSpace(price.Sku)
		if sku == "" {
			continue
		}
		if !debugsync.ShouldProcessSKU(sku) {
			filteredOut++
			continue
		}
		entry := priceMap[sku]
		if entry == nil {
			entry = &skuPrices{SkuTrim: sku}
			priceMap[sku] = entry
		}
		switch strings.ToUpper(strings.TrimSpace(price.Currency)) {
		case "USD":
			if c.logger != nil && debugsync.MatchSKU(sku) {
				c.logger.Log(fmt.Sprintf(
					"trace price candidate sku=%s currency=USD selected=%.2f overwrite=%t previous=%.2f",
					sku,
					float64(price.Price),
					entry.HasUSD,
					entry.USD,
				))
			}
			entry.USD = float64(price.Price)
			entry.HasUSD = true
		case "ILS":
			if c.logger != nil && debugsync.MatchSKU(sku) {
				c.logger.Log(fmt.Sprintf(
					"trace price candidate sku=%s currency=ILS selected=%.2f overwrite=%t previous=%.2f",
					sku,
					float64(price.Price),
					entry.HasILS,
					entry.ILS,
				))
			}
			entry.ILS = float64(price.Price)
			entry.HasILS = true
		}
	}

	inputs := make([]shopify.PriceUpsertInput, 0, len(priceMap))
	missingBoth := 0
	for _, entry := range priceMap {
		if !entry.HasUSD || !entry.HasILS {
			if c.logger != nil && debugsync.MatchSKU(entry.SkuTrim) {
				c.logger.Log(fmt.Sprintf(
					"trace price skipped sku=%s has_usd=%t usd=%.2f has_ils=%t ils=%.2f",
					entry.SkuTrim,
					entry.HasUSD,
					entry.USD,
					entry.HasILS,
					entry.ILS,
				))
			}
			missingBoth++
			continue
		}
		if c.logger != nil && debugsync.MatchSKU(entry.SkuTrim) {
			c.logger.Log(fmt.Sprintf(
				"trace price prepared sku=%s usd=%.2f ils=%.2f",
				entry.SkuTrim,
				entry.USD,
				entry.ILS,
			))
		}
		inputs = append(inputs, shopify.PriceUpsertInput{
			SKU:      entry.SkuTrim,
			USDPrice: entry.USD,
			ILSPrice: entry.ILS,
		})
	}

	if len(inputs) == 0 {
		if c.logger != nil {
			c.logger.LogWarning("Price sync skipped: no SKUs with both USD and ILS prices")
		}
		return nil
	}

	if _, err := c.shopifyClient.EnsureIsraelMarketAndCatalog(ctx); err != nil {
		if c.logger != nil {
			c.logger.LogError("Error ensure Israel market", err)
		}
		return err
	}

	if err := c.shopifyClient.UpsertPricesBatch(ctx, inputs); err != nil {
		if c.logger != nil {
			c.logger.LogError("Error sync prices", err)
		}
		return err
	}

	if c.logger != nil {
		c.logger.LogSuccess(fmt.Sprintf(
			"Price sync completed sku=%d skipped_missing=%d filtered_out=%d",
			len(inputs),
			missingBoth,
			filteredOut,
		))
	}

	return nil
}

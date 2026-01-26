package usecases

import (
	"context"
	"fmt"
	"shopify-exporter/internal/adapters/apix"
	"shopify-exporter/internal/adapters/shopify"
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
	for _, price := range prices {
		sku := strings.TrimSpace(price.Sku)
		if sku == "" {
			continue
		}
		entry := priceMap[sku]
		if entry == nil {
			entry = &skuPrices{SkuTrim: sku}
			priceMap[sku] = entry
		}
		switch strings.ToUpper(strings.TrimSpace(price.Currency)) {
		case "USD":
			entry.USD = float64(price.Price)
			entry.HasUSD = true
		case "ILS":
			entry.ILS = float64(price.Price)
			entry.HasILS = true
		}
	}

	inputs := make([]shopify.PriceUpsertInput, 0, len(priceMap))
	missingBoth := 0
	for _, entry := range priceMap {
		if !entry.HasUSD || !entry.HasILS {
			missingBoth++
			continue
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
			"Price sync completed sku=%d skipped_missing=%d",
			len(inputs),
			missingBoth,
		))
	}

	return nil
}

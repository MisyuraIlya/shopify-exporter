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
	apixClient     apix.PriceService
	apixProducts   apix.NewClientService
	shopifyClient  shopify.PriceService
	logger         logging.LoggerService
}

const (
	discountCode50Pct       = "5"
	discountProductPageSize = 100
)

func NewSyncPrices(apixClient apix.PriceService, apixProducts apix.NewClientService, shopifyClient shopify.PriceService, logger logging.LoggerService) SyncPricesService {
	return &ClientPrice{
		apixClient:    apixClient,
		apixProducts:  apixProducts,
		shopifyClient: shopifyClient,
		logger:        logger,
	}
}

func discountFraction(code string) float64 {
	switch strings.TrimSpace(code) {
	case discountCode50Pct:
		return 0.5
	}
	return 0
}

func (c *ClientPrice) Run(ctx context.Context) error {
	if c.logger != nil {
		c.logger.Log("Price sync started")
	}

	discountByCode, err := c.fetchDiscountCodes(ctx)
	if err != nil {
		if c.logger != nil {
			c.logger.LogError("Error fetch api products for discounts", err)
		}
		return err
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
		input := shopify.PriceUpsertInput{
			SKU:      entry.SkuTrim,
			USDPrice: entry.USD,
			ILSPrice: entry.ILS,
		}
		if frac := discountFraction(discountByCode[entry.SkuTrim]); frac > 0 {
			input.USDCompareAt = entry.USD
			input.ILSCompareAt = entry.ILS
			input.USDPrice = entry.USD * (1 - frac)
			input.ILSPrice = entry.ILS * (1 - frac)
			if c.logger != nil && debugsync.MatchSKU(entry.SkuTrim) {
				c.logger.Log(fmt.Sprintf(
					"trace price discount sku=%s code=%s fraction=%.2f usd_price=%.2f usd_compare_at=%.2f ils_price=%.2f ils_compare_at=%.2f",
					entry.SkuTrim,
					discountByCode[entry.SkuTrim],
					frac,
					input.USDPrice,
					input.USDCompareAt,
					input.ILSPrice,
					input.ILSCompareAt,
				))
			}
		}
		inputs = append(inputs, input)
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

func (c *ClientPrice) fetchDiscountCodes(ctx context.Context) (map[string]string, error) {
	codes := make(map[string]string)
	if c.apixProducts == nil {
		return codes, nil
	}

	page := 1
	totalPages := 1
	for page <= totalPages {
		products, pageTotal, err := c.apixProducts.ListProducts(ctx, page, discountProductPageSize)
		if err != nil {
			return nil, err
		}
		if pageTotal > 0 {
			totalPages = pageTotal
		}
		for _, p := range products {
			sku := strings.TrimSpace(p.Sku)
			code := strings.TrimSpace(p.DiscountCode)
			if sku == "" || code == "" {
				continue
			}
			codes[sku] = code
		}
		page++
	}
	return codes, nil
}

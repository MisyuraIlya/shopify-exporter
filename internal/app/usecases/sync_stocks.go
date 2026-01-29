package usecases

import (
	"context"
	"fmt"
	"shopify-exporter/internal/adapters/apix"
	"shopify-exporter/internal/adapters/shopify"
	"shopify-exporter/internal/logging"
	"strings"
	"sync"
)

type SyncStocksService interface {
	Run(ctx context.Context) error
}

type ClientStock struct {
	apixClient    apix.StockService
	shopifyClient shopify.StockService
	logger        logging.LoggerService
}

const (
	stockBatchSize  = 100
	stockConcurrent = 4
)

func NewSyncStocks(apixClient apix.StockService, shopifyClient shopify.StockService, logger logging.LoggerService) SyncStocksService {
	return &ClientStock{
		apixClient:    apixClient,
		shopifyClient: shopifyClient,
		logger:        logger,
	}
}

func (c *ClientStock) Run(ctx context.Context) error {
	if c.logger != nil {
		c.logger.Log("Stock sync started")
	}

	stocks, err := c.apixClient.FetchStocks(ctx)
	if err != nil {
		if c.logger != nil {
			c.logger.LogError("Error fetch api stocks", err)
		}
		return err
	}

	stockBySKU := make(map[string]shopify.StockInput, len(stocks))
	skippedEmptySKU := 0
	skippedNegative := 0
	duplicateSKU := 0

	for _, item := range stocks {
		sku := strings.TrimSpace(item.Sku)
		if sku == "" {
			skippedEmptySKU++
			continue
		}
		if item.Stock < 0 {
			skippedNegative++
			continue
		}
		if _, ok := stockBySKU[sku]; ok {
			duplicateSKU++
		}
		stockBySKU[sku] = shopify.StockInput{
			SKU:      sku,
			Quantity: int(item.Stock),
		}
	}

	if len(stockBySKU) == 0 {
		if c.logger != nil {
			c.logger.LogWarning("Stock sync skipped: no valid SKUs")
		}
		return nil
	}

	inputs := make([]shopify.StockInput, 0, len(stockBySKU))
	for _, item := range stockBySKU {
		inputs = append(inputs, item)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, stockConcurrent)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup

	for start := 0; start < len(inputs); start += stockBatchSize {
		end := start + stockBatchSize
		if end > len(inputs) {
			end = len(inputs)
		}
		batch := inputs[start:end]
		wg.Add(1)
		sem <- struct{}{}
		go func(batch []shopify.StockInput) {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			if err := c.shopifyClient.SetOnHandQuantities(ctx, batch); err != nil {
				if c.logger != nil {
					c.logger.LogError("Error sync stocks", err)
				}
				select {
				case errCh <- err:
					cancel()
				default:
				}
			}
		}(batch)
	}

	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
	}

	if c.logger != nil {
		c.logger.LogSuccess(fmt.Sprintf(
			"Stock sync completed sku=%d skipped_empty_sku=%d skipped_negative=%d duplicates=%d",
			len(inputs),
			skippedEmptySKU,
			skippedNegative,
			duplicateSKU,
		))
	}

	return nil
}

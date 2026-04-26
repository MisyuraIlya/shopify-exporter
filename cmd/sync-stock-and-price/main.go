// Periodic job to sync prices and stock between ApiHasav and Shopify.
package main

import (
	"context"
	"fmt"
	"shopify-exporter/internal/adapters/apix"
	"shopify-exporter/internal/adapters/shopify"
	"shopify-exporter/internal/app/usecases"
	"shopify-exporter/internal/config"
	"shopify-exporter/internal/debugsync"
	infrahttp "shopify-exporter/internal/infra/http"
	"shopify-exporter/internal/logging"
	"time"
)

func main() {
	cfg, err := config.LoadForDailySync()
	if err != nil {
		fmt.Printf("error %v\n", err)
		return
	}

	logger := logging.NewNamedLogger(cfg.TelegramBot, "sync-stock-and-price")
	httpClient := infrahttp.NewClient(maxDuration(cfg.Shopify.Timeout, cfg.ApiHasav.Timeout))

	logger.Log("stock and price sync started")
	if logger != nil && debugsync.HasOnlyStepFilter() {
		logger.Log("sync step filter active via " + debugsync.OnlyStepsEnv)
	}
	if logger != nil && debugsync.HasOnlySKUFilter() {
		logger.Log("sync sku filter active via " + debugsync.OnlySKUsEnv)
	}

	ctx := context.Background()
	shopifyClient := shopify.NewClient(cfg.Shopify, httpClient, logger)

	runStepIfEnabled(logger, "syncPrices", func() error {
		priceClient, ok := shopifyClient.(shopify.PriceService)
		if !ok {
			return fmt.Errorf("shopify price service unavailable")
		}
		apixPriceClient := apix.NewPriceSerivce(cfg.ApiHasav, httpClient, logger)
		return usecases.NewSyncPrices(apixPriceClient, priceClient, logger).Run(ctx)
	})

	runStepIfEnabled(logger, "syncStocks", func() error {
		stockClient, ok := shopifyClient.(shopify.StockService)
		if !ok {
			return fmt.Errorf("shopify stock service unavailable")
		}
		apixStockClient := apix.NewStockService(cfg.ApiHasav, httpClient, logger)
		return usecases.NewSyncStocks(apixStockClient, stockClient, logger).Run(ctx)
	})

	logger.LogSuccess("stock and price sync completed")
}

func maxDuration(a, b time.Duration) time.Duration {
	if a >= b {
		return a
	}
	return b
}

func runStep(logger logging.LoggerService, name string, run func() error) {
	if logger != nil {
		logger.Log(name)
	}
	if err := run(); err != nil && logger != nil {
		logger.LogError(name+" error", err)
	}
}

func runStepIfEnabled(logger logging.LoggerService, name string, run func() error) {
	if !debugsync.ShouldRunStep(name) {
		if logger != nil {
			logger.Log(name + " skipped by " + debugsync.OnlyStepsEnv)
		}
		return
	}
	runStep(logger, name, run)
}

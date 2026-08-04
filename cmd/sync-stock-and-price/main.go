// Periodic job to sync prices and stock between ApiHasav and Shopify.
package main

import (
	"context"
	"fmt"
	"shopify-exporter/internal/adapters/apix"
	"shopify-exporter/internal/adapters/shopify"
	"shopify-exporter/internal/app/reporting"
	"shopify-exporter/internal/app/usecases"
	"shopify-exporter/internal/config"
	"shopify-exporter/internal/debugsync"
	infrahttp "shopify-exporter/internal/infra/http"
	"shopify-exporter/internal/logging"
	"time"
)

func main() {
	startedAt := time.Now()
	cfg, err := config.LoadForDailySync()
	if err != nil {
		fmt.Printf("error %v\n", err)
		return
	}

	logger, logPath := logging.NewNamedLoggerWithPath(cfg.TelegramBot, "sync-stock-and-price")
	httpClient := infrahttp.NewClient(maxDuration(cfg.Shopify.Timeout, cfg.ApiHasav.Timeout))

	// Sent even when a step fails: the inbox is the health signal for this job.
	reporter := reporting.Start("sync-stock-and-price", cfg, logger, startedAt)
	reporter.SetLogFile(logPath)
	defer reporter.Send()

	logger.Log("stock and price sync started")
	if logger != nil && debugsync.HasOnlyStepFilter() {
		logger.Log("sync step filter active via " + debugsync.OnlyStepsEnv)
	}
	if logger != nil && debugsync.HasOnlySKUFilter() {
		logger.Log("sync sku filter active via " + debugsync.OnlySKUsEnv)
	}

	ctx := context.Background()
	shopifyClient := shopify.NewClient(cfg.Shopify, httpClient, logger)
	if aware, ok := shopifyClient.(shopify.ReporterAware); ok {
		aware.SetReporter(reporter.Recorder())
	}

	runStepIfEnabled(logger, reporter, "syncPrices", func() error {
		priceClient, ok := shopifyClient.(shopify.PriceService)
		if !ok {
			return fmt.Errorf("shopify price service unavailable")
		}
		apixPriceClient := apix.NewPriceSerivce(cfg.ApiHasav, httpClient, logger)
		apixProductsClient := apix.NewClient(cfg.ApiHasav, httpClient)
		return usecases.NewSyncPrices(apixPriceClient, apixProductsClient, priceClient, logger).Run(ctx)
	})

	runStepIfEnabled(logger, reporter, "syncStocks", func() error {
		stockClient, ok := shopifyClient.(shopify.StockService)
		if !ok {
			return fmt.Errorf("shopify stock service unavailable")
		}
		apixStockClient := apix.NewStockService(cfg.ApiHasav, httpClient, logger)
		return usecases.NewSyncStocks(apixStockClient, stockClient, logger, cfg.Stock).Run(ctx)
	})

	logger.LogSuccess("stock and price sync completed")
}

func maxDuration(a, b time.Duration) time.Duration {
	if a >= b {
		return a
	}
	return b
}

func runStep(logger logging.LoggerService, reporter *reporting.Reporter, name string, run func() error) {
	if logger != nil {
		logger.Log(name)
	}
	finish := reporter.Step(name)
	err := run()
	finish(err)
	if err != nil && logger != nil {
		logger.LogError(name+" error", err)
	}
}

func runStepIfEnabled(logger logging.LoggerService, reporter *reporting.Reporter, name string, run func() error) {
	if !debugsync.ShouldRunStep(name) {
		if logger != nil {
			logger.Log(name + " skipped by " + debugsync.OnlyStepsEnv)
		}
		reporter.Skip(name, "skipped by "+debugsync.OnlyStepsEnv)
		return
	}
	runStep(logger, reporter, name, run)
}

// every 5 min job take from shopify to api
package main

import (
	"fmt"
	"shopify-exporter/internal/adapters/apix"
	"shopify-exporter/internal/adapters/shopify"
	"shopify-exporter/internal/app/usecases"
	"shopify-exporter/internal/config"
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
	logger := logging.NewLogger(cfg.TelegramBot)

	logger.Log("Docker initialized start work..")

	httpClient := infrahttp.NewClient(maxDuration(cfg.Shopify.Timeout, cfg.ApiHasav.Timeout))

	apixClient := apix.NewClient(cfg.ApiHasav, httpClient)
	shopifyClient := shopify.NewClient(cfg.Shopify, httpClient)
	syncProducts := usecases.NewSyncProducts(apixClient, shopifyClient, logger)

	// if err := syncProducts.Run(context.Background()); err != nil {
	// 	logger.LogError(err.Error())
	// 	os.Exit(1)
	// }

	logger.LogSuccess("sync completed")

	// Example structure for the sync-to-shopify entrypoint:
	//
	// 1) Load config (API X + Shopify credentials, timeouts, pagination limits).
	// 2) Initialize logger/metrics.
	// 3) Build shared HTTP client and retry/backoff helpers.
	// 4) Create adapters:
	//    - API X client
	//    - Shopify client
	// 5) Build pipeline that wires usecases (products, categories, inventory).
	// 6) Run pipeline and handle result (log summary, set exit code on failure).
	//
	// Keep main.go thin: only wiring and execution. All business logic stays in
	// internal/app/usecases and internal/app/pipeline.
}

func maxDuration(a, b time.Duration) time.Duration {
	if a >= b {
		return a
	}
	return b
}

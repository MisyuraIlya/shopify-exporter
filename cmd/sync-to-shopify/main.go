// every 5 min job take from shopify to api
package main

import (
	"context"
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
	logger.Log("httpClient")
	apixClient := apix.NewClient(cfg.ApiHasav, httpClient)
	logger.Log("apixClient")
	shopifyClient := shopify.NewClient(cfg.Shopify, httpClient, logger)
	logger.Log("shopifyClient")
	syncProducts := usecases.NewSyncProducts(apixClient, shopifyClient, logger)
	logger.Log("syncProducts")
	if err := syncProducts.Run(context.Background()); err != nil {
		logger.LogError("sync failed", err)
		return
	}

	logger.LogSuccess("sync completed")
}

func maxDuration(a, b time.Duration) time.Duration {
	if a >= b {
		return a
	}
	return b
}

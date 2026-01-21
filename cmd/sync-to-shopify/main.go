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
	httpClient := infrahttp.NewClient(maxDuration(cfg.Shopify.Timeout, cfg.ApiHasav.Timeout))

	logger.Log("Docker initialized start work..")

	// apixClient := apix.NewClient(cfg.ApiHasav, httpClient)
	// shopifyClient := shopify.NewClient(cfg.Shopify, httpClient, logger)
	// syncProducts := usecases.NewSyncProducts(apixClient, shopifyClient, logger)
	// logger.Log("syncProducts")
	// err = syncProducts.Run(context.Background())
	// if err != nil {
	// 	logger.LogError("syncProducts error", err)
	// }

	apixClientCategory := apix.NewCategoryClientService(cfg.ApiHasav, httpClient, logger)
	shopifyClientCategory := shopify.NewShopifyCategoryService(cfg.Shopify, httpClient, logger)
	shopifyProductClient := shopify.NewClient(cfg.Shopify, httpClient, logger)
	syncCategories := usecases.NewSyncCategories(apixClientCategory, shopifyClientCategory, shopifyProductClient, logger)
	logger.Log("syncCategories")
	err = syncCategories.Run(context.Background())
	if err != nil {
		logger.LogError("syncCategories error", err)
	}

	logger.LogSuccess("sync completed")
}

func maxDuration(a, b time.Duration) time.Duration {
	if a >= b {
		return a
	}
	return b
}

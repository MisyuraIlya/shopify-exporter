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
	shopifyClient := shopify.NewClient(cfg.Shopify, httpClient, logger)
	// syncProducts := usecases.NewSyncProducts(apixClient, shopifyClient, logger)
	// logger.Log("syncProducts")
	// err = syncProducts.Run(context.Background())
	// if err != nil {
	// 	logger.LogError("syncProducts error", err)
	// }

	// apixClientCategory := apix.NewCategoryClientService(cfg.ApiHasav, httpClient, logger)
	// shopifyClientCategory := shopify.NewShopifyCategoryService(cfg.Shopify, httpClient, logger)
	// shopifyProductClient := shopify.NewClient(cfg.Shopify, httpClient, logger)
	// syncCategories := usecases.NewSyncCategories(apixClientCategory, shopifyClientCategory, shopifyProductClient, logger)
	// logger.Log("syncCategories")
	// err = syncCategories.Run(context.Background())
	// if err != nil {
	// 	logger.LogError("syncCategories error", err)
	// }

	// attributeClient, ok := shopifyClient.(shopify.AttributeService)
	// if !ok {
	// 	logger.LogError("syncAttributes error", fmt.Errorf("shopify attribute service unavailable"))
	// } else {
	// 	apixAttributeClient := apix.NewAttributeServiceClient(cfg.ApiHasav, httpClient, logger)
	// 	syncAttributes := usecases.NewSyncAttributes(apixAttributeClient, attributeClient, logger)
	// 	logger.Log("syncAttributes")
	// 	err = syncAttributes.Run(context.Background())
	// 	if err != nil {
	// 		logger.LogError("syncAttributes error", err)
	// 	}
	// }

	// priceClient, ok := shopifyClient.(shopify.PriceService)
	// if !ok {
	// 	logger.LogError("syncPrices error", fmt.Errorf("shopify price service unavailable"))
	// } else {
	// 	apixPriceClient := apix.NewPriceSerivce(cfg.ApiHasav, httpClient, logger)
	// 	syncPrices := usecases.NewSyncPrices(apixPriceClient, priceClient, logger)
	// 	logger.Log("syncPrices")
	// 	err = syncPrices.Run(context.Background())
	// 	if err != nil {
	// 		logger.LogError("syncPrices error", err)
	// 	}
	// }

	stockClient, ok := shopifyClient.(shopify.StockService)
	if !ok {
		logger.LogError("syncStocks error", fmt.Errorf("shopify stock service unavailable"))
	} else {
		apixStockClient := apix.NewStockService(cfg.ApiHasav, httpClient, logger)
		syncStocks := usecases.NewSyncStocks(apixStockClient, stockClient, logger)
		logger.Log("syncStocks")
		err = syncStocks.Run(context.Background())
		if err != nil {
			logger.LogError("syncStocks error", err)
		}
	}

	logger.LogSuccess("sync completed")
}

func maxDuration(a, b time.Duration) time.Duration {
	if a >= b {
		return a
	}
	return b
}

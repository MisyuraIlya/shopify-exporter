// Periodic job to sync data between ApiHasav and Shopify.
package main

import (
	"context"
	"fmt"
	"net/http"
	"shopify-exporter/internal/adapters/apix"
	"shopify-exporter/internal/adapters/shopify"
	"shopify-exporter/internal/app/usecases"
	"shopify-exporter/internal/config"
	infrahttp "shopify-exporter/internal/infra/http"
	"shopify-exporter/internal/logging"
	"strings"
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

	ctx := context.Background()
	shopifyClient := shopify.NewClient(cfg.Shopify, httpClient, logger)
	apixClient := apix.NewClient(cfg.ApiHasav, httpClient)

	runStep(logger, "syncProducts", func() error {
		return usecases.NewSyncProducts(apixClient, shopifyClient, logger).Run(ctx)
	})

	runStep(logger, "syncCategories", func() error {
		apixClientCategory := apix.NewCategoryClientService(cfg.ApiHasav, httpClient, logger)
		shopifyClientCategory := shopify.NewShopifyCategoryService(cfg.Shopify, httpClient, logger)
		return usecases.NewSyncCategories(apixClientCategory, shopifyClientCategory, shopifyClient, logger).Run(ctx)
	})

	runStep(logger, "syncAttributes", func() error {
		attributeClient, ok := shopifyClient.(shopify.AttributeService)
		if !ok {
			return fmt.Errorf("shopify attribute service unavailable")
		}
		apixAttributeClient := apix.NewAttributeServiceClient(cfg.ApiHasav, httpClient, logger)
		return usecases.NewSyncAttributes(apixAttributeClient, attributeClient, logger).Run(ctx)
	})

	runStep(logger, "syncPrices", func() error {
		priceClient, ok := shopifyClient.(shopify.PriceService)
		if !ok {
			return fmt.Errorf("shopify price service unavailable")
		}
		apixPriceClient := apix.NewPriceSerivce(cfg.ApiHasav, httpClient, logger)
		return usecases.NewSyncPrices(apixPriceClient, priceClient, logger).Run(ctx)
	})

	runStep(logger, "syncStocks", func() error {
		stockClient, ok := shopifyClient.(shopify.StockService)
		if !ok {
			return fmt.Errorf("shopify stock service unavailable")
		}
		apixStockClient := apix.NewStockService(cfg.ApiHasav, httpClient, logger)
		return usecases.NewSyncStocks(apixStockClient, stockClient, logger).Run(ctx)
	})

	triggerFileSync(logger, httpClient, cfg.ApiHasav.BaseUrl)

	logger.LogSuccess("sync completed")
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

func triggerFileSync(logger logging.LoggerService, httpClient *http.Client, baseURL string) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		if logger != nil {
			logger.LogWarning("file sync skipped: API_BASE_URL is empty")
		}
		return
	}
	endpoint := baseURL + "/files/shopify/sync"
	if logger != nil {
		logger.Log("file sync trigger: " + endpoint)
	}
	go func() {
		req, err := http.NewRequest(http.MethodPost, endpoint, http.NoBody)
		if err != nil {
			if logger != nil {
				logger.LogError("file sync trigger error", err)
			}
			return
		}
		client := httpClient
		if client == nil {
			client = http.DefaultClient
		}
		resp, err := client.Do(req)
		if err != nil {
			if logger != nil {
				logger.LogError("file sync trigger error", err)
			}
			return
		}
		_ = resp.Body.Close()
	}()
}

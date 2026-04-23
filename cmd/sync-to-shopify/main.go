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
	"shopify-exporter/internal/debugsync"
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
	logger := logging.NewNamedLogger(cfg.TelegramBot, "sync-to-shopify")
	httpClient := infrahttp.NewClient(maxDuration(cfg.Shopify.Timeout, cfg.ApiHasav.Timeout))

	logger.Log("Docker initialized start work..")
	if logger != nil && debugsync.HasOnlyStepFilter() {
		logger.Log("sync step filter active via " + debugsync.OnlyStepsEnv)
	}
	if logger != nil && debugsync.HasOnlySKUFilter() {
		logger.Log("sync sku filter active via " + debugsync.OnlySKUsEnv)
	}

	ctx := context.Background()
	shopifyClient := shopify.NewClient(cfg.Shopify, httpClient, logger)
	apixClient := apix.NewClient(cfg.ApiHasav, httpClient)

	runStepIfEnabled(logger, "syncProducts", func() error {
		return usecases.NewSyncProducts(apixClient, shopifyClient, logger).Run(ctx)
	})

	runStepIfEnabled(logger, "syncCategories", func() error {
		apixClientCategory := apix.NewCategoryClientService(cfg.ApiHasav, httpClient, logger)
		shopifyClientCategory := shopify.NewShopifyCategoryService(cfg.Shopify, httpClient, logger)
		return usecases.NewSyncCategories(apixClientCategory, shopifyClientCategory, shopifyClient, logger).Run(ctx)
	})

	runStepIfEnabled(logger, "syncAttributes", func() error {
		attributeClient, ok := shopifyClient.(shopify.AttributeService)
		if !ok {
			return fmt.Errorf("shopify attribute service unavailable")
		}
		apixAttributeClient := apix.NewAttributeServiceClient(cfg.ApiHasav, httpClient, logger)
		return usecases.NewSyncAttributes(apixAttributeClient, attributeClient, logger).Run(ctx)
	})

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

	runStepIfEnabled(logger, "syncRelatedProducts", func() error {
		relatedClient, ok := shopifyClient.(shopify.RelatedService)
		if !ok {
			return fmt.Errorf("shopify related service unavailable")
		}
		apixRelatedClient := apix.NewRellated(cfg.ApiHasav, httpClient, logger)
		return usecases.NewSyncRelatedProducts(apixRelatedClient, relatedClient, logger).Run(ctx)
	})

	runStepIfEnabled(logger, "syncProductsOrder", func() error {
		orderClient, ok := shopifyClient.(shopify.ProductOrderService)
		if !ok {
			return fmt.Errorf("shopify product order service unavailable")
		}
		apixOrderClient := apix.NewProductOrder(cfg.ApiHasav, httpClient, logger)
		return usecases.NewSyncProductsOrder(apixOrderClient, orderClient, logger).Run(ctx)
	})

	if debugsync.ShouldRunStep("fileSync") {
		triggerFileSync(logger, httpClient, cfg.ApiHasav.BaseUrl)
	} else if logger != nil {
		logger.Log("fileSync skipped by " + debugsync.OnlyStepsEnv)
	}

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

func runStepIfEnabled(logger logging.LoggerService, name string, run func() error) {
	if !debugsync.ShouldRunStep(name) {
		if logger != nil {
			logger.Log(name + " skipped by " + debugsync.OnlyStepsEnv)
		}
		return
	}
	runStep(logger, name, run)
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

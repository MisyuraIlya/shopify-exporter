package main

import (
	"context"
	"fmt"
	"shopify-exporter/internal/adapters/shopify"
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
	httpClient := infrahttp.NewClient(cfg.Shopify.Timeout)

	logger.Log("wipe shopify started")
	logger.Log(fmt.Sprintf("wipe shopify timeout=%s", cfg.Shopify.Timeout))

	shopifyClient := shopify.NewClient(cfg.Shopify, httpClient, logger)
	wipeClient, ok := shopifyClient.(shopify.WipeService)
	if !ok {
		logger.LogError("wipe shopify error", fmt.Errorf("shopify wipe service unavailable"))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if err := wipeClient.WipeAll(ctx); err != nil {
		logger.LogError("wipe shopify error", err)
		return
	}

	logger.LogSuccess("wipe shopify completed")
}

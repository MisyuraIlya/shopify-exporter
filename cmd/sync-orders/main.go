// Daily jon fetcher from api to shopify
package main

import (
	"shopify-exporter/internal/config"
	"shopify-exporter/internal/logging"
)

func main() {
	cfg, err := config.LoadForDailySync()
	if err != nil {
		return
	}
	logging.NewLogger(cfg.TelegramBot)

}

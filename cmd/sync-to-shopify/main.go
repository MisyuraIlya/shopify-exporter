// every 5 min job take from shopify to api
package main

import (
	"fmt"
	"shopify-exporter/internal/config"
	"shopify-exporter/internal/logging"
)

func main() {
	cfg, err := config.LoadForSyncOrder()
	if err != nil {
		fmt.Printf("error %v\n", err)
		return
	}
	logger := logging.NewLogger(cfg.TelegramBot)

	logger.Log("Docker initialized start work..")

}

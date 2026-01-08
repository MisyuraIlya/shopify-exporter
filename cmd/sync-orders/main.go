// Daily jon fetcher from api to shopify
package main

import (
	"shopify-exporter/internal/config"
)

func main() {
	cfg, err := config.LoadForDailySync()
	if err != nil {
		return
	}

}

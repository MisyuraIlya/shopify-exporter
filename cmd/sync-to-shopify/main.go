// every 5 min job take from shopify to api
package main

import (
	"shopify-exporter/internal/config"
	"shopify-exporter/internal/infra/mysql"
)

func main() {
	cfg, err := config.LoadForSyncOrder()
	if err != nil {
		return
	}
	db, err := mysql.New(cfg.Mysql)

	if err != nil {
		return
	}

}

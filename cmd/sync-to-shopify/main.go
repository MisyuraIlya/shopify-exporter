// every 5 min job take from shopify to api
package main

import "shopify-exporter/internal/config"

func main() {
	cfg, err := config.LoadForSyncOrder()
}

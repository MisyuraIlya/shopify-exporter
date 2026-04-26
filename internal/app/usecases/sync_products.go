package usecases

import (
	"context"
	"fmt"
	"shopify-exporter/internal/adapters/apix"
	"shopify-exporter/internal/adapters/shopify"
	"shopify-exporter/internal/domain/model"
	"shopify-exporter/internal/logging"
	"strings"
	"sync"
	"sync/atomic"
)

type SyncProductsService interface {
	Run(ctx context.Context) error
}

type Client struct {
	apixClient    apix.NewClientService
	shopifyClient shopify.NewClientService
	logger        logging.LoggerService
}

func NewSyncProducts(apixClient apix.NewClientService, shopifyClient shopify.NewClientService, logger logging.LoggerService) SyncProductsService {
	return &Client{
		apixClient:    apixClient,
		shopifyClient: shopifyClient,
		logger:        logger,
	}
}

func (c *Client) Run(ctx context.Context) error {
	const pageSize = 100
	const maxConcurrent = 4
	c.logger.Log(fmt.Sprintf("Product sync started limit=%d", pageSize))

	page := 1
	totalPages := 1
	var (
		createdProducts     atomic.Int64
		updatedProducts     atomic.Int64
		localizationUpdates atomic.Int64
		failedProducts      atomic.Int64
		skippedEmptySKU     atomic.Int64
		skippedEmptyTitle   atomic.Int64
	)

	for page <= totalPages {
		apiProducts, pageTotal, err := c.apixClient.ListProducts(ctx, page, pageSize)
		if err != nil {
			c.logger.LogError("Error fetch api products", err)
			return err
		}
		if pageTotal > 0 {
			totalPages = pageTotal
		}
		c.logger.Log(fmt.Sprintf("Product sync page=%d/%d fetched=%d limit=%d", page, totalPages, len(apiProducts), pageSize))

		sem := make(chan struct{}, maxConcurrent)
		var wg sync.WaitGroup
		for _, v := range apiProducts {
			product := v
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				sku := strings.TrimSpace(product.Sku)
				if sku == "" {
					skippedEmptySKU.Add(1)
					c.logger.LogWarning("Product skipped: empty SKU")
					return
				}

				productTitle := productSyncTitle(product)
				if productTitle == "" {
					skippedEmptyTitle.Add(1)
					c.logger.LogWarning(fmt.Sprintf("Product skipped: empty title sku=%s", sku))
					return
				}

				productExists, productGid, err := c.shopifyClient.CheckExistProductBySku(ctx, product)
				if err != nil {
					failedProducts.Add(1)
					c.logger.LogError(fmt.Sprintf("Product lookup failed sku=%s", sku), err)
					return
				}

				if productExists {
					if err := c.shopifyClient.UpdateProduct(ctx, product, productGid); err == nil {
						updatedProducts.Add(1)
						// c.logger.LogSuccess(fmt.Sprintf("Product updated sku=%s title=%s", v.Sku, productTitle))
					} else {
						failedProducts.Add(1)
						c.logger.LogError(fmt.Sprintf("Product update failed sku=%s title=%s", sku, productTitle), err)
					}
				} else {
					createdGid, err := c.shopifyClient.CreateProduct(ctx, product)
					if err != nil {
						failedProducts.Add(1)
						c.logger.LogError(fmt.Sprintf("Product create failed sku=%s title=%s", sku, productTitle), err)
					} else {
						createdProducts.Add(1)
						// c.logger.LogSuccess(fmt.Sprintf("Product created sku=%s title=%s", v.Sku, productTitle))
					}
					productGid = createdGid
				}

				if strings.TrimSpace(productGid) == "" {
					return
				}

				if err := c.shopifyClient.UpdateLocalization(ctx, product, productGid); err == nil {
					// c.logger.LogSuccess(fmt.Sprintf("Product localization updated sku=%s title=%s", v.Sku, productTitle))
					localizationUpdates.Add(1)
				} else {
					failedProducts.Add(1)
					c.logger.LogError(fmt.Sprintf("Product localization failed sku=%s title=%s", sku, productTitle), err)
				}
			}()
		}
		wg.Wait()

		page++
	}

	summary := fmt.Sprintf(
		"Product sync completed pages=%d created=%d updated=%d localization_updates=%d failed=%d skipped_empty_sku=%d skipped_empty_title=%d",
		totalPages,
		createdProducts.Load(),
		updatedProducts.Load(),
		localizationUpdates.Load(),
		failedProducts.Load(),
		skippedEmptySKU.Load(),
		skippedEmptyTitle.Load(),
	)
	if failedProducts.Load() > 0 {
		c.logger.LogWarning(summary)
	} else {
		c.logger.LogSuccess(summary)
	}

	return nil
}

func productSyncTitle(product model.Product) string {
	if title := strings.TrimSpace(product.EnglishTitle); title != "" {
		return title
	}
	return strings.TrimSpace(product.HebrewTitle)
}

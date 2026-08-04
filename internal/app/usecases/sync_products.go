package usecases

import (
	"context"
	"fmt"
	"shopify-exporter/internal/adapters/apix"
	"shopify-exporter/internal/adapters/shopify"
	"shopify-exporter/internal/domain/model"
	"shopify-exporter/internal/logging"
	"shopify-exporter/internal/report"
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
	recorder      report.Recorder
}

func NewSyncProducts(apixClient apix.NewClientService, shopifyClient shopify.NewClientService, logger logging.LoggerService, recorder report.Recorder) SyncProductsService {
	return &Client{
		apixClient:    apixClient,
		shopifyClient: shopifyClient,
		logger:        logger,
		recorder:      recorder,
	}
}

func (c *Client) recordCreated(sku, title string) {
	if c.recorder != nil {
		c.recorder.ProductCreated(sku, title)
	}
}

func (c *Client) recordUpdated(sku string) {
	if c.recorder != nil {
		c.recorder.ProductUpdated(sku)
	}
}

func (c *Client) recordFailed(sku, title string, err error) {
	if c.recorder != nil {
		c.recorder.ProductFailed(sku, title, err)
	}
}

func (c *Client) recordWarning(message string) {
	if c.recorder != nil {
		c.recorder.Warn("products", message)
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
					c.recordWarning("product skipped: empty SKU")
					return
				}

				productTitle := productSyncTitle(product)
				if productTitle == "" {
					skippedEmptyTitle.Add(1)
					c.logger.LogWarning(fmt.Sprintf("Product skipped: empty title sku=%s", sku))
					c.recordWarning(fmt.Sprintf("product skipped: empty title sku=%s", sku))
					return
				}

				productExists, productGid, err := c.shopifyClient.CheckExistProductBySku(ctx, product)
				if err != nil {
					failedProducts.Add(1)
					c.logger.LogError(fmt.Sprintf("Product lookup failed sku=%s", sku), err)
					c.recordFailed(sku, productTitle, fmt.Errorf("lookup failed: %w", err))
					return
				}

				if productExists {
					if err := c.shopifyClient.UpdateProduct(ctx, product, productGid); err == nil {
						updatedProducts.Add(1)
						// Counted, not listed: every existing product is re-pushed on
						// every run, so a per-SKU list would just be the catalogue.
						c.recordUpdated(sku)
					} else {
						failedProducts.Add(1)
						c.logger.LogError(fmt.Sprintf("Product update failed sku=%s title=%s", sku, productTitle), err)
						c.recordFailed(sku, productTitle, fmt.Errorf("update failed: %w", err))
					}
				} else {
					createdGid, err := c.shopifyClient.CreateProduct(ctx, product)
					if err != nil {
						failedProducts.Add(1)
						c.logger.LogError(fmt.Sprintf("Product create failed sku=%s title=%s", sku, productTitle), err)
						c.recordFailed(sku, productTitle, fmt.Errorf("create failed: %w", err))
					} else {
						createdProducts.Add(1)
						c.recordCreated(sku, productTitle)
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
					c.recordFailed(sku, productTitle, fmt.Errorf("localization failed: %w", err))
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

	if c.recorder != nil {
		c.recorder.Incr("products", "pages", int64(totalPages))
		c.recorder.Incr("products", "created", createdProducts.Load())
		c.recorder.Incr("products", "reexported", updatedProducts.Load())
		c.recorder.Incr("products", "localization_updates", localizationUpdates.Load())
		c.recorder.Incr("products", "failed", failedProducts.Load())
		c.recorder.Incr("products", "skipped_empty_sku", skippedEmptySKU.Load())
		c.recorder.Incr("products", "skipped_empty_title", skippedEmptyTitle.Load())
	}

	return nil
}

func productSyncTitle(product model.Product) string {
	if title := strings.TrimSpace(product.EnglishTitle); title != "" {
		return title
	}
	return strings.TrimSpace(product.HebrewTitle)
}

package usecases

import (
	"context"
	"fmt"
	"shopify-exporter/internal/adapters/apix"
	"shopify-exporter/internal/adapters/shopify"
	"shopify-exporter/internal/logging"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

type SyncProductsOrderService interface {
	Run(ctx context.Context) error
}

type ClientProductsOrder struct {
	apixClient    apix.ProductOrderService
	shopifyClient shopify.ProductOrderService
	logger        logging.LoggerService
}

const productOrderConcurrent = 4

func NewSyncProductsOrder(apixClient apix.ProductOrderService, shopifyClient shopify.ProductOrderService, logger logging.LoggerService) SyncProductsOrderService {
	return &ClientProductsOrder{
		apixClient:    apixClient,
		shopifyClient: shopifyClient,
		logger:        logger,
	}
}

type orderedSku struct {
	sku   string
	order int
}

func (c *ClientProductsOrder) Run(ctx context.Context) error {
	if c.logger != nil {
		c.logger.Log("Products order sync started")
	}

	productsOrder, err := c.apixClient.ProductsOrderList(ctx)
	if err != nil {
		if c.logger != nil {
			c.logger.LogError("Error fetch api products order", err)
		}
		return err
	}

	perCategory := make(map[string]map[string]int)
	skippedEmptySKU := 0
	skippedEmptyCategory := 0

	for _, item := range productsOrder {
		sku := strings.TrimSpace(item.Sku)
		if sku == "" {
			skippedEmptySKU++
			continue
		}
		for _, category := range item.Categories {
			categoryTitle := strings.TrimSpace(category.CategoryEnglish)
			if categoryTitle == "" {
				categoryTitle = strings.TrimSpace(category.CategoryValue)
			}
			if categoryTitle == "" {
				skippedEmptyCategory++
				continue
			}
			if perCategory[categoryTitle] == nil {
				perCategory[categoryTitle] = make(map[string]int)
			}
			existingOrder, exists := perCategory[categoryTitle][sku]
			if !exists || category.OrderNumber < existingOrder {
				perCategory[categoryTitle][sku] = category.OrderNumber
			}
		}
	}

	if len(perCategory) == 0 {
		if c.logger != nil {
			c.logger.LogWarning("Products order sync skipped: no valid category rows")
		}
		return nil
	}

	categories := make([]string, 0, len(perCategory))
	for category := range perCategory {
		categories = append(categories, category)
	}
	sort.Strings(categories)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, productOrderConcurrent)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	var synced atomic.Int64

	for _, category := range categories {
		category := category
		entries := make([]orderedSku, 0, len(perCategory[category]))
		for sku, order := range perCategory[category] {
			entries = append(entries, orderedSku{sku: sku, order: order})
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].order == entries[j].order {
				return entries[i].sku < entries[j].sku
			}
			return entries[i].order < entries[j].order
		})

		orderItems := make([]shopify.CollectionOrderItem, 0, len(entries))
		for _, entry := range entries {
			orderItems = append(orderItems, shopify.CollectionOrderItem{
				SKU:         entry.sku,
				OrderNumber: entry.order,
			})
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(category string, orderItems []shopify.CollectionOrderItem) {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			if err := c.shopifyClient.ReorderCollectionProductsByCategory(ctx, category, orderItems); err != nil {
				if c.logger != nil {
					c.logger.LogError("Error sync category order", err)
				}
				select {
				case errCh <- err:
					cancel()
				default:
				}
				return
			}
			synced.Add(1)
		}(category, orderItems)
	}

	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
	}

	if c.logger != nil {
		c.logger.LogSuccess(fmt.Sprintf(
			"Products order sync completed categories=%d synced=%d skipped_empty_sku=%d skipped_empty_category=%d",
			len(perCategory),
			synced.Load(),
			skippedEmptySKU,
			skippedEmptyCategory,
		))
	}
	return nil
}

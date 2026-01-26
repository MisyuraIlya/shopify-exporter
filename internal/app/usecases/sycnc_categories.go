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

type SyncCategoriesService interface {
	Run(ctx context.Context) error
}

type ClientCategory struct {
	apixClient    apix.CategoryService
	shopifyClient shopify.ShopifyCategoryService
	productClient shopify.NewClientService
	logger        logging.LoggerService
}

func NewSyncCategories(apixClient apix.CategoryService, shopifyClient shopify.ShopifyCategoryService, productClient shopify.NewClientService, logger logging.LoggerService) SyncCategoriesService {
	return &ClientCategory{
		apixClient:    apixClient,
		shopifyClient: shopifyClient,
		productClient: productClient,
		logger:        logger,
	}
}

func (c *ClientCategory) Run(ctx context.Context) error {
	const maxCategoryConcurrent = 4
	const maxAttachConcurrent = 4

	c.logger.Log("Category sync started")
	caetgories, err := c.apixClient.CategoryList(ctx)
	if err != nil {
		c.logger.LogError("Error fetch api categories", err)
		return err
	}

	c.logger.Log(fmt.Sprintf("Category sync fetched products=%d", len(caetgories)))

	var (
		totalCategories   int
		skippedEmptyTitle int
	)
	var (
		createdCategories atomic.Int64
		updatedCategories atomic.Int64
	)
	processedCategories := make(map[string]struct{})
	uniqueCategories := make([]model.Category, 0)
	productsWithSKU := make([]model.ProductCategories, 0, len(caetgories))

	for i, v := range caetgories {
		if i%200 == 0 {
			c.logger.Log(fmt.Sprintf("Category sync progress=%d/%d", i+1, len(caetgories)))
		}
		if len(v.Categproes) == 0 {
			continue
		}
		for _, v2 := range v.Categproes {
			totalCategories++
			title := strings.TrimSpace(v2.TitlteEnglish)
			if title == "" {
				title = strings.TrimSpace(v2.TitleHebrew)
			}
			if title == "" {
				skippedEmptyTitle++
				continue
			}
			key := strings.ToLower(title)
			if _, ok := processedCategories[key]; ok {
				continue
			}
			processedCategories[key] = struct{}{}
			uniqueCategories = append(uniqueCategories, v2)
		}
		if strings.TrimSpace(v.SKU) != "" {
			productsWithSKU = append(productsWithSKU, v)
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)
	sem := make(chan struct{}, maxCategoryConcurrent)
	var wg sync.WaitGroup
	for _, category := range uniqueCategories {
		category := category
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			existCategory, err := c.shopifyClient.CheckCategoryExist(ctx, category)
			if err != nil {
				c.logger.LogError("Error category exists", err)
				select {
				case errCh <- err:
					cancel()
				default:
				}
				return
			}
			if existCategory {
				updatedCategories.Add(1)
				c.shopifyClient.UpdateCategory(ctx, category)
			} else {
				createdCategories.Add(1)
				c.shopifyClient.CreateCategory(ctx, category)
			}
		}()
	}
	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
	}

	if len(productsWithSKU) > 0 {
		attachSem := make(chan struct{}, maxAttachConcurrent)
		var attachWG sync.WaitGroup
		for _, v := range productsWithSKU {
			productCategories := v
			attachWG.Add(1)
			attachSem <- struct{}{}
			go func() {
				defer attachWG.Done()
				defer func() { <-attachSem }()
				if ctx.Err() != nil {
					return
				}
				c.productClient.AttachCategoryToProduct(ctx, productCategories)
			}()
		}
		attachWG.Wait()
	}

	c.logger.LogSuccess(fmt.Sprintf(
		"Category sync completed products=%d categories=%d unique=%d created=%d updated=%d skipped_empty=%d",
		len(caetgories),
		totalCategories,
		len(processedCategories),
		createdCategories.Load(),
		updatedCategories.Load(),
		skippedEmptyTitle,
	))
	return nil
}

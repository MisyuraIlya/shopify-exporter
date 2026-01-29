package shopify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"shopify-exporter/internal/adapters/shopify/dto"
)

type WipeService interface {
	WipeAll(ctx context.Context) error
}

const (
	wipePageSize = 50
	productDeleteConcurrency = 5
)

type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage,omitempty"`
	EndCursor   string `json:"endCursor,omitempty"`
}

type productsQueryData struct {
	Products struct {
		Nodes []struct {
			ID    string `json:"id,omitempty"`
			Title string `json:"title,omitempty"`
		} `json:"nodes,omitempty"`
		PageInfo pageInfo `json:"pageInfo,omitempty"`
	} `json:"products"`
}

type collectionsQueryData struct {
	Collections struct {
		Nodes []struct {
			ID    string `json:"id,omitempty"`
			Title string `json:"title,omitempty"`
		} `json:"nodes,omitempty"`
		PageInfo pageInfo `json:"pageInfo,omitempty"`
	} `json:"collections"`
}

type catalogListData struct {
	Catalogs struct {
		Nodes    []dto.CatalogNode `json:"nodes,omitempty"`
		PageInfo pageInfo          `json:"pageInfo,omitempty"`
	} `json:"catalogs"`
}

type priceListListData struct {
	PriceLists struct {
		Nodes    []dto.PriceListNode `json:"nodes,omitempty"`
		PageInfo pageInfo            `json:"pageInfo,omitempty"`
	} `json:"priceLists"`
}

type productDeleteData struct {
	ProductDelete struct {
		DeletedProductID string                 `json:"deletedProductId,omitempty"`
		UserErrors       []dto.ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"productDelete"`
}

type collectionDeleteData struct {
	CollectionDelete struct {
		DeletedCollectionID string                 `json:"deletedCollectionId,omitempty"`
		UserErrors          []dto.ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"collectionDelete"`
}

type metafieldDefinitionDeleteData struct {
	MetafieldDefinitionDelete struct {
		DeletedDefinitionID string                 `json:"deletedDefinitionId,omitempty"`
		UserErrors          []dto.ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"metafieldDefinitionDelete"`
}

type catalogDeleteData struct {
	CatalogDelete struct {
		DeletedID string                 `json:"deletedId,omitempty"`
		UserErrors       []dto.ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"catalogDelete"`
}

type priceListDeleteData struct {
	PriceListDelete struct {
		DeletedID string                 `json:"deletedId,omitempty"`
		UserErrors         []dto.ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"priceListDelete"`
}

type marketDeleteData struct {
	MarketDelete struct {
		DeletedID  string                 `json:"deletedId,omitempty"`
		UserErrors      []dto.ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"marketDelete"`
}

func (c *Client) WipeAll(ctx context.Context) error {
	if c == nil {
		return errors.New("shopify client is nil")
	}

	type wipeStep struct {
		name string
		run  func(context.Context) error
	}

	steps := []wipeStep{
		{name: "products", run: func(ctx context.Context) error { return c.deleteAllProducts(ctx) }},
		{name: "collections", run: func(ctx context.Context) error { return c.deleteAllCollections(ctx) }},
		{name: "metafield definitions", run: func(ctx context.Context) error { return c.deleteAllMetafieldDefinitions(ctx) }},
		{name: "price lists", run: func(ctx context.Context) error { return c.deleteAllPriceLists(ctx) }},
		{name: "catalogs", run: func(ctx context.Context) error { return c.deleteAllCatalogs(ctx) }},
		{name: "markets", run: func(ctx context.Context) error { return c.deleteAllMarkets(ctx) }},
	}

	var (
		firstErr error
		errCh    = make(chan error, len(steps))
	)

	for _, step := range steps {
		step := step
		go func() {
			c.logInfo(fmt.Sprintf("shopify wipe: deleting %s", step.name))
			errCh <- step.run(ctx)
		}()
	}

	for i := 0; i < len(steps); i++ {
		if err := <-errCh; err != nil {
			if firstErr == nil {
				firstErr = err
			}
			c.logError("shopify wipe error", err)
		}
	}

	if firstErr == nil {
		c.logSuccess("shopify wipe completed")
	}

	return firstErr
}

func (c *Client) deleteAllProducts(ctx context.Context) error {
	query := `
	query products($first: Int!, $after: String) {
		products(first: $first, after: $after) {
			nodes { id title }
			pageInfo { hasNextPage endCursor }
		}
	}`

	deleteQuery := `
	mutation productDelete($input: ProductDeleteInput!) {
		productDelete(input: $input) {
			deletedProductId
			userErrors { field message }
		}
	}`

	var deleted atomic.Int64
	after := ""
	for {
		var data productsQueryData
		variables := map[string]any{"first": wipePageSize}
		if after != "" {
			variables["after"] = after
		}
		if err := c.graphqlRequest(ctx, query, variables, &data); err != nil {
			if strings.Contains(err.Error(), "Access denied for metafieldDefinitionDelete field") {
				c.logWarning("shopify wipe metafield definitions: access denied, skipping")
				return nil
			}
			return err
		}
		pageCount := len(data.Products.Nodes)
		if pageCount == 0 {
			c.logInfo("shopify wipe products: no products returned")
		} else {
			c.logInfo(fmt.Sprintf("shopify wipe products: page size=%d deleted_total=%d", pageCount, deleted.Load()))
		}

		if pageCount > 0 {
			ctxPage, cancel := context.WithCancel(ctx)
			var wg sync.WaitGroup
			sem := make(chan struct{}, productDeleteConcurrency)
			errCh := make(chan error, 1)

			for _, node := range data.Products.Nodes {
				id := strings.TrimSpace(node.ID)
				if id == "" {
					continue
				}
				wg.Add(1)
				sem <- struct{}{}
				go func(productID string) {
					defer wg.Done()
					defer func() { <-sem }()
					if ctxPage.Err() != nil {
						return
					}
					var resp productDeleteData
					if err := c.graphqlRequest(ctxPage, deleteQuery, map[string]any{
						"input": map[string]any{"id": productID},
					}, &resp); err != nil {
						select {
						case errCh <- err:
							cancel()
						default:
						}
						return
					}
					if err := userErrorsToDetailedError("productDelete", resp.ProductDelete.UserErrors); err != nil {
						select {
						case errCh <- err:
							cancel()
						default:
						}
						return
					}
					deleted.Add(1)
				}(id)
			}

			wg.Wait()
			cancel()
			select {
			case err := <-errCh:
				return err
			default:
			}
		}

		if !data.Products.PageInfo.HasNextPage || strings.TrimSpace(data.Products.PageInfo.EndCursor) == "" {
			break
		}
		after = data.Products.PageInfo.EndCursor
	}

	c.logSuccess(fmt.Sprintf("shopify products deleted=%d", deleted.Load()))
	return nil
}

func (c *Client) deleteAllCollections(ctx context.Context) error {
	query := `
	query collections($first: Int!, $after: String) {
		collections(first: $first, after: $after) {
			nodes { id title }
			pageInfo { hasNextPage endCursor }
		}
	}`

	deleteQuery := `
	mutation collectionDelete($input: CollectionDeleteInput!) {
		collectionDelete(input: $input) {
			deletedCollectionId
			userErrors { field message }
		}
	}`

	deleted := 0
	after := ""
	for {
		var data collectionsQueryData
		variables := map[string]any{"first": wipePageSize}
		if after != "" {
			variables["after"] = after
		}
		if err := c.graphqlRequest(ctx, query, variables, &data); err != nil {
			return err
		}
		pageCount := len(data.Collections.Nodes)
		if pageCount == 0 {
			c.logInfo("shopify wipe collections: no collections returned")
		} else {
			c.logInfo(fmt.Sprintf("shopify wipe collections: page size=%d deleted_total=%d", pageCount, deleted))
		}
		for _, node := range data.Collections.Nodes {
			id := strings.TrimSpace(node.ID)
			if id == "" {
				continue
			}
			var resp collectionDeleteData
			if err := c.graphqlRequest(ctx, deleteQuery, map[string]any{
				"input": map[string]any{"id": id},
			}, &resp); err != nil {
				return err
			}
			if err := userErrorsToDetailedError("collectionDelete", resp.CollectionDelete.UserErrors); err != nil {
				return err
			}
			deleted++
		}
		if !data.Collections.PageInfo.HasNextPage || strings.TrimSpace(data.Collections.PageInfo.EndCursor) == "" {
			break
		}
		after = data.Collections.PageInfo.EndCursor
	}

	c.logSuccess(fmt.Sprintf("shopify collections deleted=%d", deleted))
	return nil
}

func (c *Client) deleteAllMetafieldDefinitions(ctx context.Context) error {
	ctx = context.WithValue(ctx, suppressGraphQLErrorsKey{}, true)
	query := `
	query metafieldDefinitions($first: Int!, $after: String, $ownerType: MetafieldOwnerType!) {
		metafieldDefinitions(first: $first, after: $after, ownerType: $ownerType) {
			nodes { id name namespace key }
			pageInfo { hasNextPage endCursor }
		}
	}`

	deleteQuery := `
	mutation metafieldDefinitionDelete($id: ID!, $deleteAllAssociatedMetafields: Boolean) {
		metafieldDefinitionDelete(id: $id, deleteAllAssociatedMetafields: $deleteAllAssociatedMetafields) {
			deletedDefinitionId
			userErrors { field message }
		}
	}`

	deleted := 0
	after := ""
	for {
		var data dto.MetafieldDefinitionsQueryData
		variables := map[string]any{
			"first":     wipePageSize,
			"ownerType": "PRODUCT",
		}
		if after != "" {
			variables["after"] = after
		}
		if err := c.graphqlRequest(ctx, query, variables, &data); err != nil {
			return err
		}
		pageCount := len(data.MetafieldDefinitions.Nodes)
		if pageCount == 0 {
			c.logInfo("shopify wipe metafield definitions: no definitions returned")
		} else {
			c.logInfo(fmt.Sprintf("shopify wipe metafield definitions: page size=%d deleted_total=%d", pageCount, deleted))
		}
		for _, node := range data.MetafieldDefinitions.Nodes {
			id := strings.TrimSpace(node.ID)
			if id == "" {
				continue
			}
			var resp metafieldDefinitionDeleteData
			if err := c.graphqlRequest(ctx, deleteQuery, map[string]any{
				"id":                            id,
				"deleteAllAssociatedMetafields": true,
			}, &resp); err != nil {
				if strings.Contains(err.Error(), "Access denied for metafieldDefinitionDelete field") {
					c.logWarning("shopify wipe metafield definitions: access denied, skipping")
					return nil
				}
				return err
			}
			if err := userErrorsToDetailedError("metafieldDefinitionDelete", resp.MetafieldDefinitionDelete.UserErrors); err != nil {
				return err
			}
			deleted++
		}
		if !data.MetafieldDefinitions.PageInfo.HasNextPage || strings.TrimSpace(data.MetafieldDefinitions.PageInfo.EndCursor) == "" {
			break
		}
		after = data.MetafieldDefinitions.PageInfo.EndCursor
	}

	c.logSuccess(fmt.Sprintf("shopify metafield definitions deleted=%d", deleted))
	return nil
}

func (c *Client) deleteAllCatalogs(ctx context.Context) error {
	query := `
	query catalogs($first: Int!, $after: String) {
		catalogs(first: $first, after: $after) {
			nodes { id title status }
			pageInfo { hasNextPage endCursor }
		}
	}`

	deleteQuery := `
	mutation catalogDelete($id: ID!) {
		catalogDelete(id: $id) {
			deletedId
			userErrors { field message }
		}
	}`

	deleted := 0
	after := ""
	for {
		var data catalogListData
		variables := map[string]any{"first": wipePageSize}
		if after != "" {
			variables["after"] = after
		}
		if err := c.graphqlRequest(ctx, query, variables, &data); err != nil {
			return err
		}
		pageCount := len(data.Catalogs.Nodes)
		if pageCount == 0 {
			c.logInfo("shopify wipe catalogs: no catalogs returned")
		} else {
			c.logInfo(fmt.Sprintf("shopify wipe catalogs: page size=%d deleted_total=%d", pageCount, deleted))
		}
		for _, node := range data.Catalogs.Nodes {
			id := strings.TrimSpace(node.ID)
			if id == "" {
				continue
			}
			var resp catalogDeleteData
			if err := c.graphqlRequest(ctx, deleteQuery, map[string]any{
				"id": id,
			}, &resp); err != nil {
				return err
			}
			if err := userErrorsToDetailedError("catalogDelete", resp.CatalogDelete.UserErrors); err != nil {
				if strings.Contains(err.Error(), "Cannot delete a catalog for an app") {
					c.logWarning(fmt.Sprintf("shopify wipe catalogs: skipping app catalog id=%s", id))
					continue
				}
				return err
			}
			deleted++
		}
		if !data.Catalogs.PageInfo.HasNextPage || strings.TrimSpace(data.Catalogs.PageInfo.EndCursor) == "" {
			break
		}
		after = data.Catalogs.PageInfo.EndCursor
	}

	c.logSuccess(fmt.Sprintf("shopify catalogs deleted=%d", deleted))
	return nil
}

func (c *Client) deleteAllPriceLists(ctx context.Context) error {
	query := `
	query priceLists($first: Int!, $after: String) {
		priceLists(first: $first, after: $after) {
			nodes { id name currency }
			pageInfo { hasNextPage endCursor }
		}
	}`

	deleteQuery := `
	mutation priceListDelete($id: ID!) {
		priceListDelete(id: $id) {
			deletedId
			userErrors { field message }
		}
	}`

	deleted := 0
	after := ""
	for {
		var data priceListListData
		variables := map[string]any{"first": wipePageSize}
		if after != "" {
			variables["after"] = after
		}
		if err := c.graphqlRequest(ctx, query, variables, &data); err != nil {
			return err
		}
		pageCount := len(data.PriceLists.Nodes)
		if pageCount == 0 {
			c.logInfo("shopify wipe price lists: no price lists returned")
		} else {
			c.logInfo(fmt.Sprintf("shopify wipe price lists: page size=%d deleted_total=%d", pageCount, deleted))
		}
		for _, node := range data.PriceLists.Nodes {
			id := strings.TrimSpace(node.ID)
			if id == "" {
				continue
			}
			var resp priceListDeleteData
			if err := c.graphqlRequest(ctx, deleteQuery, map[string]any{
				"id": id,
			}, &resp); err != nil {
				return err
			}
			if err := userErrorsToDetailedError("priceListDelete", resp.PriceListDelete.UserErrors); err != nil {
				return err
			}
			deleted++
		}
		if !data.PriceLists.PageInfo.HasNextPage || strings.TrimSpace(data.PriceLists.PageInfo.EndCursor) == "" {
			break
		}
		after = data.PriceLists.PageInfo.EndCursor
	}

	c.logSuccess(fmt.Sprintf("shopify price lists deleted=%d", deleted))
	return nil
}

func (c *Client) deleteAllMarkets(ctx context.Context) error {
	markets, err := c.listMarkets(ctx)
	if err != nil {
		return err
	}

	deleteQuery := `
	mutation marketDelete($id: ID!) {
		marketDelete(id: $id) {
			deletedId
			userErrors { field message }
		}
	}`

	deleted := 0
	for _, market := range markets {
		if deleted == 0 {
			c.logInfo(fmt.Sprintf("shopify wipe markets: total=%d", len(markets)))
		}
		id := strings.TrimSpace(market.ID)
		if id == "" {
			continue
		}
		var resp marketDeleteData
		if err := c.graphqlRequest(ctx, deleteQuery, map[string]any{
			"id": id,
		}, &resp); err != nil {
			return err
		}
		if err := userErrorsToDetailedError("marketDelete", resp.MarketDelete.UserErrors); err != nil {
			if strings.Contains(err.Error(), "last region market") {
				c.logWarning(fmt.Sprintf("shopify wipe markets: skipping last region market id=%s", id))
				continue
			}
			return err
		}
		deleted++
	}

	c.logSuccess(fmt.Sprintf("shopify markets deleted=%d", deleted))
	return nil
}

func (c *Client) logInfo(message string) {
	if c == nil || c.logger == nil || strings.TrimSpace(message) == "" {
		return
	}
	c.logger.Log(message)
}

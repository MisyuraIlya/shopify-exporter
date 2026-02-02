package shopify

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"shopify-exporter/internal/adapters/shopify/dto"
)

const (
	currencyUSD = "USD"
	currencyILS = "ILS"

	israelMarketHandle = "il"
	israelMarketName   = "Israel"
	israelCatalogTitle = "Israel Catalog"
	israelPriceList    = "Israel ILS"

	marketRegionIL         = "IL"
	maxFixedPriceBatchSize = 250
	maxVariantsBatchSize   = 250
)

type PriceService interface {
	EnsureIsraelMarketAndCatalog(ctx context.Context) (IsraelMarketResources, error)
	UpsertPrices(ctx context.Context, input PriceUpsertInput) error
	UpsertPricesBatch(ctx context.Context, inputs []PriceUpsertInput) error
}

type PriceUpsertInput struct {
	SKU       string
	ProductID string
	VariantID string
	USDPrice  float64
	ILSPrice  float64
}

type IsraelMarketResources struct {
	MarketID      string
	CatalogID     string
	PublicationID string
	PriceListID   string
}

type userErrorDetail struct {
	Field   string
	Message string
}

type userErrorsError struct {
	Action string
	Errors []userErrorDetail
}

func (e *userErrorsError) Error() string {
	if e == nil {
		return "shopify user errors"
	}
	parts := make([]string, 0, len(e.Errors))
	for _, err := range e.Errors {
		field := strings.TrimSpace(err.Field)
		message := strings.TrimSpace(err.Message)
		if field == "" {
			parts = append(parts, message)
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", field, message))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("shopify %s failed with user errors", e.Action)
	}
	return fmt.Sprintf("shopify %s failed: %s", e.Action, strings.Join(parts, "; "))
}

type variantNotFoundError struct {
	SKU string
}

func (e *variantNotFoundError) Error() string {
	sku := strings.TrimSpace(e.SKU)
	if sku == "" {
		return "shopify variant not found"
	}
	return fmt.Sprintf("shopify variant not found for sku %s", sku)
}

func isVariantNotFoundError(err error) (*variantNotFoundError, bool) {
	if err == nil {
		return nil, false
	}
	var typed *variantNotFoundError
	if errors.As(err, &typed) {
		return typed, true
	}
	return nil, false
}

func (c *Client) EnsureIsraelMarketAndCatalog(ctx context.Context) (IsraelMarketResources, error) {
	if c == nil {
		return IsraelMarketResources{}, errors.New("shopify client is nil")
	}
	if cached, ok := c.getPriceCache(); ok {
		return cached, nil
	}

	resources, err := c.ensureIsraelMarketAndCatalog(ctx)
	if err != nil {
		return IsraelMarketResources{}, err
	}
	c.setPriceCache(resources)
	return resources, nil
}

func (c *Client) UpsertPrices(ctx context.Context, input PriceUpsertInput) error {
	return c.UpsertPricesBatch(ctx, []PriceUpsertInput{input})
}

func (c *Client) UpsertPricesBatch(ctx context.Context, inputs []PriceUpsertInput) error {
	if len(inputs) == 0 {
		return nil
	}
	resources, err := c.EnsureIsraelMarketAndCatalog(ctx)
	if err != nil {
		return err
	}

	skippedMissing := 0
	resolved := make([]resolvedPriceInput, 0, len(inputs))
	for _, input := range inputs {
		if err := validatePriceInput(input); err != nil {
			return err
		}
		item, err := c.resolvePriceInput(ctx, input)
		if err != nil {
			if missing, ok := isVariantNotFoundError(err); ok {
				skippedMissing++
				c.logWarning(missing.Error())
				continue
			}
			return err
		}
		resolved = append(resolved, item)
	}

	if len(resolved) == 0 {
		if skippedMissing > 0 {
			c.logWarning(fmt.Sprintf("price sync skipped: missing variants=%d", skippedMissing))
		}
		return nil
	}

	if err := c.updateBaseUSDPrices(ctx, resolved); err != nil {
		return err
	}
	if err := c.addFixedILSPrices(ctx, resources.PriceListID, resolved); err != nil {
		return err
	}

	c.logSuccess(fmt.Sprintf("shopify prices updated variants=%d skipped_missing=%d", len(resolved), skippedMissing))
	return nil
}

func (c *Client) ensureIsraelMarketAndCatalog(ctx context.Context) (IsraelMarketResources, error) {
	market, err := c.findIsraelMarket(ctx)
	if err != nil {
		return IsraelMarketResources{}, err
	}
	if market.ID == "" {
		market, err = c.createIsraelMarket(ctx)
		if err != nil {
			return IsraelMarketResources{}, err
		}
		c.logSuccess(fmt.Sprintf("shopify market created id=%s handle=%s", market.ID, market.Handle))
	} else {
		c.logSuccess(fmt.Sprintf("shopify market found id=%s handle=%s", market.ID, market.Handle))
	}

	if !strings.EqualFold(market.CurrencyCode, currencyILS) || market.LocalCurrencies {
		if err := c.updateMarketCurrencySettings(ctx, market.ID); err != nil {
			return IsraelMarketResources{}, err
		}
		market.CurrencyCode = currencyILS
		market.LocalCurrencies = false
		c.logSuccess(fmt.Sprintf("shopify market currency updated id=%s currency=%s", market.ID, currencyILS))
	}
	if !market.Enabled {
		c.logWarning(fmt.Sprintf("shopify market disabled id=%s", market.ID))
	}

	catalog, err := c.findCatalogByTitle(ctx, israelCatalogTitle)
	if err != nil {
		return IsraelMarketResources{}, err
	}
	if catalog.ID == "" {
		catalog, err = c.createCatalog(ctx, israelCatalogTitle, market.ID)
		if err != nil {
			return IsraelMarketResources{}, err
		}
		c.logSuccess(fmt.Sprintf("shopify catalog created id=%s title=%s", catalog.ID, catalog.Title))
	} else {
		c.logSuccess(fmt.Sprintf("shopify catalog found id=%s title=%s", catalog.ID, catalog.Title))
	}

	attached, err := c.marketHasCatalog(ctx, market.ID, catalog.ID)
	if err != nil {
		return IsraelMarketResources{}, err
	}
	if !attached {
		if err := c.addCatalogToMarket(ctx, market.ID, catalog.ID); err != nil {
			return IsraelMarketResources{}, err
		}
		c.logSuccess(fmt.Sprintf("shopify market catalog attached market=%s catalog=%s", market.ID, catalog.ID))
	}
	attached, err = c.marketHasCatalog(ctx, market.ID, catalog.ID)
	if err != nil {
		return IsraelMarketResources{}, err
	}
	if !attached {
		return IsraelMarketResources{}, fmt.Errorf("shopify market %s missing catalog %s", market.ID, catalog.ID)
	}

	publication, priceList, err := c.ensureCatalogPublicationAndPriceList(ctx, catalog.ID)
	if err != nil {
		return IsraelMarketResources{}, err
	}
	if publication.ID != "" {
		c.logSuccess(fmt.Sprintf("shopify publication ready id=%s", publication.ID))
	}
	if priceList.ID != "" {
		c.logSuccess(fmt.Sprintf("shopify price list ready id=%s currency=%s", priceList.ID, priceList.Currency))
	}

	resources := IsraelMarketResources{
		MarketID:      market.ID,
		CatalogID:     catalog.ID,
		PublicationID: publication.ID,
		PriceListID:   priceList.ID,
	}

	if err := c.verifyIsraelMarketSetup(ctx, resources); err != nil {
		return IsraelMarketResources{}, err
	}

	return resources, nil
}

func (c *Client) listMarkets(ctx context.Context) ([]dto.MarketNode, error) {
	query := `
	query markets($first: Int!, $after: String) {
		markets(first: $first, after: $after) {
			nodes {
				id
				name
				handle
				enabled
				currencySettings {
					baseCurrency { currencyCode }
					localCurrencies
				}
				regions(first: 250) {
					nodes {
						... on MarketRegionCountry { code }
					}
				}
			}
			pageInfo { hasNextPage endCursor }
		}
	}`

	markets := make([]dto.MarketNode, 0)
	after := ""
	for {
		variables := map[string]any{
			"first": 50,
		}
		if after != "" {
			variables["after"] = after
		}
		var data dto.MarketsQueryData
		err := c.graphqlRequest(ctx, query, variables, &data)
		if err != nil {
			return nil, err
		}
		markets = append(markets, data.Markets.Nodes...)
		if !data.Markets.PageInfo.HasNextPage {
			break
		}
		after = data.Markets.PageInfo.EndCursor
		if strings.TrimSpace(after) == "" {
			break
		}
	}
	return markets, nil
}

func (c *Client) findIsraelMarket(ctx context.Context) (marketInfo, error) {
	markets, err := c.listMarkets(ctx)
	if err != nil {
		return marketInfo{}, err
	}
	var found marketInfo
	for _, market := range markets {
		if !marketHasCountry(market, marketRegionIL) {
			continue
		}
		if found.ID != "" {
			c.logWarning(fmt.Sprintf("multiple markets include %s, keeping id=%s", marketRegionIL, found.ID))
			break
		}
		found = marketInfo{
			ID:              strings.TrimSpace(market.ID),
			Name:            strings.TrimSpace(market.Name),
			Handle:          strings.TrimSpace(market.Handle),
			Enabled:         market.Enabled,
			CurrencyCode:    strings.TrimSpace(market.CurrencySettings.BaseCurrency.CurrencyCode),
			LocalCurrencies: market.CurrencySettings.LocalCurrencies,
		}
	}
	return found, nil
}

func marketHasCountry(market dto.MarketNode, countryCode string) bool {
	countryCode = strings.TrimSpace(countryCode)
	for _, region := range market.Regions.Nodes {
		if strings.EqualFold(strings.TrimSpace(region.Code), countryCode) {
			return true
		}
	}
	return false
}

func (c *Client) createIsraelMarket(ctx context.Context) (marketInfo, error) {
	query := `
	mutation marketCreate($input: MarketCreateInput!) {
		marketCreate(input: $input) {
			market {
				id
				name
				handle
				enabled
				currencySettings {
					baseCurrency { currencyCode }
					localCurrencies
				}
			}
			userErrors { field message }
		}
	}`

	input := map[string]any{
		"name":   israelMarketName,
		"handle": israelMarketHandle,
		"regionsCondition": map[string]any{
			"countryCodes": []string{marketRegionIL},
		},
		"currencySettings": map[string]any{
			"baseCurrency":    currencyILS,
			"localCurrencies": false,
		},
	}

	var data dto.MarketCreateData
	if err := c.graphqlRequest(ctx, query, map[string]any{"input": input}, &data); err != nil {
		return marketInfo{}, err
	}
	if err := userErrorsToDetailedError("marketCreate", data.MarketCreate.UserErrors); err != nil {
		return marketInfo{}, err
	}
	if data.MarketCreate.Market == nil || strings.TrimSpace(data.MarketCreate.Market.ID) == "" {
		return marketInfo{}, errors.New("shopify market create returned empty id")
	}
	market := data.MarketCreate.Market
	return marketInfo{
		ID:              strings.TrimSpace(market.ID),
		Name:            strings.TrimSpace(market.Name),
		Handle:          strings.TrimSpace(market.Handle),
		Enabled:         market.Enabled,
		CurrencyCode:    strings.TrimSpace(market.CurrencySettings.BaseCurrency.CurrencyCode),
		LocalCurrencies: market.CurrencySettings.LocalCurrencies,
	}, nil
}

func (c *Client) updateMarketCurrencySettings(ctx context.Context, marketID string) error {
	query := `
	mutation marketUpdate($id: ID!, $input: MarketUpdateInput!) {
		marketUpdate(id: $id, input: $input) {
			market { id }
			userErrors { field message }
		}
	}`

	input := map[string]any{
		"currencySettings": map[string]any{
			"baseCurrency":    currencyILS,
			"localCurrencies": false,
		},
	}

	var data dto.MarketUpdateData
	if err := c.graphqlRequest(ctx, query, map[string]any{
		"id":    marketID,
		"input": input,
	}, &data); err != nil {
		return err
	}
	return userErrorsToDetailedError("marketUpdate", data.MarketUpdate.UserErrors)
}

func (c *Client) findCatalogByTitle(ctx context.Context, title string) (dto.CatalogNode, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return dto.CatalogNode{}, errors.New("shopify catalog title is required")
	}

	query := `
	query catalogs($first: Int!, $query: String!) {
		catalogs(first: $first, query: $query) {
			nodes { id title status }
		}
	}`

	var data dto.CatalogsQueryData
	err := c.graphqlRequest(ctx, query, map[string]any{
		"first": 5,
		"query": buildSearchQuery("title", title),
	}, &data)
	if err != nil {
		return dto.CatalogNode{}, err
	}
	if len(data.Catalogs.Nodes) == 0 {
		return dto.CatalogNode{}, nil
	}
	return data.Catalogs.Nodes[0], nil
}

func (c *Client) createCatalog(ctx context.Context, title string, marketID string) (dto.CatalogNode, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return dto.CatalogNode{}, errors.New("shopify catalog title is required")
	}
	marketID = strings.TrimSpace(marketID)
	if marketID == "" {
		return dto.CatalogNode{}, errors.New("shopify market id is required for catalog")
	}

	query := `
	mutation catalogCreate($input: CatalogCreateInput!) {
		catalogCreate(input: $input) {
			catalog { id title status }
			userErrors { field message }
		}
	}`

	input := map[string]any{
		"title":  title,
		"status": "ACTIVE",
		"context": map[string]any{
			"marketIds": []string{marketID},
		},
	}

	var data dto.CatalogCreateData
	if err := c.graphqlRequest(ctx, query, map[string]any{"input": input}, &data); err != nil {
		return dto.CatalogNode{}, err
	}
	if err := userErrorsToDetailedError("catalogCreate", data.CatalogCreate.UserErrors); err != nil {
		return dto.CatalogNode{}, err
	}
	if data.CatalogCreate.Catalog == nil || strings.TrimSpace(data.CatalogCreate.Catalog.ID) == "" {
		return dto.CatalogNode{}, errors.New("shopify catalog create returned empty id")
	}
	return *data.CatalogCreate.Catalog, nil
}

func (c *Client) marketHasCatalog(ctx context.Context, marketID string, catalogID string) (bool, error) {
	marketID = strings.TrimSpace(marketID)
	catalogID = strings.TrimSpace(catalogID)
	if marketID == "" || catalogID == "" {
		return false, errors.New("shopify market and catalog ids are required")
	}

	query := `
	query market($id: ID!, $first: Int!, $after: String) {
		market(id: $id) {
			id
			catalogs(first: $first, after: $after) {
				nodes { id title }
				pageInfo { hasNextPage endCursor }
			}
		}
	}`

	after := ""
	for {
		var data dto.MarketCatalogsData
		variables := map[string]any{
			"id":    marketID,
			"first": 50,
		}
		if after != "" {
			variables["after"] = after
		}
		if err := c.graphqlRequest(ctx, query, variables, &data); err != nil {
			return false, err
		}
		if data.Market == nil {
			return false, nil
		}
		for _, catalog := range data.Market.Catalogs.Nodes {
			if strings.EqualFold(strings.TrimSpace(catalog.ID), catalogID) {
				return true, nil
			}
		}
		if !data.Market.Catalogs.PageInfo.HasNextPage {
			break
		}
		after = data.Market.Catalogs.PageInfo.EndCursor
		if strings.TrimSpace(after) == "" {
			break
		}
	}
	return false, nil
}

func (c *Client) addCatalogToMarket(ctx context.Context, marketID, catalogID string) error {
	marketID = strings.TrimSpace(marketID)
	catalogID = strings.TrimSpace(catalogID)
	if marketID == "" || catalogID == "" {
		return errors.New("shopify market and catalog ids are required")
	}

	query := `
	mutation marketUpdate($id: ID!, $input: MarketUpdateInput!) {
		marketUpdate(id: $id, input: $input) {
			market { id }
			userErrors { field message }
		}
	}`

	input := map[string]any{
		"catalogsToAdd": []string{catalogID},
	}

	var data dto.MarketUpdateData
	if err := c.graphqlRequest(ctx, query, map[string]any{
		"id":    marketID,
		"input": input,
	}, &data); err != nil {
		return err
	}
	return userErrorsToDetailedError("marketUpdate", data.MarketUpdate.UserErrors)
}

func (c *Client) getCatalogDetails(ctx context.Context, catalogID string) (dto.PublicationNode, dto.PriceListNode, error) {
	catalogID = strings.TrimSpace(catalogID)
	if catalogID == "" {
		return dto.PublicationNode{}, dto.PriceListNode{}, errors.New("shopify catalog id is required")
	}

	query := `
	query catalog($id: ID!) {
		catalog(id: $id) {
			id
			title
			publication { id autoPublish }
			priceList { id name currency }
		}
	}`

	var data dto.CatalogDetailsData
	if err := c.graphqlRequest(ctx, query, map[string]any{"id": catalogID}, &data); err != nil {
		return dto.PublicationNode{}, dto.PriceListNode{}, err
	}
	if data.Catalog == nil {
		return dto.PublicationNode{}, dto.PriceListNode{}, errors.New("shopify catalog not found")
	}

	var publication dto.PublicationNode
	if data.Catalog.Publication != nil {
		publication = *data.Catalog.Publication
	}
	var priceList dto.PriceListNode
	if data.Catalog.PriceList != nil {
		priceList = *data.Catalog.PriceList
	}

	return publication, priceList, nil
}

func (c *Client) ensureCatalogPublicationAndPriceList(ctx context.Context, catalogID string) (dto.PublicationNode, dto.PriceListNode, error) {
	publication, priceList, err := c.getCatalogDetails(ctx, catalogID)
	if err != nil {
		return dto.PublicationNode{}, dto.PriceListNode{}, err
	}

	if publication.ID == "" {
		publication, err = c.createCatalogPublication(ctx, catalogID)
		if err != nil {
			return dto.PublicationNode{}, dto.PriceListNode{}, err
		}
	} else if !publication.AutoPublish {
		publication, err = c.updatePublicationAutoPublish(ctx, publication.ID)
		if err != nil {
			return dto.PublicationNode{}, dto.PriceListNode{}, err
		}
	}

	if priceList.ID == "" || !strings.EqualFold(strings.TrimSpace(priceList.Currency), currencyILS) {
		priceList, err = c.createPriceList(ctx, catalogID)
		if err != nil {
			return dto.PublicationNode{}, dto.PriceListNode{}, err
		}
	}

	return publication, priceList, nil
}

func (c *Client) createCatalogPublication(ctx context.Context, catalogID string) (dto.PublicationNode, error) {
	query := `
	mutation publicationCreate($input: PublicationCreateInput!) {
		publicationCreate(input: $input) {
			publication { id autoPublish }
			userErrors { field message }
		}
	}`

	input := map[string]any{
		"catalogId":    catalogID,
		"defaultState": "ALL_PRODUCTS",
		"autoPublish":  true,
	}

	var data dto.PublicationCreateData
	if err := c.graphqlRequest(ctx, query, map[string]any{"input": input}, &data); err != nil {
		return dto.PublicationNode{}, err
	}
	if err := userErrorsToDetailedError("publicationCreate", data.PublicationCreate.UserErrors); err != nil {
		return dto.PublicationNode{}, err
	}
	if data.PublicationCreate.Publication == nil || strings.TrimSpace(data.PublicationCreate.Publication.ID) == "" {
		return dto.PublicationNode{}, errors.New("shopify publication create returned empty id")
	}
	return *data.PublicationCreate.Publication, nil
}

func (c *Client) updatePublicationAutoPublish(ctx context.Context, publicationID string) (dto.PublicationNode, error) {
	query := `
	mutation publicationUpdate($id: ID!, $input: PublicationInput!) {
		publicationUpdate(id: $id, input: $input) {
			publication { id autoPublish }
			userErrors { field message }
		}
	}`

	input := map[string]any{
		"autoPublish": true,
	}

	var data dto.PublicationUpdateData
	if err := c.graphqlRequest(ctx, query, map[string]any{
		"id":    publicationID,
		"input": input,
	}, &data); err != nil {
		return dto.PublicationNode{}, err
	}
	if err := userErrorsToDetailedError("publicationUpdate", data.PublicationUpdate.UserErrors); err != nil {
		return dto.PublicationNode{}, err
	}
	if data.PublicationUpdate.Publication == nil || strings.TrimSpace(data.PublicationUpdate.Publication.ID) == "" {
		return dto.PublicationNode{}, errors.New("shopify publication update returned empty id")
	}
	return *data.PublicationUpdate.Publication, nil
}

func (c *Client) createPriceList(ctx context.Context, catalogID string) (dto.PriceListNode, error) {
	query := `
	mutation priceListCreate($input: PriceListCreateInput!) {
		priceListCreate(input: $input) {
			priceList { id name currency }
			userErrors { field message }
		}
	}`

	input := map[string]any{
		"catalogId": catalogID,
		"name":      israelPriceList,
		"currency":  currencyILS,
		"parent": map[string]any{
			"adjustment": map[string]any{
				"type":  "PERCENTAGE_INCREASE",
				"value": 0,
			},
		},
	}

	var data dto.PriceListCreateData
	if err := c.graphqlRequest(ctx, query, map[string]any{"input": input}, &data); err != nil {
		return dto.PriceListNode{}, err
	}
	if err := userErrorsToDetailedError("priceListCreate", data.PriceListCreate.UserErrors); err != nil {
		return dto.PriceListNode{}, err
	}
	if data.PriceListCreate.PriceList == nil || strings.TrimSpace(data.PriceListCreate.PriceList.ID) == "" {
		return dto.PriceListNode{}, errors.New("shopify price list create returned empty id")
	}
	return *data.PriceListCreate.PriceList, nil
}

func (c *Client) verifyIsraelMarketSetup(ctx context.Context, resources IsraelMarketResources) error {
	if resources.MarketID == "" || resources.CatalogID == "" {
		return errors.New("shopify market resources are incomplete")
	}
	attached, err := c.marketHasCatalog(ctx, resources.MarketID, resources.CatalogID)
	if err != nil {
		return err
	}
	if !attached {
		return fmt.Errorf("shopify market %s missing catalog %s", resources.MarketID, resources.CatalogID)
	}

	publication, priceList, err := c.getCatalogDetails(ctx, resources.CatalogID)
	if err != nil {
		return err
	}
	if publication.ID == "" || !publication.AutoPublish {
		return fmt.Errorf("shopify catalog %s publication not ready", resources.CatalogID)
	}
	if priceList.ID == "" || !strings.EqualFold(strings.TrimSpace(priceList.Currency), currencyILS) {
		return fmt.Errorf("shopify catalog %s price list currency mismatch", resources.CatalogID)
	}

	return nil
}

type marketInfo struct {
	ID              string
	Name            string
	Handle          string
	Enabled         bool
	CurrencyCode    string
	LocalCurrencies bool
}

type resolvedPriceInput struct {
	SKU       string
	ProductID string
	VariantID string
	USDPrice  float64
	ILSPrice  float64
}

func validatePriceInput(input PriceUpsertInput) error {
	if input.USDPrice < 0 || input.ILSPrice < 0 {
		return errors.New("shopify price must be non-negative")
	}
	if input.VariantID == "" && strings.TrimSpace(input.SKU) == "" {
		return errors.New("shopify price requires sku or variant id")
	}
	return nil
}

func (c *Client) resolvePriceInput(ctx context.Context, input PriceUpsertInput) (resolvedPriceInput, error) {
	resolved := resolvedPriceInput{
		SKU:       strings.TrimSpace(input.SKU),
		ProductID: strings.TrimSpace(input.ProductID),
		VariantID: strings.TrimSpace(input.VariantID),
		USDPrice:  input.USDPrice,
		ILSPrice:  input.ILSPrice,
	}

	if resolved.VariantID == "" {
		variantID, productID, err := c.findVariantBySKU(ctx, resolved.SKU)
		if err != nil {
			return resolvedPriceInput{}, err
		}
		resolved.VariantID = variantID
		if resolved.ProductID == "" {
			resolved.ProductID = productID
		}
	}

	if resolved.VariantID == "" {
		return resolvedPriceInput{}, errors.New("shopify variant id is required")
	}

	if resolved.ProductID == "" {
		productID, err := c.getProductIDForVariant(ctx, resolved.VariantID)
		if err != nil {
			return resolvedPriceInput{}, err
		}
		resolved.ProductID = productID
	}

	if resolved.ProductID == "" {
		return resolvedPriceInput{}, errors.New("shopify product id is required")
	}

	if input.ProductID != "" && !strings.EqualFold(strings.TrimSpace(input.ProductID), resolved.ProductID) {
		return resolvedPriceInput{}, fmt.Errorf("shopify sku %s resolved to different product id", resolved.SKU)
	}

	return resolved, nil
}

func (c *Client) findVariantBySKU(ctx context.Context, sku string) (string, string, error) {
	sku = strings.TrimSpace(sku)
	if sku == "" {
		return "", "", errors.New("shopify sku is required")
	}

	query := `
	query productVariantBySku($first: Int!, $query: String!) {
		productVariants(first: $first, query: $query) {
			nodes { id sku product { id } }
		}
	}`

	var data productVariantSearchData
	if err := c.graphqlRequest(ctx, query, map[string]any{
		"first": 1,
		"query": buildSearchQuery("sku", sku),
	}, &data); err != nil {
		return "", "", err
	}
	if len(data.ProductVariants.Nodes) == 0 {
		return "", "", &variantNotFoundError{SKU: sku}
	}
	variant := data.ProductVariants.Nodes[0]
	return strings.TrimSpace(variant.ID), strings.TrimSpace(variant.Product.ID), nil
}

func (c *Client) getProductIDForVariant(ctx context.Context, variantID string) (string, error) {
	variantID = strings.TrimSpace(variantID)
	if variantID == "" {
		return "", errors.New("shopify variant id is required")
	}

	query := `
	query productVariant($id: ID!) {
		productVariant(id: $id) {
			id
			product { id }
		}
	}`

	var data dto.ProductVariantProductData
	if err := c.graphqlRequest(ctx, query, map[string]any{"id": variantID}, &data); err != nil {
		return "", err
	}
	if data.ProductVariant == nil {
		return "", errors.New("shopify variant lookup returned empty data")
	}
	return strings.TrimSpace(data.ProductVariant.Product.ID), nil
}

func (c *Client) updateBaseUSDPrices(ctx context.Context, inputs []resolvedPriceInput) error {
	if len(inputs) == 0 {
		return nil
	}
	byProduct := make(map[string][]resolvedPriceInput)
	for _, item := range inputs {
		byProduct[item.ProductID] = append(byProduct[item.ProductID], item)
	}

	query := `
	mutation productVariantsBulkUpdate($productId: ID!, $variants: [ProductVariantsBulkInput!]!) {
		productVariantsBulkUpdate(productId: $productId, variants: $variants) {
			productVariants { id }
			userErrors { field message }
		}
	}`

	for productID, items := range byProduct {
		for start := 0; start < len(items); start += maxVariantsBatchSize {
			end := start + maxVariantsBatchSize
			if end > len(items) {
				end = len(items)
			}
			batch := items[start:end]
			variants := make([]map[string]any, 0, len(batch))
			for _, item := range batch {
				variants = append(variants, map[string]any{
					"id":    item.VariantID,
					"price": formatMoneyAmount(item.USDPrice),
				})
			}
			var data productVariantsBulkUpdateData
			err := c.graphqlRequest(ctx, query, map[string]any{
				"productId": productID,
				"variants":  variants,
			}, &data)
			if err != nil {
				return err
			}
			if err := userErrorsToDetailedError("productVariantsBulkUpdate", data.ProductVariantsBulkUpdate.UserErrors); err != nil {
				return err
			}
			for _, item := range batch {
				if item.SKU == "" {
					continue
				}
				c.logSuccess(fmt.Sprintf("shopify price updated sku=%s usd=%s", item.SKU, formatMoneyAmount(item.USDPrice)))
			}
		}
	}

	return nil
}

func (c *Client) addFixedILSPrices(ctx context.Context, priceListID string, inputs []resolvedPriceInput) error {
	priceListID = strings.TrimSpace(priceListID)
	if priceListID == "" {
		return errors.New("shopify price list id is required")
	}
	if len(inputs) == 0 {
		return nil
	}

	query := `
	mutation priceListFixedPricesAdd($priceListId: ID!, $prices: [PriceListPriceInput!]!) {
		priceListFixedPricesAdd(priceListId: $priceListId, prices: $prices) {
			userErrors { field message }
		}
	}`

	for start := 0; start < len(inputs); start += maxFixedPriceBatchSize {
		end := start + maxFixedPriceBatchSize
		if end > len(inputs) {
			end = len(inputs)
		}
		batch := inputs[start:end]
		prices := make([]map[string]any, 0, len(batch))
		for _, item := range batch {
			prices = append(prices, map[string]any{
				"variantId": item.VariantID,
				"price": map[string]any{
					"amount":       formatMoneyAmount(item.ILSPrice),
					"currencyCode": currencyILS,
				},
			})
		}
		var data dto.PriceListFixedPricesAddData
		err := c.graphqlRequest(ctx, query, map[string]any{
			"priceListId": priceListID,
			"prices":      prices,
		}, &data)
		if err != nil {
			return err
		}
		if err := userErrorsToDetailedError("priceListFixedPricesAdd", data.PriceListFixedPricesAdd.UserErrors); err != nil {
			return err
		}
		for _, item := range batch {
			if item.SKU == "" {
				continue
			}
			c.logSuccess(fmt.Sprintf("shopify price updated sku=%s ils=%s", item.SKU, formatMoneyAmount(item.ILSPrice)))
		}
	}

	return nil
}

func formatMoneyAmount(amount float64) string {
	return strconv.FormatFloat(amount, 'f', 2, 64)
}

func userErrorsToDetailedError(action string, errs []dto.ShopifyUserError) error {
	if len(errs) == 0 {
		return nil
	}
	details := make([]userErrorDetail, 0, len(errs))
	for _, e := range errs {
		message := strings.TrimSpace(e.Message)
		if message == "" {
			continue
		}
		field := ""
		if len(e.Field) > 0 {
			field = strings.Join(e.Field, ".")
		}
		details = append(details, userErrorDetail{Field: field, Message: message})
	}
	if len(details) == 0 {
		return &userErrorsError{Action: action, Errors: []userErrorDetail{{Message: "user errors returned"}}}
	}
	return &userErrorsError{Action: action, Errors: details}
}

func (c *Client) getPriceCache() (IsraelMarketResources, bool) {
	c.priceMu.Lock()
	defer c.priceMu.Unlock()
	if c.priceCache == nil {
		return IsraelMarketResources{}, false
	}
	return *c.priceCache, true
}

func (c *Client) setPriceCache(resources IsraelMarketResources) {
	c.priceMu.Lock()
	c.priceCache = &resources
	c.priceMu.Unlock()
}

func (c *Client) logSuccess(message string) {
	if c.logger == nil || strings.TrimSpace(message) == "" {
		return
	}
	c.logger.LogSuccess(message)
}

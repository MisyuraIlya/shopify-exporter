package shopify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"shopify-exporter/internal/adapters/shopify/dto"
	"shopify-exporter/internal/config"
	"shopify-exporter/internal/domain/model"
	"shopify-exporter/internal/logging"
	"strings"
	"time"
)

type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type productUpdateData struct {
	ProductUpdate struct {
		Product    *dto.ShopifyProduct    `json:"product"`
		UserErrors []dto.ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"productUpdate"`
}

type productVariantLookupData struct {
	Product *struct {
		Variants struct {
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes,omitempty"`
		} `json:"variants,omitempty"`
	} `json:"product,omitempty"`
}

type productVariantSearchData struct {
	ProductVariants struct {
		Nodes []struct {
			ID      string `json:"id,omitempty"`
			SKU     string `json:"sku,omitempty"`
			Product struct {
				ID string `json:"id,omitempty"`
			} `json:"product,omitempty"`
		} `json:"nodes,omitempty"`
	} `json:"productVariants"`
}

type productVariantsBulkUpdateData struct {
	ProductVariantsBulkUpdate struct {
		ProductVariants []struct {
			ID string `json:"id,omitempty"`
		} `json:"productVariants,omitempty"`
		UserErrors []dto.ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"productVariantsBulkUpdate"`
}

type productTranslationResourceData struct {
	TranslatableResource *struct {
		ResourceId          string `json:"resourceId,omitempty"`
		TranslatableContent []struct {
			Key    string `json:"key,omitempty"`
			Value  string `json:"value,omitempty"`
			Digest string `json:"digest,omitempty"`
			Locale string `json:"locale,omitempty"`
		} `json:"translatableContent"`
	} `json:"translatableResource"`
}

type productTranslationUpdateData struct {
	TranslationsRegister struct {
		Translations []struct {
			Key   string `json:"key,omitempty"`
			Value string `json:"value,omitempty"`
		} `json:"translations"`
		UserErrors []dto.ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"translationsRegister"`
}

type NewClientService interface {
	CreateProduct(ctx context.Context, products model.Product) (string, error)
	UpdateProduct(ctx context.Context, product model.Product, productGid string) error
	UpdateLocalization(ctx context.Context, product model.Product, productGid string) error
	GetCollectionProducts(ctx context.Context) ([]model.Product, error)
	UnpublishProduct(ctx context.Context, productId string) error
	CheckExistProductBySku(product model.Product) (bool, string)
}

type Client struct {
	config     config.ShopifyConfig
	httpClient *http.Client
	logger     logging.LoggerService
}

func NewClient(config config.ShopifyConfig, httpClient *http.Client, logger logging.LoggerService) NewClientService {
	if httpClient == nil {
		timeout := config.Timeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		httpClient = &http.Client{Timeout: timeout}
	}
	return &Client{
		config:     config,
		httpClient: httpClient,
		logger:     logger,
	}
}

func (c *Client) logError(message string, err error) {
	if c.logger == nil || err == nil {
		return
	}
	c.logger.LogError(message, err)
}

func (c *Client) CreateProduct(ctx context.Context, product model.Product) (string, error) {

	if product.EnglishTitle == "" {
		return "", errors.New("shopify product title is required")
	}

	input := map[string]any{
		"title":  product.EnglishTitle,
		"status": productStatus(product.IsPublished),
	}
	if product.Description != "" {
		input["descriptionHtml"] = product.Description
	}

	query := `
	mutation productCreate($input: ProductInput!) {
		productCreate(input: $input) {
			product { id }
			userErrors { field message }
		}
	}`

	var data dto.ProductCreateData
	err := c.graphqlRequest(ctx, query,
		map[string]any{
			"input": input,
		}, &data)

	if err != nil {
		c.logError("shopify product create request failed", err)
		return "", err
	}

	errGraph := userErrorsToError("productCreate", data.ProductCreate.UserErrors)

	if errGraph != nil {
		c.logError("shopify product create user errors", errGraph)
		return "", errGraph
	}

	if data.ProductCreate.Product == nil || data.ProductCreate.Product.ID == "" {
		return "", errors.New("shopify product create returned empty product id")
	}

	err = c.updatePrimaryVariantIdentifiers(ctx, data.ProductCreate.Product.ID, product)
	if err != nil {
		c.logError("shopify product create variant update failed", err)
		return "", err
	}

	return data.ProductCreate.Product.ID, nil
}

func (c *Client) UpdateProduct(ctx context.Context, product model.Product, productGid string) error {
	productGid = strings.TrimSpace(productGid)
	if productGid == "" {
		return errors.New("shopify product gid is required")
	}

	input := map[string]any{
		"id":     productGid,
		"status": productStatus(product.IsPublished),
	}

	if title := strings.TrimSpace(product.EnglishTitle); title != "" {
		input["title"] = title
	} else if title := strings.TrimSpace(product.EnglishTitle); title != "" {
		input["title"] = title
	}
	if strings.TrimSpace(product.Description) != "" {
		input["descriptionHtml"] = product.Description
	}

	query := `
	mutation productUpdate($input: ProductInput!) {
		productUpdate(input: $input) {
			product { id }
			userErrors { field message }
		}
	}`

	var data productUpdateData
	err := c.graphqlRequest(ctx, query, map[string]any{
		"input": input,
	}, &data)
	if err != nil {
		c.logError("shopify product update request failed", err)
		return err
	}
	if err := userErrorsToError("productUpdate", data.ProductUpdate.UserErrors); err != nil {
		c.logError("shopify product update user errors", err)
		return err
	}

	err = c.updatePrimaryVariantIdentifiers(ctx, productGid, product)
	if err != nil {
		c.logError("shopify product update variant update failed", err)
		return err
	}

	return nil
}

func (c *Client) CheckExistProductBySku(product model.Product) (bool, string) {
	sku := strings.TrimSpace(product.Sku)
	if sku == "" {
		return false, ""
	}

	queryValue := sku
	if strings.ContainsAny(queryValue, " \"") {
		queryValue = strings.ReplaceAll(queryValue, `"`, `\"`)
		queryValue = fmt.Sprintf(`"%s"`, queryValue)
	}
	searchQuery := fmt.Sprintf("sku:%s", queryValue)

	query := `
	query productVariantBySku($first: Int!, $query: String!) {
		productVariants(first: $first, query: $query) {
			nodes {
				id
				sku
				product { id }
			}
		}
	}`

	var data productVariantSearchData
	err := c.graphqlRequest(context.Background(), query, map[string]any{
		"first": 1,
		"query": searchQuery,
	}, &data)
	if err != nil {
		c.logError("shopify product variant search failed", err)
		return false, ""
	}

	if len(data.ProductVariants.Nodes) == 0 {
		return false, ""
	}

	gid := strings.TrimSpace(data.ProductVariants.Nodes[0].Product.ID)
	return gid != "", gid
}

func (c *Client) GetCollectionProducts(ctx context.Context) ([]model.Product, error) {
	const pageSize = 100

	query := `
	query products($first: Int!, $after: String) {
		products(first: $first, after: $after) {
			nodes {
				id
				title
				descriptionHtml
				status
				variants(first: 1) {
					nodes { sku barcode }
				}
			}
			pageInfo { hasNextPage endCursor }
		}
	}`

	var (
		products []model.Product
		cursor   *string
	)

	for {
		variables := map[string]any{"first": pageSize}
		if cursor != nil && *cursor != "" {
			variables["after"] = *cursor
		}

		var data dto.ProductsQueryData
		err := c.graphqlRequest(ctx, query, variables, &data)
		if err != nil {
			c.logError("shopify products query failed", err)
			return nil, err
		}

		for _, sp := range data.Products.Nodes {
			products = append(products, mapShopifyProduct(sp))
		}

		if !data.Products.PageInfo.HasNextPage || data.Products.PageInfo.EndCursor == "" {
			break
		}
		next := data.Products.PageInfo.EndCursor
		cursor = &next
	}

	return products, nil
}

func (c *Client) UnpublishProduct(ctx context.Context, productId string) error {
	productId = strings.TrimSpace(productId)
	if productId == "" {
		return errors.New("shopify product id is required")
	}

	query := `
	mutation productUpdate($input: ProductInput!) {
		productUpdate(input: $input) {
			product { id status }
			userErrors { field message }
		}
	}`

	var data productUpdateData
	err := c.graphqlRequest(ctx, query, map[string]any{
		"input": map[string]any{
			"id":     productId,
			"status": "DRAFT",
		},
	}, &data)
	if err != nil {
		c.logError("shopify unpublish request failed", err)
		return err
	}
	if err := userErrorsToError("productUpdate", data.ProductUpdate.UserErrors); err != nil {
		c.logError("shopify unpublish user errors", err)
		return err
	}

	return nil
}

func (c *Client) shopifyAPIRequest(ctx context.Context, method string, endpoint string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		c.logError("shopify request build failed", err)
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Shopify-Access-Token", c.config.Token)

	client := c.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		c.logError("shopify request failed", err)
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logError("shopify response read failed", err)
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("shopify request failed: %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
		c.logError("shopify request returned non-success status", err)
		return nil, err
	}

	return respBody, nil
}

func (c *Client) graphqlRequest(ctx context.Context, query string, variables map[string]any, out any) error {
	domain := strings.TrimSpace(c.config.ShopDomain)
	if domain == "" {
		return errors.New("shopify shop domain is empty")
	}
	if !strings.HasPrefix(domain, "http://") && !strings.HasPrefix(domain, "https://") {
		domain = "https://" + domain
	}
	domain = strings.TrimRight(domain, "/")
	if c.config.APIVer == "" {
		return errors.New("shopify api version is empty")
	}
	endpoint := domain + "/admin/api/" + c.config.APIVer + "/graphql.json"

	payload := graphQLRequest{
		Query:     strings.TrimSpace(query),
		Variables: variables,
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		c.logError("shopify graphql marshal failed", err)
		return err
	}

	raw, err := c.shopifyAPIRequest(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		c.logError("shopify graphql request failed", err)
		return err
	}

	var resp dto.GraphQLResponse[json.RawMessage]
	if err := json.Unmarshal(raw, &resp); err != nil {
		c.logError("shopify graphql response unmarshal failed", err)
		return err
	}
	if len(resp.Errors) > 0 {
		err := fmt.Errorf("shopify graphql errors: %s", formatGraphQLErrors(resp.Errors))
		c.logError("shopify graphql response errors", err)
		return err
	}
	if out == nil {
		return nil
	}
	if len(resp.Data) == 0 {
		return errors.New("shopify graphql response missing data")
	}
	if err := json.Unmarshal(resp.Data, out); err != nil {
		c.logError("shopify graphql data unmarshal failed", err)
		return err
	}

	return nil
}

func (c *Client) getPrimaryVariantID(ctx context.Context, productGid string) (string, error) {
	query := `
	query productVariant($id: ID!) {
		product(id: $id) {
			variants(first: 1) {
				nodes { id }
			}
		}
	}`

	var data productVariantLookupData
	err := c.graphqlRequest(ctx, query, map[string]any{
		"id": productGid,
	}, &data)
	if err != nil {
		c.logError("shopify variant lookup failed", err)
		return "", err
	}
	if data.Product == nil || len(data.Product.Variants.Nodes) == 0 {
		return "", nil
	}
	return strings.TrimSpace(data.Product.Variants.Nodes[0].ID), nil
}

func (c *Client) updatePrimaryVariantIdentifiers(ctx context.Context, productGid string, product model.Product) error {
	variantID, err := c.getPrimaryVariantID(ctx, productGid)
	if err != nil {
		c.logError("shopify primary variant lookup failed", err)
		return err
	}
	if variantID == "" {
		return errors.New("shopify product has no variants to update")
	}

	variantInput := map[string]any{"id": variantID}

	if product.Sku != "" {
		variantInput["inventoryItem"] = map[string]any{
			"sku": product.Sku,
		}
	}

	if product.Barcode != "" {
		variantInput["barcode"] = product.Barcode
	}

	variantQuery := `
	mutation productVariantsBulkUpdate($productId: ID!, $variants: [ProductVariantsBulkInput!]!) {
		productVariantsBulkUpdate(productId: $productId, variants: $variants) {
			productVariants { id }
			userErrors { field message }
		}
	}`

	var variantData productVariantsBulkUpdateData
	err = c.graphqlRequest(ctx, variantQuery, map[string]any{
		"productId": productGid,
		"variants":  []map[string]any{variantInput},
	}, &variantData)
	if err != nil {
		c.logError("shopify variant update request failed", err)
		return err
	}
	if err := userErrorsToError("productVariantsBulkUpdate", variantData.ProductVariantsBulkUpdate.UserErrors); err != nil {
		c.logError("shopify variant update user errors", err)
		return err
	}

	return nil
}

func productStatus(isPublished bool) string {
	if isPublished {
		return "ACTIVE"
	}
	return "DRAFT"
}

func mapShopifyProduct(p dto.ShopifyProduct) model.Product {
	var sku, barcode string
	if len(p.Variants.Nodes) > 0 {
		sku = p.Variants.Nodes[0].SKU
		barcode = p.Variants.Nodes[0].Barcode
	} else if len(p.Variants.Edges) > 0 {
		sku = p.Variants.Edges[0].Node.SKU
		barcode = p.Variants.Edges[0].Node.Barcode
	}

	return model.Product{
		Sku:         sku,
		HebrewTitle: p.Title,
		Description: p.DescriptionHTML,
		IsPublished: strings.EqualFold(p.Status, "ACTIVE"),
		Barcode:     barcode,
	}
}

func userErrorsToError(action string, errs []dto.ShopifyUserError) error {
	if len(errs) == 0 {
		return nil
	}
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		msg := strings.TrimSpace(e.Message)
		if msg == "" {
			continue
		}
		if len(e.Field) > 0 {
			msg = fmt.Sprintf("%s: %s", strings.Join(e.Field, "."), msg)
		}
		parts = append(parts, msg)
	}
	if len(parts) == 0 {
		return fmt.Errorf("shopify %s failed with user errors", action)
	}
	return fmt.Errorf("shopify %s failed: %s", action, strings.Join(parts, "; "))
}

func formatGraphQLErrors(errs []dto.GraphQLError) string {
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		msg := strings.TrimSpace(e.Message)
		if msg == "" {
			continue
		}
		if len(e.Path) > 0 {
			msg = fmt.Sprintf("%s (path: %v)", msg, e.Path)
		}
		parts = append(parts, msg)
	}
	if len(parts) == 0 {
		return "unknown graphql error"
	}
	return strings.Join(parts, "; ")
}

func (c *Client) UpdateLocalization(ctx context.Context, product model.Product, productGid string) error {
	if productGid == "" {
		return nil
	}

	translationDigest := c.getProductLocalizationDigest(ctx, productGid)

	if translationDigest == "" {
		return nil
	}

	updateLocalization := c.updateProductLocalization(ctx, translationDigest, productGid, product.HebrewTitle)

	if updateLocalization != nil {
		c.logError("shopify update localization failed", updateLocalization)
	}

	return nil
}

func (c *Client) getProductLocalizationDigest(ctx context.Context, productGid string) string {
	if productGid == "" {
		return ""
	}

	query := `
	query ($id: ID!) {
		translatableResource(resourceId: $id) {
			resourceId translatableContent {
				key value digest locale
			}
		}
	}`

	var result productTranslationResourceData
	err := c.graphqlRequest(ctx, query, map[string]any{
		"id": productGid,
	}, &result)

	if err != nil {
		c.logError("shopify get localization digest failed", err)
		return ""
	}

	if result.TranslatableResource == nil {
		return ""
	}

	for _, v := range result.TranslatableResource.TranslatableContent {
		if v.Key == "title" && v.Locale == "en" {
			return v.Digest
		}
	}

	return ""

}

func (c *Client) updateProductLocalization(ctx context.Context, translationDigest string, productDigit string, valueTranslation string) error {
	if valueTranslation == "" || translationDigest == "" || productDigit == "" {
		return nil
	}

	obj := map[string]any{
		"locale":                    "he",
		"key":                       "title",
		"value":                     valueTranslation,
		"translatableContentDigest": translationDigest,
	}

	translatinPayload := []map[string]any{obj}

	paylod := map[string]any{
		"resourceId":   productDigit,
		"translations": translatinPayload,
	}

	variantQuery := `
	mutation translationsRegister($resourceId: ID!, $translations: [TranslationInput!]!) {
		translationsRegister(resourceId: $resourceId, translations: $translations) {
			userErrors { message field } 
			translations { key value } 
		}
	}`

	var result productTranslationUpdateData

	err := c.graphqlRequest(ctx, variantQuery, paylod, &result)

	if err != nil {
		c.logError("shopify update localization request failed", err)
		return err
	}

	if err := userErrorsToError("TranslationsRegister", result.TranslationsRegister.UserErrors); err != nil {
		c.logError("shopify update localization user errors", err)
		return err
	}

	return nil
}

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
	"strings"
	"time"
)

type NewClientService interface {
	CreateProduct(ctx context.Context, products model.Product) error
	UpdateProduct(ctx context.Context, product model.Product, productGid string) error
	GetCollectionProducts(ctx context.Context) ([]model.Product, error)
	UnpublishProduct(ctx context.Context, productId string) error
	CheckExistProductBySku(product model.Product) (bool, string)
}

type Client struct {
	config     config.ShopifyConfig
	httpClient *http.Client
}

func NewClient(config config.ShopifyConfig, httpClient *http.Client) NewClientService {
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
	}
}

func (c *Client) CreateProduct(ctx context.Context, product model.Product) error {
	title := strings.TrimSpace(product.Title)
	if title == "" {
		title = strings.TrimSpace(product.EnglishTitle)
	}
	if title == "" {
		return errors.New("shopify product title is required")
	}

	input := map[string]any{
		"title":  title,
		"status": productStatus(product.IsPublished),
	}
	if strings.TrimSpace(product.Description) != "" {
		input["descriptionHtml"] = product.Description
	}

	variantInput := map[string]any{}
	if strings.TrimSpace(product.Sku) != "" {
		variantInput["sku"] = product.Sku
	}
	if strings.TrimSpace(product.Barcode) != "" {
		variantInput["barcode"] = product.Barcode
	}
	if len(variantInput) > 0 {
		input["variants"] = []map[string]any{variantInput}
	}

	query := `
mutation productCreate($input: ProductInput!) {
	productCreate(input: $input) {
		product { id }
		userErrors { field message }
	}
}`

	var data dto.ProductCreateData
	err := c.graphqlRequest(ctx, query, map[string]any{
		"input": input,
	}, &data)
	if err != nil {
		return err
	}
	return userErrorsToError("productCreate", data.ProductCreate.UserErrors)
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

	if title := strings.TrimSpace(product.Title); title != "" {
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
		return err
	}
	if err := userErrorsToError("productUpdate", data.ProductUpdate.UserErrors); err != nil {
		return err
	}

	if strings.TrimSpace(product.Sku) == "" && strings.TrimSpace(product.Barcode) == "" {
		return nil
	}

	variantID, err := c.getPrimaryVariantID(ctx, productGid)
	if err != nil {
		return err
	}
	if variantID == "" {
		return errors.New("shopify product has no variants to update")
	}

	variantInput := map[string]any{"id": variantID}
	if strings.TrimSpace(product.Sku) != "" {
		variantInput["sku"] = product.Sku
	}
	if strings.TrimSpace(product.Barcode) != "" {
		variantInput["barcode"] = product.Barcode
	}

	variantQuery := `
mutation productVariantUpdate($input: ProductVariantInput!) {
	productVariantUpdate(input: $input) {
		productVariant { id }
		userErrors { field message }
	}
}`

	var variantData productVariantUpdateData
	err = c.graphqlRequest(ctx, variantQuery, map[string]any{
		"input": variantInput,
	}, &variantData)
	if err != nil {
		return err
	}
	return userErrorsToError("productVariantUpdate", variantData.ProductVariantUpdate.UserErrors)
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
		return err
	}
	return userErrorsToError("productUpdate", data.ProductUpdate.UserErrors)
}

func (c *Client) shopifyAPIRequest(ctx context.Context, method string, endpoint string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
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
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("shopify request failed: %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	return respBody, nil
}

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

type productVariantUpdateData struct {
	ProductVariantUpdate struct {
		ProductVariant *struct {
			ID string `json:"id,omitempty"`
		} `json:"productVariant,omitempty"`
		UserErrors []dto.ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"productVariantUpdate"`
}

func (c *Client) graphqlRequest(ctx context.Context, query string, variables map[string]any, out any) error {
	endpoint, err := c.graphQLEndpoint()
	if err != nil {
		return err
	}

	payload := graphQLRequest{
		Query:     strings.TrimSpace(query),
		Variables: variables,
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	raw, err := c.shopifyAPIRequest(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}

	var resp dto.GraphQLResponse[json.RawMessage]
	if err := json.Unmarshal(raw, &resp); err != nil {
		return err
	}
	if len(resp.Errors) > 0 {
		return fmt.Errorf("shopify graphql errors: %s", formatGraphQLErrors(resp.Errors))
	}
	if out == nil {
		return nil
	}
	if len(resp.Data) == 0 {
		return errors.New("shopify graphql response missing data")
	}
	return json.Unmarshal(resp.Data, out)
}

func (c *Client) graphQLEndpoint() (string, error) {
	base, err := c.apiBaseURL()
	if err != nil {
		return "", err
	}
	return base + "/graphql.json", nil
}

func (c *Client) apiBaseURL() (string, error) {
	domain := strings.TrimSpace(c.config.ShopDomain)
	if domain == "" {
		return "", errors.New("shopify shop domain is empty")
	}
	if !strings.HasPrefix(domain, "http://") && !strings.HasPrefix(domain, "https://") {
		domain = "https://" + domain
	}
	domain = strings.TrimRight(domain, "/")
	if c.config.APIVer == "" {
		return "", errors.New("shopify api version is empty")
	}
	return domain + "/admin/api/" + c.config.APIVer, nil
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
		return "", err
	}
	if data.Product == nil || len(data.Product.Variants.Nodes) == 0 {
		return "", nil
	}
	return strings.TrimSpace(data.Product.Variants.Nodes[0].ID), nil
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
		Sku:          sku,
		Title:        p.Title,
		EnglishTitle: p.Title,
		Description:  p.DescriptionHTML,
		IsPublished:  strings.EqualFold(p.Status, "ACTIVE"),
		Barcode:      barcode,
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

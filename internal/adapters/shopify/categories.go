package shopify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"shopify-exporter/internal/config"
	"shopify-exporter/internal/domain/model"
	"shopify-exporter/internal/logging"
	"strings"
	"time"

	"shopify-exporter/internal/adapters/shopify/dto"
)

type ShopifyCategoryService interface {
	CheckCategoryExist(ctx context.Context, category model.Category) (bool, error)
	CreateCategory(ctx context.Context, category model.Category)
	UpdateCategory(ctx context.Context, category model.Category)
}

type ClientShopifyCategoryService struct {
	config     config.ShopifyConfig
	httpClient *http.Client
	logger     logging.LoggerService
}

type collectionTranslationResourceData struct {
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

type collectionTranslationUpdateData struct {
	TranslationsRegister struct {
		Translations []struct {
			Key   string `json:"key,omitempty"`
			Value string `json:"value,omitempty"`
		} `json:"translations"`
		UserErrors []dto.ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"translationsRegister"`
}

func NewShopifyCategoryService(config config.ShopifyConfig, httpClient *http.Client, logger logging.LoggerService) ShopifyCategoryService {
	if httpClient == nil {
		timeout := config.Timeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		httpClient = &http.Client{Timeout: timeout}
	}
	return &ClientShopifyCategoryService{
		config:     config,
		httpClient: httpClient,
		logger:     logger,
	}
}

func (c *ClientShopifyCategoryService) CheckCategoryExist(ctx context.Context, category model.Category) (bool, error) {
	title := categoryTitle(category)
	if title == "" {
		return false, nil
	}
	collectionID, err := c.findCollectionByTitle(ctx, title)
	if err != nil {
		c.logError("shopify category lookup failed", err)
		return false, err
	}
	return collectionID != "", nil
}

func (c *ClientShopifyCategoryService) CreateCategory(ctx context.Context, category model.Category) {
	title := categoryTitle(category)
	if title == "" {
		c.logError("shopify category title is required", errors.New("empty category title"))
		return
	}

	collectionID, err := c.createCollection(ctx, title)
	if err != nil {
		c.logError("shopify category create failed", err)
		return
	}
	c.logSuccess(fmt.Sprintf("shopify category created title=%s id=%s", title, collectionID))

	hebrewTitle := strings.TrimSpace(category.TitleHebrew)
	if shouldUpdateTranslation(title, hebrewTitle) {
		if err := c.updateCollectionTranslation(ctx, collectionID, hebrewTitle); err != nil {
			c.logError("shopify category translation update failed", err)
		}
	}
}

func (c *ClientShopifyCategoryService) UpdateCategory(ctx context.Context, category model.Category) {
	title := categoryTitle(category)
	if title == "" {
		c.logError("shopify category title is required", errors.New("empty category title"))
		return
	}

	collectionID, err := c.findCollectionByTitle(ctx, title)
	if err != nil {
		c.logError("shopify category lookup failed", err)
		return
	}

	if collectionID == "" {
		c.CreateCategory(ctx, category)
		return
	}

	englishTitle := strings.TrimSpace(category.TitlteEnglish)
	if englishTitle != "" && englishTitle != title {
		title = englishTitle
	}

	if err := c.updateCollection(ctx, collectionID, title); err != nil {
		c.logError("shopify category update failed", err)
		return
	}
	c.logSuccess(fmt.Sprintf("shopify category updated title=%s id=%s", title, collectionID))

	hebrewTitle := strings.TrimSpace(category.TitleHebrew)
	if shouldUpdateTranslation(title, hebrewTitle) {
		if err := c.updateCollectionTranslation(ctx, collectionID, hebrewTitle); err != nil {
			c.logError("shopify category translation update failed", err)
		}
	}
}

func (c *ClientShopifyCategoryService) logError(message string, err error) {
	if c.logger == nil || err == nil {
		return
	}
	c.logger.LogError(message, err)
}

func (c *ClientShopifyCategoryService) logWarning(message string) {
	if c.logger == nil || strings.TrimSpace(message) == "" {
		return
	}
	c.logger.LogWarning(message)
}

func (c *ClientShopifyCategoryService) logSuccess(message string) {
	if c.logger == nil || strings.TrimSpace(message) == "" {
		return
	}
	c.logger.LogSuccess(message)
}

func (c *ClientShopifyCategoryService) findCollectionByTitle(ctx context.Context, title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", errors.New("shopify collection title is required")
	}

	query := `
	query collections($first: Int!, $query: String!) {
		collections(first: $first, query: $query) {
			nodes { id title }
		}
	}`

	var data dto.CollectionsQueryData
	err := c.graphqlRequest(ctx, query, map[string]any{
		"first": 1,
		"query": buildSearchQuery("title", title),
	}, &data)
	if err != nil {
		return "", err
	}

	if len(data.Collections.Nodes) == 0 {
		return "", nil
	}
	return strings.TrimSpace(data.Collections.Nodes[0].ID), nil
}

func (c *ClientShopifyCategoryService) createCollection(ctx context.Context, title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", errors.New("shopify category title is required")
	}

	query := `
	mutation collectionCreate($input: CollectionInput!) {
		collectionCreate(input: $input) {
			collection { id title }
			userErrors { field message }
		}
	}`

	var data dto.CollectionCreateData
	err := c.graphqlRequest(ctx, query, map[string]any{
		"input": map[string]any{
			"title": title,
		},
	}, &data)
	if err != nil {
		return "", err
	}
	if err := userErrorsToError("collectionCreate", data.CollectionCreate.UserErrors); err != nil {
		return "", err
	}
	if data.CollectionCreate.Collection == nil || strings.TrimSpace(data.CollectionCreate.Collection.ID) == "" {
		return "", errors.New("shopify category create returned empty collection id")
	}
	return strings.TrimSpace(data.CollectionCreate.Collection.ID), nil
}

func (c *ClientShopifyCategoryService) updateCollection(ctx context.Context, collectionID string, title string) error {
	collectionID = strings.TrimSpace(collectionID)
	if collectionID == "" {
		return errors.New("shopify category id is required")
	}

	input := map[string]any{"id": collectionID}
	title = strings.TrimSpace(title)
	if title != "" {
		input["title"] = title
	}

	query := `
	mutation collectionUpdate($input: CollectionInput!) {
		collectionUpdate(input: $input) {
			collection { id title }
			userErrors { field message }
		}
	}`

	var data dto.CollectionUpdateData
	err := c.graphqlRequest(ctx, query, map[string]any{
		"input": input,
	}, &data)
	if err != nil {
		return err
	}
	return userErrorsToError("collectionUpdate", data.CollectionUpdate.UserErrors)
}

func (c *ClientShopifyCategoryService) addProductToCollection(ctx context.Context, collectionID string, productID string) error {
	collectionID = strings.TrimSpace(collectionID)
	productID = strings.TrimSpace(productID)
	if collectionID == "" || productID == "" {
		return errors.New("shopify collection id and product id are required")
	}

	query := `
	mutation collectionAddProducts($id: ID!, $productIds: [ID!]!) {
		collectionAddProducts(id: $id, productIds: $productIds) {
			userErrors { field message }
		}
	}`

	var data dto.CollectionAddProductsData
	err := c.graphqlRequest(ctx, query, map[string]any{
		"id":         collectionID,
		"productIds": []string{productID},
	}, &data)
	if err != nil {
		return err
	}
	return userErrorsToError("collectionAddProducts", data.CollectionAddProducts.UserErrors)
}

func (c *ClientShopifyCategoryService) lookupProductIDBySKU(ctx context.Context, sku string) (string, error) {
	sku = strings.TrimSpace(sku)
	if sku == "" {
		return "", errors.New("shopify product sku is required")
	}

	query := `
	query productVariantBySku($first: Int!, $query: String!) {
		productVariants(first: $first, query: $query) {
			nodes {
				product { id }
			}
		}
	}`

	var data productVariantSearchData
	err := c.graphqlRequest(ctx, query, map[string]any{
		"first": 1,
		"query": buildSearchQuery("sku", sku),
	}, &data)
	if err != nil {
		return "", err
	}
	if len(data.ProductVariants.Nodes) == 0 {
		return "", nil
	}
	return strings.TrimSpace(data.ProductVariants.Nodes[0].Product.ID), nil
}

func (c *ClientShopifyCategoryService) updateCollectionTranslation(ctx context.Context, collectionID string, hebrewTitle string) error {
	collectionID = strings.TrimSpace(collectionID)
	hebrewTitle = strings.TrimSpace(hebrewTitle)
	if collectionID == "" || hebrewTitle == "" {
		return nil
	}

	translationDigest := c.getCollectionTranslationDigest(ctx, collectionID)
	if translationDigest == "" {
		return nil
	}

	payload := map[string]any{
		"resourceId": collectionID,
		"translations": []map[string]any{
			{
				"locale":                    "he",
				"key":                       "title",
				"value":                     hebrewTitle,
				"translatableContentDigest": translationDigest,
			},
		},
	}

	query := `
	mutation translationsRegister($resourceId: ID!, $translations: [TranslationInput!]!) {
		translationsRegister(resourceId: $resourceId, translations: $translations) {
			userErrors { message field }
			translations { key value }
		}
	}`

	var data collectionTranslationUpdateData
	err := c.graphqlRequest(ctx, query, payload, &data)
	if err != nil {
		return err
	}
	return userErrorsToError("translationsRegister", data.TranslationsRegister.UserErrors)
}

func (c *ClientShopifyCategoryService) getCollectionTranslationDigest(ctx context.Context, collectionID string) string {
	collectionID = strings.TrimSpace(collectionID)
	if collectionID == "" {
		return ""
	}

	query := `
	query ($id: ID!) {
		translatableResource(resourceId: $id) {
			resourceId
			translatableContent { key value digest locale }
		}
	}`

	var data collectionTranslationResourceData
	err := c.graphqlRequest(ctx, query, map[string]any{
		"id": collectionID,
	}, &data)
	if err != nil {
		c.logError("shopify category translation digest lookup failed", err)
		return ""
	}
	if data.TranslatableResource == nil {
		return ""
	}
	for _, v := range data.TranslatableResource.TranslatableContent {
		if v.Key == "title" && v.Locale == "en" {
			return v.Digest
		}
	}
	return ""
}

func (c *ClientShopifyCategoryService) shopifyAPIRequest(ctx context.Context, method string, endpoint string, body io.Reader) ([]byte, error) {
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
		err := newHTTPStatusError(resp.StatusCode, resp.Status, respBody)
		c.logError("shopify request returned non-success status", err)
		return nil, err
	}

	return respBody, nil
}

func (c *ClientShopifyCategoryService) graphqlRequest(ctx context.Context, query string, variables map[string]any, out any) error {
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

	for attempt := 0; attempt <= graphqlRetryMax; attempt++ {
		raw, err := c.shopifyAPIRequest(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
		if err != nil {
			if attempt < graphqlRetryMax && isRetryableHTTPError(err) {
				if err := sleepWithContext(ctx, retryDelay(attempt)); err != nil {
					return err
				}
				continue
			}
			c.logError("shopify graphql request failed", err)
			return err
		}

		var resp dto.GraphQLResponse[json.RawMessage]
		if err := json.Unmarshal(raw, &resp); err != nil {
			c.logError("shopify graphql response unmarshal failed", err)
			return err
		}
		if len(resp.Errors) > 0 {
			if isThrottleGraphQLError(resp.Errors) && attempt < graphqlRetryMax {
				if err := sleepWithContext(ctx, retryDelay(attempt)); err != nil {
					return err
				}
				continue
			}
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

	return errors.New("shopify graphql request retries exhausted")
}

func categoryTitle(category model.Category) string {
	title := strings.TrimSpace(category.TitlteEnglish)
	if title != "" {
		return title
	}
	return strings.TrimSpace(category.TitleHebrew)
}

func shouldUpdateTranslation(baseTitle string, translatedTitle string) bool {
	baseTitle = strings.TrimSpace(baseTitle)
	translatedTitle = strings.TrimSpace(translatedTitle)
	if translatedTitle == "" || baseTitle == "" {
		return false
	}
	return !strings.EqualFold(baseTitle, translatedTitle)
}

func buildSearchQuery(field, value string) string {
	queryValue := strings.TrimSpace(value)
	if strings.ContainsAny(queryValue, " \"") {
		queryValue = strings.ReplaceAll(queryValue, `"`, `\"`)
		queryValue = fmt.Sprintf(`"%s"`, queryValue)
	}
	return fmt.Sprintf("%s:%s", field, queryValue)
}

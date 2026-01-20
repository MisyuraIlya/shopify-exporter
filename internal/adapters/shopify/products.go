package shopify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"shopify-exporter/internal/config"
	"shopify-exporter/internal/domain/model"
	"strings"
)

type NewClientService interface {
	CreateProduct(ctx context.Context, products model.Product) error
}

type Client struct {
	config     config.ShopifyConfig
	httpClient *http.Client
}

func NewClient(config config.ShopifyConfig, httpClient *http.Client) NewClientService {
	return &Client{
		config:     config,
		httpClient: httpClient,
	}
}

func (c *Client) CreateProduct(ctx context.Context, product model.Product) error {

}

func (c *Client) UpdateProduct(ctx context.Context, product model.Product) error {

}

func (c *Client) GetCollectionProducts(ctx context.Context) ([]model.Product, error) {

}

func (c *Client) UnpublishProduct(ctx context.Context, productId string) error {

}

func (c *Client) shopifyAPIRequest(ctx context.Context, method string, endpoint string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Shopify-Access-Token", c.config.Token)

	resp, err := c.httpClient.Do(req)
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

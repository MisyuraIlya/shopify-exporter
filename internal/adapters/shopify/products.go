package shopify

import (
	"net/http"
	"shopify-exporter/internal/config"
)

type NewClientService interface {
	UploadProducts()
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

func (c *Client) UploadProducts() {

}

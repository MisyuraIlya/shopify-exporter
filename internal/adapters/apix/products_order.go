package apix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"shopify-exporter/internal/adapters/apix/dto"
	"shopify-exporter/internal/config"
	"shopify-exporter/internal/domain/model"
	"shopify-exporter/internal/logging"
	"strings"
)

type ProductOrderService interface {
	ProductsOrderList(ctx context.Context) ([]model.ProductOrder, error)
}

type ProductOrderClient struct {
	Config     config.ApiHasvConfig
	httpClient *http.Client
	logger     logging.LoggerService
}

func NewProductOrder(cfg config.ApiHasvConfig, httpClient *http.Client, logger logging.LoggerService) ProductOrderService {
	return &ProductOrderClient{
		Config:     cfg,
		httpClient: httpClient,
		logger:     logger,
	}
}

const API_ENDPOINT_PRODUCTS_ORDER = "/products-order"

func (c *ProductOrderClient) ProductsOrderList(ctx context.Context) ([]model.ProductOrder, error) {
	reqBody := dto.ProductsOrderRequest{
		DbName: "EMANUEL",
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		c.logger.LogError("error marshal body", err)
		return nil, fmt.Errorf("marshal products order request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Config.BaseUrl+API_ENDPOINT_PRODUCTS_ORDER, bytes.NewReader(bodyBytes))
	if err != nil {
		c.logger.LogError("error create request", err)
		return nil, fmt.Errorf("create products order request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.Config.Token)

	client := c.httpClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		c.logger.LogError("error response request", err)
		return nil, fmt.Errorf("products order request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.LogError("error io read", err)
		return nil, fmt.Errorf("read products order response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		statusErr := fmt.Errorf("apix products-order request failed: %s", resp.Status)
		c.logger.LogError("error response status", statusErr)
		return nil, statusErr
	}

	var apiResp dto.ProductsOrderResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		c.logger.LogError("error unmarshal", err)
		return nil, fmt.Errorf("unmarshal products order response: %w", err)
	}

	orders := make([]model.ProductOrder, 0, len(apiResp.Products))
	for _, product := range apiResp.Products {
		orders = append(orders, mapProductOrder(product))
	}

	return orders, nil
}

func mapProductOrder(data dto.ProductOrderDto) model.ProductOrder {
	categories := make([]model.ProductOrderCategory, 0, len(data.Categories))
	for _, category := range data.Categories {
		categories = append(categories, model.ProductOrderCategory{
			CategoryNoteID: category.CategoryNoteID,
			CategoryValue:  strings.TrimSpace(category.CategoryValue),
			OrderNoteID:    category.OrderNoteID,
			OrderValue:     strings.TrimSpace(category.OrderValue),
			OrderNumber:    category.OrderNumber,
		})
	}

	return model.ProductOrder{
		Sku:        strings.TrimSpace(data.Sku),
		Categories: categories,
	}
}

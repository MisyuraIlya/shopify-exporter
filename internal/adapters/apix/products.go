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
)

type NewClientService interface {
	ListProducts(ctx context.Context, page, limit int) ([]model.Product, int, error)
}

type Client struct {
	config     config.ApiHasvConfig
	httpClient *http.Client
}

func NewClient(config config.ApiHasvConfig, httpClient *http.Client) NewClientService {
	return &Client{
		config:     config,
		httpClient: httpClient,
	}
}

func (c *Client) ListProducts(ctx context.Context, page, limit int) ([]model.Product, int, error) {
	reqBody := struct {
		DbName   string   `json:"dbName"`
		Page     int      `json:"page"`
		PageSize int      `json:"pageSize"`
		NoteIds  []string `json:"noteIds"`
	}{
		DbName:   "EMANUEL",
		Page:     page,
		PageSize: limit,
		NoteIds:  []string{"17", "78", "79", "80", "81"},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseUrl+"/products", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.config.Token)

	client := c.httpClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, 0, fmt.Errorf("apix products request failed: %s", resp.Status)
	}

	var apiResp dto.ProductResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, 0, err
	}

	products := make([]model.Product, 0, len(apiResp.Products))
	for _, p := range apiResp.Products {
		products = append(products, mapProduct(p))
	}

	return products, apiResp.TotalPages, nil
}

func mapProduct(dto dto.ProductDto) model.Product {
	return model.Product{
		Sku:          dto.ItemKey,
		HebrewTitle:  dto.ItemName,
		EnglishTitle: dto.ForignName,
		IsPublished:  dto.Status,
		Barcode:      dto.BarCode,
	}
}

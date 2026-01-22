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

type PriceService interface {
	PriceList(ctx context.Context) ([]model.Price, error)
}

type NewPriceService struct {
	Config     config.ApiHasvConfig
	httpClient *http.Client
	logger     logging.LoggerService
}

const Endpoint = "/prices-latest"

func NewPriceSerivce(Config config.ApiHasvConfig, httpClient *http.Client, logger logging.LoggerService) PriceService {
	return &NewPriceService{
		Config:     Config,
		httpClient: httpClient,
		logger:     logger,
	}
}

func (c *NewPriceService) PriceList(ctx context.Context) ([]model.Price, error) {
	reqBody := map[string]any{
		"dbName": "EMANUEL",
	}

	url := c.Config.BaseUrl + Endpoint
	jsonMarshel, err := json.Marshal(reqBody)
	if err != nil {
		c.logger.LogError("error marshel json", err)
	}
	bytesBody := bytes.NewReader(jsonMarshel)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytesBody)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.Config.Token)

	client := c.httpClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		c.logger.LogError("error request", err)
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.LogError("error respnose", err)
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("apix products request failed: %s", resp.Status)
	}

	var apiResp dto.PriceRespone
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, err
	}

	prices := make([]model.Price, 0, len(apiResp.Prices))
	for _, v := range apiResp.Prices {
		prices = append(prices, mapPrice(v))
	}

	return prices, nil

}

func mapPrice(dto dto.PriceDto) model.Price {
	return model.Price{
		Sku:      dto.ItemKey,
		Currency: normalizeCurrencyCode(dto.CurrencyCode),
		Price:    dto.Price,
	}
}

func normalizeCurrencyCode(code string) string {
	value := strings.TrimSpace(code)
	switch value {
	case "$":
		return "USD"
	case "ש\"ח":
		return "ILS"
	case "₪":
		return "ILS"
	default:
		return strings.ToUpper(value)
	}
}

package apix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"shopify-exporter/internal/adapters/apix/dto"
	"shopify-exporter/internal/config"
	"shopify-exporter/internal/domain/model"
	"shopify-exporter/internal/logging"
)

type StockService interface {
	FetchStocks(ctx context.Context) ([]model.Stock, error)
}

type NewStockS struct {
	Config     config.ApiHasvConfig
	httpClient *http.Client
	logger     logging.LoggerService
}

const ENDPOINT = "/stocksProducts"

func NewStockService(Config config.ApiHasvConfig, httpClient *http.Client, logger logging.LoggerService) StockService {
	return &NewStockS{
		Config:     Config,
		httpClient: httpClient,
		logger:     logger,
	}
}

func (c *NewStockS) FetchStocks(ctx context.Context) ([]model.Stock, error) {
	body := map[string]any{
		"dbName": "EMANUEL",
	}
	url := c.Config.BaseUrl + ENDPOINT
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		c.logger.LogError("ERROR marshel json", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.Config.Token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.LogError("ERROR response json", err)
	}
	defer resp.Body.Close()

	parsed, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.LogError("ERROR response json", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		statusErr := fmt.Errorf("apix categories request failed: %s", resp.Status)
		c.logger.LogError("error response status", statusErr)
		return nil, statusErr
	}

	var result dto.StockResponse
	errUnmarshel := json.Unmarshal(parsed, &result)
	if errUnmarshel != nil {
		c.logger.LogError("ERROR response json", errUnmarshel)
	}

	resData := make([]model.Stock, 0, len(result.Items))
	for _, v := range result.Items {
		resData = append(resData, dtoMap(v))
	}
	return resData, nil
}

func dtoMap(dto dto.Stock) model.Stock {
	quantity := int32(0)
	if !math.IsNaN(dto.ItemWarHBal) && !math.IsInf(dto.ItemWarHBal, 0) {
		quantity = int32(math.Round(dto.ItemWarHBal))
	}
	return model.Stock{
		Sku:   dto.ItemKey,
		Stock: quantity,
	}
}

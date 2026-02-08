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
)

type DiscountService interface {
	DiscountList(ctx context.Context) ([]model.Discount, error)
}

type DiscountNew struct {
	Config     config.ApiHasvConfig
	httpClient *http.Client
	logger     logging.LoggerService
}

func NewDiscount(cfg config.ApiHasvConfig, httpClient *http.Client, logger logging.LoggerService) DiscountService {
	return &DiscountNew{
		Config:     cfg,
		httpClient: httpClient,
		logger:     logger,
	}
}

const API_ENDPOINT = "/discounts-item-code-5"

func (c *DiscountNew) DiscountList(ctx context.Context) ([]model.Discount, error) {
	reqBody := map[string]string{
		"dbName": "EMANUEL",
	}

	bodyJson, err := json.Marshal(reqBody)

	if err != nil {
		c.logger.LogError("Error Marshal json", err)
		return nil, err
	}

	jsonIoReader := bytes.NewReader(bodyJson)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Config.BaseUrl+API_ENDPOINT, jsonIoReader)

	if err != nil {
		c.logger.LogError("error create reqeust", err)
		return nil, err
	}

	req.Header.Set("Authorization", c.Config.Token)
	req.Header.Set("Content-Type", "application/json")

	client := c.httpClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)

	if err != nil {
		c.logger.LogError("Error response", err)
		return nil, err
	}

	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		statusErr := fmt.Errorf("apix categories request failed: %s", resp.Status)
		c.logger.LogError("error response status", statusErr)
		return nil, statusErr
	}

	var apiResp dto.DiscountResponse
	errMarshele := json.Unmarshal(respBody, &apiResp)
	if errMarshele != nil {
		c.logger.LogError("Error errMarshele", err)
		return nil, err
	}

	// result := make([]model.Discount, len(apiResp.Discounts))

	// for _, v := range apiResp.Discounts {
	// 	result = append(result, mapDiscount(v))
	// }
	return nil, nil
}

// func mapDiscount(dto dto.Discount) model.Discount {
// 	return model.Discount{
// 		// Sku: dto.,
// 	}
// }

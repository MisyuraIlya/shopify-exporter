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

type RelatedService interface {
	RelatedList(ctx context.Context) ([]model.Rellated, error)
}

type RelatedClient struct {
	Config     config.ApiHasvConfig
	httpClient *http.Client
	logger     logging.LoggerService
}

func NewRellated(cfg config.ApiHasvConfig, httpClient *http.Client, logger logging.LoggerService) RelatedService {
	return &RelatedClient{
		Config:     cfg,
		httpClient: httpClient,
		logger:     logger,
	}
}

const API_ENDPOINT_SIMILAR = "/similar-products"

func (c *RelatedClient) RelatedList(ctx context.Context) ([]model.Rellated, error) {
	reqBody := map[string]string{
		"dbName": "EMANUEL",
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		c.logger.LogError("error marshal body", err)
		return nil, fmt.Errorf("marshal related request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Config.BaseUrl+API_ENDPOINT_SIMILAR, bytes.NewReader(bodyBytes))
	if err != nil {
		c.logger.LogError("error create request", err)
		return nil, fmt.Errorf("create related request: %w", err)
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
		return nil, fmt.Errorf("related request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.LogError("error io read", err)
		return nil, fmt.Errorf("read related response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		statusErr := fmt.Errorf("apix related request failed: %s", resp.Status)
		c.logger.LogError("error response status", statusErr)
		return nil, statusErr
	}

	var apiResp dto.RellatedDtoResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		c.logger.LogError("error unmarshal", err)
		return nil, fmt.Errorf("unmarshal related response: %w", err)
	}

	related := make([]model.Rellated, 0, len(apiResp.Products))
	for _, v := range apiResp.Products {
		related = append(related, mapRelated(v))
	}

	return related, nil
}

func mapRelated(data dto.RellatedDto) model.Rellated {
	similar := make([]string, 0, len(data.SimilarSkus))
	for _, sku := range data.SimilarSkus {
		trimmed := strings.TrimSpace(sku)
		if trimmed == "" {
			continue
		}
		similar = append(similar, trimmed)
	}

	return model.Rellated{
		Sku:     strings.TrimSpace(data.Sku),
		Similar: similar,
	}
}

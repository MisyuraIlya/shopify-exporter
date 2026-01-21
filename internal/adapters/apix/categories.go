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

type CategoryService interface {
	CategoryList(ctx context.Context) ([]model.ProductCategories, error)
}

type CleintCategoryService struct {
	Config     config.ApiHasvConfig
	httpClient *http.Client
	logger     logging.LoggerService
}

func NewCategoryClientService(config config.ApiHasvConfig, httpClient *http.Client, logger logging.LoggerService) CategoryService {
	return &CleintCategoryService{
		Config:     config,
		httpClient: httpClient,
		logger:     logger,
	}
}

func (c *CleintCategoryService) CategoryList(ctx context.Context) ([]model.ProductCategories, error) {
	reqBody := dto.CategoryRequest{
		DbName: "EMANUEL",
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		c.logger.LogError("error marsah body", err)
		return nil, fmt.Errorf("marshal category request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Config.BaseUrl+"/custom-categories", bytes.NewReader(bodyBytes))

	if err != nil {
		c.logger.LogError("error create request", err)
		return nil, fmt.Errorf("create category request: %w", err)
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
		return nil, fmt.Errorf("category request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.LogError("error io read", err)
		return nil, fmt.Errorf("read category response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		statusErr := fmt.Errorf("apix categories request failed: %s", resp.Status)
		c.logger.LogError("error response status", statusErr)
		return nil, statusErr
	}

	var apiResp dto.CagtegoryResponse
	unmarshalErr := json.Unmarshal(respBody, &apiResp)
	if unmarshalErr != nil {
		c.logger.LogError("error unmarshal", unmarshalErr)
		return nil, fmt.Errorf("unmarshal category response: %w", unmarshalErr)
	}

	categories := make([]model.ProductCategories, 0, len(apiResp.Results))

	for _, v := range apiResp.Results {
		categories = append(categories, mapCategory(v))
	}

	return categories, nil
}

func mapCategory(dto dto.ProductCategoryDto) model.ProductCategories {
	mappedCategories := make([]model.Category, 0, len(dto.Categories))
	for _, v := range dto.Categories {
		hebrew := strings.TrimSpace(v.CategoryHewbrew)
		english := strings.TrimSpace(v.CategoryEnglish)
		if hebrew == "" && english == "" {
			continue
		}
		mappedCategories = append(mappedCategories, model.Category{
			TitleHebrew:   hebrew,
			TitlteEnglish: english,
		})
	}
	return model.ProductCategories{
		SKU:        strings.TrimSpace(dto.Sku),
		Categproes: mappedCategories,
	}
}

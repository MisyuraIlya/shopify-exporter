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

type AttributeService interface {
	AttributesList(ctx context.Context) ([]model.Attribute, error)
	AttributeProductList(ctx context.Context) ([]model.AttributeProduct, error)
}

type NewAttributeService struct {
	Config     config.ApiHasvConfig
	httpClient *http.Client
	logger     logging.LoggerService
}

func NewAttributeServiceClient(Config config.ApiHasvConfig, httpClient *http.Client, logger logging.LoggerService) AttributeService {
	return &NewAttributeService{
		Config:     Config,
		httpClient: httpClient,
		logger:     logger,
	}
}

const EndpointAttributes = "/attributes"

func (c *NewAttributeService) AttributesList(ctx context.Context) ([]model.Attribute, error) {
	data, err := c.apiResponse(ctx)
	if err != nil {
		c.logger.LogError("error api response", err)
		return nil, err
	}

	res := make([]model.Attribute, 0, len(data.AttributesMain))

	for _, v := range data.AttributesMain {
		res = append(res, mapAttribute(v))
	}

	return res, nil
}

func (c *NewAttributeService) AttributeProductList(ctx context.Context) ([]model.AttributeProduct, error) {
	data, err := c.apiResponse(ctx)
	if err != nil {
		c.logger.LogError("error api response", err)
	}

	res := make([]model.AttributeProduct, 0, len(data.AttributesProducts))

	for _, v := range data.AttributesProducts {
		res = append(res, mapAttributeProduct(v))
	}

	return res, nil
}

func (c *NewAttributeService) apiResponse(ctx context.Context) (dto.AttributesResponse, error) {
	var noteNames = [][2]string{
		{"סינון", "Filter"},                                    // noteId = 86 = 1
		{"מידות המוצר (ס\"מ)", "Item Size (cm)"},               // noteId = 9  = 2
		{"מידות כולל אריזה (ס\"מ)", "Size of packaging (cm)"},  // noteId = 15 = 3
		{"משקל נטו (ק\"ג)", "Net weight (kg)"},                 // noteId = 76 = 4
		{"משקל כולל אריזה (ק''ג)", "Weight With Packaging"},    // noteId = 27 = 5
		{"קיבולת הכוס (מ\"ל)", "Cup capacity (ml)"},            // noteId = 77 = 6
		{"תיאור", "Description"},                               // noteId = 20 = 7
		{"Item Size (inch)", "Item Size (inch)"},               // noteId = 10 = 8
		{"מידות אינצ' מוצר עם קופסה", "Packaging Size (inch)"}, // noteId = 16 = 9
	}

	reqBody := map[string]any{
		"dbName":   "EMANUEL",
		"noteName": noteNames,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		c.logger.LogError("error marshel json", err)
	}

	url := c.Config.BaseUrl + EndpointAttributes
	bytesBody := bytes.NewReader(jsonBody)
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
		return dto.AttributesResponse{}, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.LogError("error respnose", err)
		return dto.AttributesResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return dto.AttributesResponse{}, fmt.Errorf("apix products request failed: %s", resp.Status)
	}
	var apiResp dto.AttributesResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return dto.AttributesResponse{}, err
	}
	return apiResp, nil
}

func mapAttribute(dto dto.AttributeMain) model.Attribute {
	return model.Attribute{
		HebrewName:  dto.NoteName,
		EnglishName: dto.NoteNameEnglish,
	}
}

func mapAttributeProduct(dto dto.AttributeProduct) model.AttributeProduct {
	return model.AttributeProduct{
		Sku:                  dto.KeF,
		AttributeNameHebrew:  dto.Note,
		AttributeNameEnglish: dto.NoteEnglish,
	}
}

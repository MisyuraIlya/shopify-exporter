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
	"shopify-exporter/internal/debugsync"
	"shopify-exporter/internal/domain/model"
	"shopify-exporter/internal/logging"
	"strings"
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

func (c *NewStockS) logError(message string, err error) {
	if c.logger == nil || err == nil {
		return
	}
	c.logger.LogError(message, err)
}

// FetchStocks pulls the whole catalogue's warehouse balances in one request.
//
// Every failure path returns an error. It used to log and fall through, which meant a
// network failure dereferenced a nil response (panic), and a malformed body yielded an
// empty slice — sync_stocks then logged "no valid SKUs" and returned success while
// nothing had been synced. At four runs a day that was rare; on a five-minute cadence
// it is routine, and a silent success is worse than a loud failure.
func (c *NewStockS) FetchStocks(ctx context.Context) ([]model.Stock, error) {
	body := map[string]any{
		"dbName": "EMANUEL",
	}
	url := strings.TrimRight(strings.TrimSpace(c.Config.BaseUrl), "/") + ENDPOINT
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		c.logError("apix stocks marshal failed", err)
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		c.logError("apix stocks request build failed", err)
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.Config.Token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logError("apix stocks request failed", err)
		return nil, err
	}
	defer resp.Body.Close()

	parsed, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logError("apix stocks response read failed", err)
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		statusErr := fmt.Errorf("apix stocks request failed: %s", resp.Status)
		c.logError("apix stocks response status", statusErr)
		return nil, statusErr
	}

	var result dto.StockResponse
	if err := json.Unmarshal(parsed, &result); err != nil {
		c.logError("apix stocks response unmarshal failed", err)
		return nil, err
	}

	resData := make([]model.Stock, 0, len(result.Items))
	for _, v := range result.Items {
		mapped := dtoMap(v)
		if c.logger != nil && debugsync.MatchSKU(v.ItemKey) {
			c.logger.Log(fmt.Sprintf(
				"trace stock api sku=%s raw_itemwarhbal=%.2f exported_quantity=%d buffer=3",
				strings.TrimSpace(v.ItemKey),
				v.ItemWarHBal,
				mapped.Stock,
			))
		}
		resData = append(resData, mapped)
	}
	return resData, nil
}

// CLIENT WISH TO TAKE STOCK AND - 3 (3-unit reserve buffer).
// Clamp at 0: out-of-stock items (balance <= 3, or already negative in the ERP) are
// pushed to Shopify as 0 instead of going negative and being SKIPPED by sync_stocks,
// which left them showing as available on the storefront. See FIXES.md 2026-06-30.
func dtoMap(dto dto.Stock) model.Stock {
	quantity := int32(0)
	if !math.IsNaN(dto.ItemWarHBal) && !math.IsInf(dto.ItemWarHBal, 0) {
		quantity = int32(math.Round(dto.ItemWarHBal))
	}
	stock := quantity - 3
	if stock < 0 {
		stock = 0
	}
	return model.Stock{
		Sku:   dto.ItemKey,
		Stock: stock,
	}
}

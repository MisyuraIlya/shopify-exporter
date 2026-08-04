package shopify

import (
	"shopify-exporter/internal/report"
	"strings"
)

// ReporterAware is implemented by clients that can feed a run report. The sync
// entrypoints type-assert to it, matching how they discover StockService and
// PriceService on the same client.
type ReporterAware interface {
	SetReporter(recorder report.Recorder)
}

// SetReporter attaches the run report. Safe to leave unset — every report call is
// nil-guarded, so an unreported run behaves exactly as before.
func (c *Client) SetReporter(recorder report.Recorder) {
	if c == nil {
		return
	}
	c.reportMu.Lock()
	defer c.reportMu.Unlock()
	c.reporter = recorder
}

func (c *Client) recorder() report.Recorder {
	if c == nil {
		return nil
	}
	c.reportMu.Lock()
	defer c.reportMu.Unlock()
	return c.reporter
}

// reportStockSeen records one SKU's quantity outcome after a successful mutation.
func (c *Client) reportStockSeen(item resolvedStockInput) {
	recorder := c.recorder()
	if recorder == nil {
		return
	}
	recorder.StockSeen(item.SKU, item.BeforeQuantity, item.BeforeKnown, item.Quantity)
}

// reportPriceSeen records one SKU's price outcome for a single currency.
func (c *Client) reportPriceSeen(sku, currency string, before float64, beforeKnown bool, after float64) {
	recorder := c.recorder()
	if recorder == nil {
		return
	}
	recorder.PriceSeen(sku, currency, before, beforeKnown, after)
}

func (c *Client) reportWarning(scope, message string) {
	recorder := c.recorder()
	if recorder == nil || strings.TrimSpace(message) == "" {
		return
	}
	recorder.Warn(scope, message)
}

func (c *Client) reportIncr(scope, key string, n int64) {
	recorder := c.recorder()
	if recorder == nil {
		return
	}
	recorder.Incr(scope, key, n)
}

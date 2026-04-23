package shopify

import (
	"fmt"
	"shopify-exporter/internal/debugsync"
	"strings"
)

func (c *Client) traceSKU(sku, format string, args ...any) {
	if c == nil || c.logger == nil {
		return
	}
	sku = strings.TrimSpace(sku)
	if !debugsync.MatchSKU(sku) {
		return
	}
	message := strings.TrimSpace(format)
	if message == "" {
		message = "-"
	} else if len(args) > 0 {
		message = fmt.Sprintf(format, args...)
	}
	c.logger.Log(fmt.Sprintf("trace sku=%s %s", sku, message))
}

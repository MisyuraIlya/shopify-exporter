package usecases

import (
	"context"
	"fmt"
	"shopify-exporter/internal/adapters/apix"
	"shopify-exporter/internal/adapters/shopify"
	"shopify-exporter/internal/config"
	"shopify-exporter/internal/debugsync"
	"shopify-exporter/internal/infra/stockstate"
	"shopify-exporter/internal/logging"
	"sort"
	"strings"
	"time"
)

type SyncStocksService interface {
	Run(ctx context.Context) error
}

type ClientStock struct {
	apixClient    apix.StockService
	shopifyClient shopify.StockService
	logger        logging.LoggerService
	stockConfig   config.StockConfig
}

func NewSyncStocks(
	apixClient apix.StockService,
	shopifyClient shopify.StockService,
	logger logging.LoggerService,
	stockConfig config.StockConfig,
) SyncStocksService {
	return &ClientStock{
		apixClient:    apixClient,
		shopifyClient: shopifyClient,
		logger:        logger,
		stockConfig:   stockConfig,
	}
}

func (c *ClientStock) Run(ctx context.Context) error {
	c.log(fmt.Sprintf("Stock sync started mode=%s dry_run=%t", c.stockConfig.Mode, c.stockConfig.DryRun))
	if c.stockConfig.DryRun {
		c.logWarning("DRY RUN active (SYNC_STOCK_DRY_RUN): reads only, no writes to Shopify, snapshot not updated")
	}

	stocks, err := c.apixClient.FetchStocks(ctx)
	if err != nil {
		c.logError("Error fetch api stocks", err)
		return err
	}

	targets := make(map[string]int, len(stocks))
	skippedEmptySKU := 0
	skippedNegative := 0
	duplicateSKU := 0
	filteredOut := 0

	for _, item := range stocks {
		sku := strings.TrimSpace(item.Sku)
		if sku == "" {
			skippedEmptySKU++
			continue
		}
		if !debugsync.ShouldProcessSKU(sku) {
			filteredOut++
			continue
		}
		if item.Stock < 0 {
			// Defensive only: apix.dtoMap clamps at 0, so an out-of-stock item arrives
			// as 0 and gets pushed as 0 rather than being silently dropped and left
			// showing as available. See FIXES.md 2026-06-30.
			if debugsync.MatchSKU(sku) {
				c.log(fmt.Sprintf("trace stock skipped sku=%s reason=negative api_quantity=%d", sku, item.Stock))
			}
			skippedNegative++
			continue
		}
		if previous, ok := targets[sku]; ok {
			duplicateSKU++
			if debugsync.MatchSKU(sku) {
				c.log(fmt.Sprintf(
					"trace stock duplicate sku=%s previous_quantity=%d replacement_quantity=%d",
					sku,
					previous,
					int(item.Stock),
				))
			}
		}
		targets[sku] = int(item.Stock)
		if debugsync.MatchSKU(sku) {
			c.log(fmt.Sprintf(
				"trace stock prepared sku=%s api_quantity=%d shopify_quantity=%d",
				sku,
				item.Stock,
				int(item.Stock),
			))
		}
	}

	if len(targets) == 0 {
		c.logWarning("Stock sync skipped: no valid SKUs")
		return nil
	}

	inputs, snapshotUsable := c.selectInputs(targets)

	if len(inputs) == 0 {
		c.logSuccess(fmt.Sprintf(
			"Stock sync completed mode=%s no_erp_changes=true sku=%d",
			c.stockConfig.Mode,
			len(targets),
		))
		return nil
	}

	// One call, not a fan-out. The adapter resolves every SKU against a single
	// paginated inventory map and batches the mutations itself, so splitting the work
	// across goroutines here would rebuild that map once per batch. It bought nothing
	// even before: every Shopify request serialises on one process-wide lock in
	// adminGraphQLLimiter, so the concurrency was never real.
	if err := c.shopifyClient.SetOnHandQuantities(ctx, inputs); err != nil {
		c.logError("Error sync stocks", err)
		return err
	}

	c.saveSnapshot(targets, snapshotUsable)

	// candidates, not "pushed": this is how many SKUs were handed to the adapter, which
	// then skips the ones Shopify already holds. The adapter logs what it actually
	// wrote. Calling this "pushed" read as 1 written on a dry run that wrote nothing.
	c.logSuccess(fmt.Sprintf(
		"Stock sync completed mode=%s candidates=%d of=%d skipped_empty_sku=%d skipped_negative=%d duplicates=%d filtered_out=%d",
		c.stockConfig.Mode,
		len(inputs),
		len(targets),
		skippedEmptySKU,
		skippedNegative,
		duplicateSKU,
		filteredOut,
	))

	return nil
}

// selectInputs narrows the ERP feed to what this run should push. In full mode that is
// everything; in delta mode only the SKUs whose ERP quantity moved since the last
// successful run. The second return value reports whether the snapshot is trustworthy
// enough to write back afterwards.
func (c *ClientStock) selectInputs(targets map[string]int) ([]shopify.StockInput, bool) {
	// A SKU filter means this run deliberately saw only part of the catalogue. Writing
	// that back as the snapshot would tell the next run that every other SKU is
	// unchanged at a quantity it never pushed, so the snapshot is left alone.
	snapshotUsable := !debugsync.HasOnlySKUFilter()

	if !c.stockConfig.IsDelta() {
		return c.inputsFor(targets, nil), snapshotUsable
	}

	snapshot, err := stockstate.Load(c.stockConfig.StatePath)
	if err != nil {
		// Unreadable snapshot: push everything. Pushing too much costs API calls;
		// trusting a corrupt diff costs oversold orders.
		c.logWarning(fmt.Sprintf("stock delta snapshot unusable, falling back to a full push: %v", err))
		return c.inputsFor(targets, nil), snapshotUsable
	}
	if len(snapshot.Quantities) == 0 {
		c.log("stock delta has no previous snapshot, pushing everything once")
		return c.inputsFor(targets, nil), snapshotUsable
	}

	inputs := c.inputsFor(targets, &snapshot)
	c.log(fmt.Sprintf(
		"stock delta changed=%d of=%d snapshot_age=%s",
		len(inputs),
		len(targets),
		time.Since(snapshot.UpdatedAt).Round(time.Second),
	))
	return inputs, snapshotUsable
}

// inputsFor builds the push list, keeping only changed SKUs when a snapshot is given.
// The order is stable so a trace of two runs can be compared line by line.
func (c *ClientStock) inputsFor(targets map[string]int, snapshot *stockstate.Snapshot) []shopify.StockInput {
	skus := make([]string, 0, len(targets))
	for sku := range targets {
		if snapshot != nil && !snapshot.Changed(sku, targets[sku]) {
			continue
		}
		skus = append(skus, sku)
	}
	sort.Strings(skus)

	inputs := make([]shopify.StockInput, 0, len(skus))
	for _, sku := range skus {
		inputs = append(inputs, shopify.StockInput{SKU: sku, Quantity: targets[sku]})
	}
	return inputs
}

// saveSnapshot records the full ERP state, including SKUs this run had no reason to
// push, because that is what the next delta compares against. Written only after a
// successful push: if the push failed, the changed SKUs must be retried next tick.
func (c *ClientStock) saveSnapshot(targets map[string]int, usable bool) {
	// A dry run wrote nothing, so recording these quantities as pushed would make the
	// next real delta skip every one of them — the sync would go quiet while Shopify
	// stayed wrong. This is the single most dangerous thing a dry run could do.
	if c.stockConfig.DryRun {
		c.log("stock snapshot not written: dry run")
		return
	}
	if !usable {
		c.logWarning("stock snapshot not written: " + debugsync.OnlySKUsEnv + " limited this run to a subset of SKUs")
		return
	}
	if err := stockstate.Save(c.stockConfig.StatePath, targets, time.Now()); err != nil {
		// Not fatal: the stock was pushed. The next run just diffs against an older
		// snapshot, or pushes everything, which is correct either way.
		c.logWarning(fmt.Sprintf("stock snapshot write failed at %s: %v", c.stockConfig.StatePath, err))
	}
}

func (c *ClientStock) log(message string) {
	if c.logger != nil {
		c.logger.Log(message)
	}
}

func (c *ClientStock) logWarning(message string) {
	if c.logger != nil {
		c.logger.LogWarning(message)
	}
}

func (c *ClientStock) logSuccess(message string) {
	if c.logger != nil {
		c.logger.LogSuccess(message)
	}
}

func (c *ClientStock) logError(message string, err error) {
	if c.logger != nil {
		c.logger.LogError(message, err)
	}
}

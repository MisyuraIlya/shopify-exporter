package usecases

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"shopify-exporter/internal/adapters/shopify"
	"shopify-exporter/internal/config"
	"shopify-exporter/internal/domain/model"
	"shopify-exporter/internal/infra/stockstate"
	"sort"
	"testing"
	"time"
)

func testTime() time.Time {
	return time.Date(2026, 8, 4, 6, 0, 0, 0, time.UTC)
}

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

type fakeStockAPI struct {
	stocks []model.Stock
	err    error
}

func (f *fakeStockAPI) FetchStocks(context.Context) ([]model.Stock, error) {
	return f.stocks, f.err
}

type fakeStockShopify struct {
	calls   int
	batches [][]shopify.StockInput
	err     error
}

func (f *fakeStockShopify) SetOnHandQuantity(ctx context.Context, input shopify.StockInput) error {
	return f.SetOnHandQuantities(ctx, []shopify.StockInput{input})
}

func (f *fakeStockShopify) SetOnHandQuantities(_ context.Context, inputs []shopify.StockInput) error {
	f.calls++
	f.batches = append(f.batches, append([]shopify.StockInput(nil), inputs...))
	return f.err
}

// pushed flattens every SKU handed to Shopify across all calls.
func (f *fakeStockShopify) pushed() []string {
	skus := make([]string, 0)
	for _, batch := range f.batches {
		for _, input := range batch {
			skus = append(skus, input.SKU)
		}
	}
	sort.Strings(skus)
	return skus
}

func stocks(pairs map[string]int32) []model.Stock {
	out := make([]model.Stock, 0, len(pairs))
	for sku, quantity := range pairs {
		out = append(out, model.Stock{Sku: sku, Stock: quantity})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sku < out[j].Sku })
	return out
}

func deltaConfig(t *testing.T) config.StockConfig {
	t.Helper()
	return config.StockConfig{
		Mode:      config.StockModeDelta,
		StatePath: filepath.Join(t.TempDir(), "stock-state.json"),
	}
}

func equalSKUs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("pushed skus = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("pushed skus = %v, want %v", got, want)
		}
	}
}

// The adapter resolves every SKU against one paginated inventory map, so the use case
// must hand it the whole list in a single call. Splitting it would rebuild that map
// once per batch.
func TestSyncStocksPushesInOneCall(t *testing.T) {
	api := &fakeStockAPI{stocks: stocks(map[string]int32{"A-1": 5, "B-2": 0, "C-3": 12})}
	shop := &fakeStockShopify{}

	if err := NewSyncStocks(api, shop, nil, config.StockConfig{Mode: config.StockModeFull}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if shop.calls != 1 {
		t.Errorf("expected exactly one call to Shopify, got %d", shop.calls)
	}
	equalSKUs(t, shop.pushed(), []string{"A-1", "B-2", "C-3"})
}

func TestSyncStocksFullModeIgnoresSnapshot(t *testing.T) {
	cfg := deltaConfig(t)
	cfg.Mode = config.StockModeFull
	if err := stockstate.Save(cfg.StatePath, map[string]int{"A-1": 5}, testTime()); err != nil {
		t.Fatal(err)
	}

	api := &fakeStockAPI{stocks: stocks(map[string]int32{"A-1": 5})}
	shop := &fakeStockShopify{}
	if err := NewSyncStocks(api, shop, nil, cfg).Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Full mode is the reconciliation pass: it re-pushes even unchanged SKUs so drift
	// Shopify caused on its own gets corrected. The adapter is what skips the no-ops.
	equalSKUs(t, shop.pushed(), []string{"A-1"})
}

func TestSyncStocksDeltaPushesEverythingOnceThenNothing(t *testing.T) {
	cfg := deltaConfig(t)
	feed := stocks(map[string]int32{"A-1": 5, "B-2": 0})

	first := &fakeStockShopify{}
	if err := NewSyncStocks(&fakeStockAPI{stocks: feed}, first, nil, cfg).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	equalSKUs(t, first.pushed(), []string{"A-1", "B-2"})

	second := &fakeStockShopify{}
	if err := NewSyncStocks(&fakeStockAPI{stocks: feed}, second, nil, cfg).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if second.calls != 0 {
		t.Errorf("an unchanged ERP feed must cost zero Shopify calls, got %d", second.calls)
	}
}

func TestSyncStocksDeltaPushesOnlyChangedSKUs(t *testing.T) {
	cfg := deltaConfig(t)

	if err := NewSyncStocks(
		&fakeStockAPI{stocks: stocks(map[string]int32{"A-1": 5, "B-2": 3, "C-3": 0})},
		&fakeStockShopify{}, nil, cfg,
	).Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	// B-2 drops to zero, C-3 is restocked, A-1 is untouched.
	shop := &fakeStockShopify{}
	if err := NewSyncStocks(
		&fakeStockAPI{stocks: stocks(map[string]int32{"A-1": 5, "B-2": 0, "C-3": 4})},
		shop, nil, cfg,
	).Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	equalSKUs(t, shop.pushed(), []string{"B-2", "C-3"})
	for _, batch := range shop.batches {
		for _, input := range batch {
			if input.SKU == "B-2" && input.Quantity != 0 {
				t.Errorf("B-2 quantity = %d, want 0", input.Quantity)
			}
			if input.SKU == "C-3" && input.Quantity != 4 {
				t.Errorf("C-3 quantity = %d, want 4", input.Quantity)
			}
		}
	}
}

// A failed push must not be recorded as pushed, or the changed SKUs are never retried
// and Shopify keeps overselling the stale number.
func TestSyncStocksDeltaDoesNotSaveSnapshotAfterFailure(t *testing.T) {
	cfg := deltaConfig(t)
	feed := stocks(map[string]int32{"A-1": 5})
	pushErr := errors.New("shopify rejected the mutation")

	failing := &fakeStockShopify{err: pushErr}
	if err := NewSyncStocks(&fakeStockAPI{stocks: feed}, failing, nil, cfg).Run(context.Background()); !errors.Is(err, pushErr) {
		t.Fatalf("expected the push error to propagate, got %v", err)
	}

	retry := &fakeStockShopify{}
	if err := NewSyncStocks(&fakeStockAPI{stocks: feed}, retry, nil, cfg).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	equalSKUs(t, retry.pushed(), []string{"A-1"})
}

func TestSyncStocksDeltaFallsBackToFullPushOnCorruptSnapshot(t *testing.T) {
	cfg := deltaConfig(t)
	if err := writeFile(cfg.StatePath, `{"quantities": {"A-1": `); err != nil {
		t.Fatal(err)
	}

	shop := &fakeStockShopify{}
	if err := NewSyncStocks(
		&fakeStockAPI{stocks: stocks(map[string]int32{"A-1": 5, "B-2": 1})},
		shop, nil, cfg,
	).Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	equalSKUs(t, shop.pushed(), []string{"A-1", "B-2"})
}

func TestSyncStocksSkipsInvalidRows(t *testing.T) {
	api := &fakeStockAPI{stocks: []model.Stock{
		{Sku: "  A-1  ", Stock: 5},
		{Sku: "   ", Stock: 9},
		{Sku: "NEG-1", Stock: -2},
	}}
	shop := &fakeStockShopify{}

	if err := NewSyncStocks(api, shop, nil, config.StockConfig{Mode: config.StockModeFull}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Blank SKU dropped, negative dropped (apix clamps at 0 before this point, so a
	// negative here means the feed itself is odd), and the survivor is trimmed.
	equalSKUs(t, shop.pushed(), []string{"A-1"})
}

// The worst thing a dry run could do is record its proposals as pushed: the next real
// delta would then skip them all and stay quiet while Shopify held the wrong numbers.
func TestSyncStocksDryRunNeverWritesSnapshot(t *testing.T) {
	cfg := deltaConfig(t)
	cfg.DryRun = true
	feed := stocks(map[string]int32{"A-1": 5, "B-2": 2})

	if err := NewSyncStocks(&fakeStockAPI{stocks: feed}, &fakeStockShopify{}, nil, cfg).Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(cfg.StatePath); !os.IsNotExist(err) {
		t.Fatalf("a dry run must not create %s (err=%v)", cfg.StatePath, err)
	}

	// And the next real run must therefore still see everything as changed.
	cfg.DryRun = false
	shop := &fakeStockShopify{}
	if err := NewSyncStocks(&fakeStockAPI{stocks: feed}, shop, nil, cfg).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	equalSKUs(t, shop.pushed(), []string{"A-1", "B-2"})
}

// A dry run must not silently reuse a stale snapshot either: it still reports against
// the current ERP feed, and leaves an existing snapshot untouched.
func TestSyncStocksDryRunLeavesExistingSnapshotIntact(t *testing.T) {
	cfg := deltaConfig(t)
	if err := stockstate.Save(cfg.StatePath, map[string]int{"A-1": 5}, testTime()); err != nil {
		t.Fatal(err)
	}

	cfg.DryRun = true
	if err := NewSyncStocks(
		&fakeStockAPI{stocks: stocks(map[string]int32{"A-1": 9})},
		&fakeStockShopify{}, nil, cfg,
	).Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	snapshot, err := stockstate.Load(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Quantities["A-1"]; got != 5 {
		t.Errorf("snapshot A-1 = %d, want the untouched 5", got)
	}
	if !snapshot.UpdatedAt.Equal(testTime()) {
		t.Errorf("snapshot UpdatedAt = %s, want the untouched %s", snapshot.UpdatedAt, testTime())
	}
}

func TestSyncStocksFetchErrorPropagates(t *testing.T) {
	fetchErr := errors.New("erp unreachable")
	shop := &fakeStockShopify{}

	err := NewSyncStocks(&fakeStockAPI{err: fetchErr}, shop, nil, config.StockConfig{Mode: config.StockModeFull}).Run(context.Background())
	if !errors.Is(err, fetchErr) {
		t.Fatalf("expected the fetch error to propagate, got %v", err)
	}
	if shop.calls != 0 {
		t.Error("nothing may be pushed when the ERP feed could not be read")
	}
}

func TestSyncStocksEmptyFeedIsNotAFailure(t *testing.T) {
	shop := &fakeStockShopify{}
	if err := NewSyncStocks(&fakeStockAPI{}, shop, nil, config.StockConfig{Mode: config.StockModeFull}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if shop.calls != 0 {
		t.Error("an empty feed must not reach Shopify")
	}
}

package shopify

import (
	"shopify-exporter/internal/config"
	"testing"
)

// Ticket 12634541256: products synced to Shopify were inconsistently tracked — only
// SKUs covered by the ERP stock feed ever got `tracked: true`, so the rest could be
// oversold. Tracking is now set on the product-sync path, with a carve-out for the
// ZZ-* service items that must stay untracked to remain buyable.
func TestShouldTrackInventory(t *testing.T) {
	prefixes := config.DefaultUntrackedSkuPrefixes

	tracked := []string{
		// Real catalogue SKUs pulled from the ERP /products feed.
		"HVM-1", "CMG-28", "HAO-1", "CNJ-1",
		// No stock row in /stocksProducts, but still real merchandise: these are the
		// ones the old code left oversellable.
		"BMB-23", "CAJ-5", "TWS-125-3", "FNB-1", "SPY-LG-0",
		// Must not be caught by a loose "Z" match.
		"ZA-1", "ZZA-1", "AZZ-1",
	}
	for _, sku := range tracked {
		if !shouldTrackInventory(sku, prefixes) {
			t.Errorf("sku %q must be inventory-tracked, got tracked=false", sku)
		}
	}

	// All 45 published SKUs with no ERP stock row are ZZ-*: 44 × "נא פנו אלינו
	// לביצוע הזמנה" plus ZZ-99 "תשלום בכרטיס אשראי - הזמנות מיוחדות".
	untracked := []string{"ZZ-5", "ZZ-11", "ZZ-99", "ZZ-100", "ZZ-401", "zz-100"}
	for _, sku := range untracked {
		if shouldTrackInventory(sku, prefixes) {
			t.Errorf("service item %q must stay untracked, got tracked=true", sku)
		}
	}
}

func TestShouldTrackInventoryEdgeCases(t *testing.T) {
	prefixes := config.DefaultUntrackedSkuPrefixes

	if shouldTrackInventory("", prefixes) {
		t.Error("empty sku must not be tracked")
	}
	if shouldTrackInventory("   ", prefixes) {
		t.Error("blank sku must not be tracked")
	}
	if !shouldTrackInventory("  HVM-1  ", prefixes) {
		t.Error("surrounding whitespace must not change the verdict")
	}
	if shouldTrackInventory("  ZZ-100  ", prefixes) {
		t.Error("surrounding whitespace must not defeat the carve-out")
	}

	// Empty config (SHOPIFY_UNTRACKED_SKU_PREFIXES set to "") tracks everything.
	if !shouldTrackInventory("ZZ-100", nil) {
		t.Error("with no configured prefixes every sku must be tracked")
	}
	// Blank entries in the config must not swallow the whole catalogue.
	if !shouldTrackInventory("HVM-1", []string{"", "   "}) {
		t.Error("blank prefixes must be ignored, not match everything")
	}

	// Additional prefixes are honoured, case-insensitively.
	if shouldTrackInventory("SRV-1", []string{"ZZ-", "srv-"}) {
		t.Error("configured extra prefix must be honoured case-insensitively")
	}
}

func TestDefaultUntrackedSkuPrefixes(t *testing.T) {
	if len(config.DefaultUntrackedSkuPrefixes) != 1 || config.DefaultUntrackedSkuPrefixes[0] != "ZZ-" {
		t.Errorf("unexpected default carve-out: %v", config.DefaultUntrackedSkuPrefixes)
	}
}

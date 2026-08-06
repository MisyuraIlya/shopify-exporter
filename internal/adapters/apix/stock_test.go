package apix

import (
	"math"
	"shopify-exporter/internal/adapters/apix/dto"
	"testing"
)

// The 3-unit reserve is the client's rule, and the clamp at 0 is what stopped
// out-of-stock items showing as available on the storefront: a negative result used to
// be skipped by sync_stocks, leaving Shopify on its last positive number.
// See FIXES.md 2026-06-30.
func TestDtoMapAppliesReserveAndClampsAtZero(t *testing.T) {
	cases := []struct {
		name    string
		balance float64
		want    int32
	}{
		{"comfortably in stock", 250, 247},
		{"just above the reserve", 4, 1},
		{"exactly the reserve is out of stock", 3, 0},
		{"below the reserve", 1, 0},
		{"zero balance", 0, 0},
		{"negative balance in the erp", -1, 0},
		{"rounds to nearest", 7.6, 5},
		{"rounds half away from zero", 6.5, 4},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dtoMap(dto.Stock{ItemKey: "HVM-1", ItemWarHBal: tc.balance})
			if got.Stock != tc.want {
				t.Errorf("balance %.2f -> %d, want %d", tc.balance, got.Stock, tc.want)
			}
			if got.Sku != "HVM-1" {
				t.Errorf("sku = %q, want HVM-1", got.Sku)
			}
		})
	}
}

// A non-numeric balance must not become a huge or negative quantity.
func TestDtoMapHandlesNaNAndInf(t *testing.T) {
	for name, balance := range map[string]float64{
		"NaN":  math.NaN(),
		"+Inf": math.Inf(1),
		"-Inf": math.Inf(-1),
	} {
		t.Run(name, func(t *testing.T) {
			if got := dtoMap(dto.Stock{ItemKey: "HVM-1", ItemWarHBal: balance}); got.Stock != 0 {
				t.Errorf("%s balance -> %d, want 0", name, got.Stock)
			}
		})
	}
}

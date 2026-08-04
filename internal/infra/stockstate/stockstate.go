// Package stockstate persists the quantities the last successful stock sync pushed,
// so a frequent run can push only what the ERP actually changed.
//
// The snapshot deliberately records the ERP side, never Shopify's. Shopify's on-hand
// moves on its own when orders are fulfilled, so a cached copy of it would go stale
// and make the sync skip a SKU that genuinely needs correcting. The ERP is the source
// of truth: "did Hashavshevet change this number since we last pushed it" is the only
// question a delta tick needs, and drift in the other direction is what the periodic
// full run exists to reconcile.
package stockstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Snapshot is the last pushed ERP state.
type Snapshot struct {
	UpdatedAt time.Time `json:"updatedAt"`
	// Quantities maps SKU to the quantity that was last pushed successfully.
	Quantities map[string]int `json:"quantities"`
}

// Changed reports whether sku's target differs from the snapshot. A SKU the snapshot
// has never seen counts as changed, so a first run pushes everything.
func (s Snapshot) Changed(sku string, quantity int) bool {
	if s.Quantities == nil {
		return true
	}
	previous, ok := s.Quantities[sku]
	return !ok || previous != quantity
}

// Load reads the snapshot at path. A missing file is not an error — it yields an empty
// snapshot, which makes the next run a full push. A corrupt file is reported so the
// caller can warn, and also yields an empty snapshot: falling back to pushing
// everything is always safe, whereas trusting half a file is not.
func Load(path string) (Snapshot, error) {
	if path == "" {
		return Snapshot{}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Snapshot{}, nil
		}
		return Snapshot{}, err
	}

	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("stock state %s is unreadable: %w", path, err)
	}
	if snapshot.Quantities == nil {
		snapshot.Quantities = map[string]int{}
	}
	return snapshot, nil
}

// Save writes the snapshot atomically: a crash mid-write must not leave a truncated
// file behind, because the next run would then diff against nonsense.
func Save(path string, quantities map[string]int, updatedAt time.Time) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	payload, err := json.Marshal(Snapshot{
		UpdatedAt:  updatedAt,
		Quantities: quantities,
	})
	if err != nil {
		return err
	}

	temp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tempName := temp.Name()

	if _, err := temp.Write(payload); err != nil {
		temp.Close()
		os.Remove(tempName)
		return err
	}
	if err := temp.Close(); err != nil {
		os.Remove(tempName)
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		os.Remove(tempName)
		return err
	}
	return nil
}

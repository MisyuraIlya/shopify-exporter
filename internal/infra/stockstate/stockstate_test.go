package stockstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	snapshot, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("a missing snapshot must not be an error, got %v", err)
	}
	if len(snapshot.Quantities) != 0 {
		t.Errorf("expected an empty snapshot, got %d entries", len(snapshot.Quantities))
	}
	// An empty snapshot must make everything look changed, so a first run pushes all.
	if !snapshot.Changed("HVM-1", 0) {
		t.Error("an empty snapshot must report every sku as changed")
	}
}

func TestLoadCorruptFileReportsAndYieldsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.json")
	if err := os.WriteFile(path, []byte(`{"quantities": {"HVM-1": `), 0o644); err != nil {
		t.Fatal(err)
	}

	snapshot, err := Load(path)
	if err == nil {
		t.Fatal("a truncated snapshot must be reported so the caller can warn")
	}
	// Empty, not partial: the caller falls back to a full push, which is always safe.
	if len(snapshot.Quantities) != 0 {
		t.Errorf("a corrupt snapshot must yield no quantities, got %d", len(snapshot.Quantities))
	}
}

func TestSaveThenLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "stock-state.json")
	quantities := map[string]int{"HVM-1": 7, "CMG-28": 0}
	updatedAt := time.Date(2026, 8, 4, 9, 30, 0, 0, time.UTC)

	if err := Save(path, quantities, updatedAt); err != nil {
		t.Fatalf("Save must create missing directories: %v", err)
	}

	snapshot, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Quantities["HVM-1"]; got != 7 {
		t.Errorf("HVM-1 = %d, want 7", got)
	}
	if !snapshot.UpdatedAt.Equal(updatedAt) {
		t.Errorf("UpdatedAt = %s, want %s", snapshot.UpdatedAt, updatedAt)
	}
}

func TestChanged(t *testing.T) {
	snapshot := Snapshot{Quantities: map[string]int{"HVM-1": 7, "SOLD-OUT": 0}}

	if snapshot.Changed("HVM-1", 7) {
		t.Error("same quantity must not count as changed")
	}
	if !snapshot.Changed("HVM-1", 6) {
		t.Error("a different quantity must count as changed")
	}
	// Zero is a real value, not "absent": a sold-out item that stays sold out must not
	// be re-pushed every tick.
	if snapshot.Changed("SOLD-OUT", 0) {
		t.Error("a known zero must not count as changed")
	}
	if !snapshot.Changed("SOLD-OUT", 1) {
		t.Error("restocked item must count as changed")
	}
	if !snapshot.Changed("NEVER-SEEN", 0) {
		t.Error("an unseen sku must count as changed so it gets pushed once")
	}
}

func TestSaveLeavesNoTempFilesBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stock-state.json")

	for i := 0; i < 3; i++ {
		if err := Save(path, map[string]int{"HVM-1": i}, time.Unix(int64(i), 0)); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Errorf("expected only the snapshot to remain, found %v", names)
	}
}

func TestEmptyPathIsANoOp(t *testing.T) {
	if err := Save("", map[string]int{"HVM-1": 1}, time.Now()); err != nil {
		t.Errorf("Save with no path must be a no-op, got %v", err)
	}
	if _, err := Load(""); err != nil {
		t.Errorf("Load with no path must be a no-op, got %v", err)
	}
}

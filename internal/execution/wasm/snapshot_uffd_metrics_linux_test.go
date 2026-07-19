//go:build linux

package wasm

import "testing"

func TestUffdStrategyDirtyPageCountReportsTrackedBits(t *testing.T) {
	strategy := &UffdStrategy{dirty: []bool{true, false, true, true, false}}
	if got := strategy.DirtyPageCount(); got != 3 {
		t.Fatalf("DirtyPageCount() = %d, want 3", got)
	}
}

//go:build !windows && !linux

package membudget

// total reports physical RAM on platforms without a dedicated probe (e.g.
// darwin). It returns (0, false) so Default falls back to a fixed budget.
func total() (uint64, bool) { return 0, false }

// PeakRSS reports peak resident memory on platforms without a dedicated probe.
// It returns (0, false) so callers fall back to Go runtime stats.
func PeakRSS() (uint64, bool) { return 0, false }

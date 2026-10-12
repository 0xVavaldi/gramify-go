// Package membudget turns the --memory-max ceiling into concrete sub-budgets
// and provides the OS memory probes it relies on (total RAM, peak RSS).
//
// The ceiling is split roughly in half: one half sizes the read-ahead buffer
// pool (IO), the other bounds processing/sink state (CPU). The same ceiling is
// also applied as the Go soft memory limit (GOMEMLIMIT) by the caller, which
// provides the "little flexibility" backstop for transient/GC overhead.
//
// The platform-specific probes total() (used by Default) and PeakRSS (used for
// --debug reporting) live in probe_<os>.go.
package membudget

import (
	"fmt"
	"strconv"
	"strings"
)

// FallbackTotal is the assumed total RAM when detection fails; the default
// budget becomes a quarter of this.
const FallbackTotal = 1 << 30 // 1 GiB

// MinBudget is the smallest ceiling we honour. Requests below this are clamped
// up: gramify requires at least this much working memory to form a useful read
// double-buffer and sink state.
const MinBudget = 64 << 20 // 64 MiB

// DefaultMemBudget is the in-memory spill threshold used by sinks (dedup,
// freqsort) when constructed with a non-positive budget. The CLI always passes
// a real per-run budget; this is the fallback for direct API/test use.
const DefaultMemBudget = 256 << 20 // 256 MiB

// The peak resident memory of a run is modelled as:
//
//	peak ≈ churnFactor*(Read+Proc) + runtimeReserve
//
// churnFactor captures the worst-case spill moment, when the read pool, the
// live processing map, and the just-spilled map's string keys (not yet
// reclaimed by the GC) are all resident at once — roughly read + 2*proc, i.e.
// 1.5*(Read+Proc) when Read==Proc. runtimeReserve is the roughly-fixed Go
// runtime/GC baseline that sits on top regardless of budget (stacks, GC
// metadata, scavenger lag); reserving it keeps small budgets from overshooting.
//
// Inverting the model, the usable buffer budget is (Max-runtimeReserve)/
// churnFactor, so peak RSS lands near --memory-max instead of the ~1.5x
// overshoot seen when the buffers consumed the whole ceiling. Max itself is
// still applied as GOMEMLIMIT as the hard-ish backstop.
const (
	churnFactor    = 1.5
	runtimeReserve = 32 << 20 // 32 MiB
)

// Budget describes how the ceiling is divided.
type Budget struct {
	Max  int64 // total ceiling (applied as GOMEMLIMIT by the caller)
	Read int64 // read-ahead buffer pool (~half of the usable buffer budget)
	Proc int64 // processing / sink state (~half of the usable buffer budget)
}

// Split divides max into read and processing budgets, clamping to MinBudget.
// Read+Proc deliberately sum to less than Max (see reserveFraction): Max is the
// GOMEMLIMIT ceiling, while Read+Proc bound only our explicit buffers so that
// GC slack and transient garbage fit underneath it.
//
// Args:
//
//	max: the total memory ceiling in bytes.
//
// Returns:
//
//	a Budget with Max = the clamped ceiling and Read+Proc = the usable buffer
//	budget (~(1-reserveFraction) of Max).
func Split(max int64) Budget {
	if max < MinBudget {
		max = MinBudget
	}
	usable := int64(float64(max-runtimeReserve) / churnFactor)
	if usable < minUsable {
		usable = minUsable // keep buffers workable for tiny ceilings
	}
	read := usable / 2
	return Budget{Max: max, Read: read, Proc: usable - read}
}

// minUsable is the smallest read+proc buffer budget; with MinBudget=64 MiB and
// the reserve model this floor is not normally reached, but it guards against
// degenerate ceilings.
const minUsable = 8 << 20 // 8 MiB

// Default returns the budget to use when --memory-max is omitted: a quarter of
// detected system RAM, or FallbackTotal/4-equivalent when RAM is unknown.
//
// Returns:
//
//	the byte budget, and true when it was derived from detected RAM (false when
//	the fixed fallback was used).
func Default() (int64, bool) {
	if t, ok := total(); ok {
		return int64(t / 4), true
	}
	return FallbackTotal, false
}

// ParseSize parses a human size like "512M", "2G", "1048576", "256k".
// Recognised suffixes are B/K/M/G (case-insensitive, binary multiples).
//
// Args:
//
//	s: the size string.
//
// Returns:
//
//	the byte count, or an error if s is empty, malformed, or non-positive.
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	mult := int64(1)
	hasSuffix := true
	switch s[len(s)-1] {
	case 'k', 'K':
		mult = 1 << 10
	case 'm', 'M':
		mult = 1 << 20
	case 'g', 'G':
		mult = 1 << 30
	case 'b', 'B':
		mult = 1
	default:
		hasSuffix = false // bare byte count
	}
	if hasSuffix {
		s = strings.TrimSpace(s[:len(s)-1])
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	if n <= 0 {
		return 0, fmt.Errorf("size must be > 0")
	}
	return n * mult, nil
}

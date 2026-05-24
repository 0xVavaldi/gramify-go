// Package humanize formats byte sizes, item counts, and durations for display.
// It centralizes the formatting that was previously duplicated across the CLI
// and the progress bar.
package humanize

import (
	"fmt"
	"time"
)

// Bytes formats v bytes with binary units and a space, e.g. "512 B",
// "4.0 MiB". Used for memory diagnostics where clarity matters.
func Bytes(v float64) string {
	const unit = 1024
	if v < unit {
		return fmt.Sprintf("%.0f B", v)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	div, exp := float64(unit), 0
	for v/div >= unit && exp < len(units)-1 { // bounded by len(units)
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %s", v/div, units[exp])
}

// CompactBytes formats v bytes with single-letter binary units and no space,
// e.g. "512B", "4.0M". Used in tight progress displays.
func CompactBytes(v float64) string {
	const unit = 1024
	if v < unit {
		return fmt.Sprintf("%.0fB", v)
	}
	units := []string{"K", "M", "G", "T", "P"}
	div, exp := float64(unit), 0
	for v/div >= unit && exp < len(units)-1 { // bounded by len(units)
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%s", v/div, units[exp])
}

// Count formats v as a decimal-suffixed item count, e.g. "512", "1.5k", "2.5M".
func Count(v float64) string {
	const unit = 1000
	if v < unit {
		return fmt.Sprintf("%.0f", v)
	}
	units := []string{"k", "M", "G", "T"}
	div, exp := float64(unit), 0
	for v/div >= unit && exp < len(units)-1 { // bounded by len(units)
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%s", v/div, units[exp])
}

// Duration formats d as mm:ss, or h:mm:ss past an hour.
func Duration(d time.Duration) string {
	s := int(d.Seconds())
	h, m, sec := s/3600, (s%3600)/60, s%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%02d:%02d", m, sec)
}

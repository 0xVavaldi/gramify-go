//go:build linux

package membudget

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// total returns total physical RAM in bytes from /proc/meminfo's "MemTotal:"
// line (reported in kB).
func total() (uint64, bool) {
	return readMeminfoKB("/proc/meminfo", "MemTotal:")
}

// PeakRSS returns this process's peak resident set size in bytes from
// "VmHWM:" (high-water mark, in kB) in /proc/self/status.
//
// Returns:
//
//	the byte count and true on success; (0, false) if it cannot be read.
func PeakRSS() (uint64, bool) {
	return readMeminfoKB("/proc/self/status", "VmHWM:")
}

// readMeminfoKB scans a /proc file for a "Prefix: N kB" line and returns N*1024.
func readMeminfoKB(path, prefix string) (uint64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() { // bounded by file length
		line := sc.Text()
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		fields := strings.Fields(line) // "Prefix:" "12345" "kB"
		if len(fields) < 2 {
			return 0, false
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, false
		}
		return kb * 1024, true
	}
	return 0, false
}

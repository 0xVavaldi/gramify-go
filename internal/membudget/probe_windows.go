//go:build windows

package membudget

import (
	"syscall"
	"unsafe"
)

// memoryStatusEx mirrors the Win32 MEMORYSTATUSEX struct.
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

// total returns total physical RAM in bytes via GlobalMemoryStatusEx.
func total() (uint64, bool) {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")
	var m memoryStatusEx
	m.length = uint32(unsafe.Sizeof(m))
	r, _, _ := proc.Call(uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return 0, false
	}
	return m.totalPhys, true
}

// processMemoryCounters mirrors the Win32 PROCESS_MEMORY_COUNTERS struct.
type processMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

// PeakRSS returns the peak working-set size (resident RAM) of this process in
// bytes via GetProcessMemoryInfo.
//
// Returns:
//
//	the byte count and true on success; (0, false) if the syscall fails.
func PeakRSS() (uint64, bool) {
	curProc, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetCurrentProcess").Call()
	proc := syscall.NewLazyDLL("psapi.dll").NewProc("GetProcessMemoryInfo")
	var pmc processMemoryCounters
	pmc.cb = uint32(unsafe.Sizeof(pmc))
	r, _, _ := proc.Call(curProc, uintptr(unsafe.Pointer(&pmc)), uintptr(pmc.cb))
	if r == 0 {
		return 0, false
	}
	return uint64(pmc.peakWorkingSetSize), true
}

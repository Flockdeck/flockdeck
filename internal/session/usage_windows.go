package session

import (
	"syscall"
	"time"
	"unsafe"
)

// The reading is taken through the plain syscall package rather than a helper
// module, because Perch is a single binary with no cgo and the module's
// dependencies are fixed. Toolhelp gives the whole process table in one call,
// and only the processes in a pane's own tree are then opened for their
// figures.

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procGetProcessMemoryInfo = kernel32.NewProc("K32GetProcessMemoryInfo")
)

// processMemoryCounters is PROCESS_MEMORY_COUNTERS. Only WorkingSetSize is
// read; the rest is declared because the call is given the size of the whole
// structure and writes all of it.
type processMemoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

// procParents returns the parent of every process this one can see.
func procParents() map[int]int {
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer syscall.CloseHandle(snap)

	var e syscall.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	if err := syscall.Process32First(snap, &e); err != nil {
		return nil
	}
	out := make(map[int]int, 256)
	for {
		out[int(e.ProcessID)] = int(e.ParentProcessID)
		if err := syscall.Process32Next(snap, &e); err != nil {
			break
		}
	}
	return out
}

// procMetrics returns the CPU a process has used since it started and its
// resident memory. ok is false once the process is gone, or when this one is
// not allowed to ask about it.
func procMetrics(pid int) (cpu time.Duration, rss uint64, ok bool) {
	// PROCESS_QUERY_LIMITED_INFORMATION is enough for both calls and is
	// granted for processes a fuller query would be refused for.
	const queryLimitedInformation = 0x1000
	h, err := syscall.OpenProcess(queryLimitedInformation, false, uint32(pid))
	if err != nil {
		return 0, 0, false
	}
	defer syscall.CloseHandle(h)

	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return 0, 0, false
	}
	// A file time counts hundreds of nanoseconds.
	cpu = time.Duration(filetimeTicks(kernel)+filetimeTicks(user)) * 100 * time.Nanosecond

	var counters processMemoryCounters
	counters.CB = uint32(unsafe.Sizeof(counters))
	r, _, _ := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&counters)), uintptr(counters.CB))
	if r != 0 {
		rss = uint64(counters.WorkingSetSize)
	}
	return cpu, rss, true
}

func filetimeTicks(f syscall.Filetime) int64 {
	return int64(f.HighDateTime)<<32 | int64(f.LowDateTime)
}

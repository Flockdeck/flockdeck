package session

import (
	"syscall"
	"time"
	"unsafe"
)

// The reading is taken through the plain syscall package rather than a helper
// module, because Flockdeck is a single binary with no cgo and the module's
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
//
// Taking the snapshot is documented as able to fail outright while processes
// are starting and ending, which on a machine running several agents is all
// the time, so a failure is retried rather than taken for an answer.
func procParents() map[int]int {
	for attempt := 0; attempt < 3; attempt++ {
		if out := snapshotParents(); out != nil {
			return out
		}
	}
	return nil
}

func snapshotParents() map[int]int {
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

// procMetrics returns what the operating system says about one process. ok is
// false once it is gone, or when this process is not allowed to ask.
func procMetrics(pid int) (procMetric, bool) {
	// PROCESS_QUERY_LIMITED_INFORMATION is enough for both calls and is
	// granted for processes a fuller query would be refused for.
	const queryLimitedInformation = 0x1000
	h, err := syscall.OpenProcess(queryLimitedInformation, false, uint32(pid))
	if err != nil {
		return procMetric{}, false
	}
	defer syscall.CloseHandle(h)

	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return procMetric{}, false
	}
	m := procMetric{
		// A file time counts hundreds of nanoseconds.
		cpu: time.Duration(filetimeTicks(kernel)+filetimeTicks(user)) * 100 * time.Nanosecond,
		// The creation time is what says a process claiming this pane's
		// process as its parent really is its child.
		started: uint64(filetimeTicks(creation)),
	}

	var counters processMemoryCounters
	counters.CB = uint32(unsafe.Sizeof(counters))
	r, _, _ := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&counters)), uintptr(counters.CB))
	if r != 0 {
		m.rss = uint64(counters.WorkingSetSize)
	}
	return m, true
}

func filetimeTicks(f syscall.Filetime) int64 {
	return int64(f.HighDateTime)<<32 | int64(f.LowDateTime)
}

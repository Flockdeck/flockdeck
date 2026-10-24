package depth

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// osLookup takes one snapshot of the process table, which gives each process's
// parent and program, and asks Windows for the start time of each process it is
// asked about. The snapshot can hold a parent number that now belongs to
// another process; the start times are what show that.
func osLookup() Lookup {
	table := map[int]Proc{}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		table[int(pe.ProcessID)] = Proc{Parent: int(pe.ParentProcessID), Name: windows.UTF16ToString(pe.ExeFile[:])}
	}
	return func(pid int) (Proc, bool) {
		p, ok := table[pid]
		if !ok {
			return Proc{}, false
		}
		p.Start = startTime(uint32(pid))
		return p, true
	}
}

// startTime is when the process started, as 100-nanosecond ticks since 1601, or
// 0 when it cannot be read (it has gone, or may not be opened).
func startTime(pid uint32) int64 {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(h)
	var created, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exit, &kernel, &user); err != nil {
		return 0
	}
	return int64(created.HighDateTime)<<32 | int64(created.LowDateTime)
}

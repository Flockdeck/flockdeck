package proctree

import (
	"syscall"
	"unsafe"
)

// Parents returns the parent of every process this one can see.
//
// Taking the snapshot is documented as able to fail outright while processes
// are starting and ending, which on a machine running several agents is all
// the time, so a failure is retried rather than taken for an answer.
func Parents() map[int]int {
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

// FiletimeTicks is a file time in hundreds of nanoseconds.
func FiletimeTicks(f syscall.Filetime) int64 {
	return int64(f.HighDateTime)<<32 | int64(f.LowDateTime)
}

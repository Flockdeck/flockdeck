package depth

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// osLookup takes one snapshot of the process table and answers from it.
func osLookup() Lookup {
	type entry struct {
		parent int
		name   string
	}
	table := map[int]entry{}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		table[int(pe.ProcessID)] = entry{int(pe.ParentProcessID), windows.UTF16ToString(pe.ExeFile[:])}
	}
	return func(pid int) (int, string, bool) {
		e, ok := table[pid]
		return e.parent, e.name, ok
	}
}

package depth

import (
	"bytes"

	"golang.org/x/sys/unix"
)

func osLookup() Lookup {
	return func(pid int) (Proc, bool) {
		k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if err != nil || int(k.Proc.P_pid) != pid {
			return Proc{}, false
		}
		comm := k.Proc.P_comm[:]
		if i := bytes.IndexByte(comm, 0); i >= 0 {
			comm = comm[:i]
		}
		start := int64(k.Proc.P_starttime.Sec)*1_000_000 + int64(k.Proc.P_starttime.Usec)
		return Proc{Parent: int(k.Eproc.Ppid), Name: string(comm), Start: start}, true
	}
}

package depth

import (
	"bytes"

	"golang.org/x/sys/unix"
)

func osLookup() Lookup {
	return func(pid int) (int, string, bool) {
		k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if err != nil || int(k.Proc.P_pid) != pid {
			return 0, "", false
		}
		comm := k.Proc.P_comm[:]
		if i := bytes.IndexByte(comm, 0); i >= 0 {
			comm = comm[:i]
		}
		return int(k.Eproc.Ppid), string(comm), true
	}
}

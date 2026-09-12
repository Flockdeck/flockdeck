package session

import "syscall"

// shortPath is a path's 8.3 short name, or "" where the volume keeps none.
//
// It is what lets one command line name a program under a folder with a space
// in it -- C:\Users\Jo Smith, C:\Program Files -- for both of the shells Claude
// Code may run it through: no quoting is read the same way by Git Bash and by
// PowerShell, and a short name needs none.
func shortPath(long string) string {
	in, err := syscall.UTF16PtrFromString(long)
	if err != nil {
		return ""
	}
	buf := make([]uint16, 512)
	for {
		n, err := syscall.GetShortPathName(in, &buf[0], uint32(len(buf)))
		if err != nil || n == 0 {
			return ""
		}
		if int(n) <= len(buf) {
			return syscall.UTF16ToString(buf[:n])
		}
		buf = make([]uint16, n)
	}
}

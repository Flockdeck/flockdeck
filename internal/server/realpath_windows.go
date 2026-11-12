package server

import (
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var procGetFinalPathNameByHandleW = syscall.NewLazyDLL("kernel32.dll").NewProc("GetFinalPathNameByHandleW")

// realPath is where path really is. filepath.EvalSymlinks follows symbolic
// links but not directory junctions, which a user can make without any
// privilege, so the system's own name for an open handle is asked first: it has
// been through every kind of link. A path that cannot be opened is resolved as
// far as EvalSymlinks can.
func realPath(path string) string {
	if name, err := syscall.UTF16PtrFromString(path); err == nil {
		h, err := syscall.CreateFile(name, 0,
			syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
			nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if err == nil {
			defer syscall.CloseHandle(h)
			if s, ok := finalPathName(h); ok {
				return s
			}
		}
	}
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return path
}

func finalPathName(h syscall.Handle) (string, bool) {
	buf := make([]uint16, syscall.MAX_PATH)
	for {
		n, _, _ := procGetFinalPathNameByHandleW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0)
		if n == 0 {
			return "", false
		}
		if int(n) < len(buf) {
			break
		}
		buf = make([]uint16, n)
	}
	s := syscall.UTF16ToString(buf)
	switch {
	case strings.HasPrefix(s, `\?\UNC\`):
		s = `\` + s[len(`\?\UNC\`):]
	case strings.HasPrefix(s, `\?\`):
		s = s[len(`\?\`):]
	}
	return filepath.Clean(s), true
}

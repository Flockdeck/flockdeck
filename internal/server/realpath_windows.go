package server

import (
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var procGetFinalPathNameByHandleW = syscall.NewLazyDLL("kernel32.dll").NewProc("GetFinalPathNameByHandleW")

// resolveExisting is where an existing path really is. filepath.EvalSymlinks
// follows symbolic links but not directory junctions, which a user can make
// without any privilege, so the system's own name for an open handle is asked
// first: it has been through every kind of link. A path that cannot be opened
// is resolved as far as EvalSymlinks can; ok is false if neither can.
func resolveExisting(path string) (string, bool) {
	if name, err := syscall.UTF16PtrFromString(path); err == nil {
		h, err := syscall.CreateFile(name, 0,
			syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
			nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if err == nil {
			defer syscall.CloseHandle(h)
			if s, ok := finalPathName(h); ok {
				return s, true
			}
		}
	}
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r, true
	}
	return "", false
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
	return filepath.Clean(trimVerbatimPrefix(syscall.UTF16ToString(buf))), true
}

// trimVerbatimPrefix turns the name GetFinalPathNameByHandle gives back into an
// ordinary one: the verbatim form of a drive path loses its prefix, and the
// verbatim form of a share becomes the usual two-backslash UNC path. The
// prefixes are built from one backslash so that none is typed doubled.
func trimVerbatimPrefix(s string) string {
	const bs = "\x5c"
	verbatim := bs + bs + "?" + bs
	unc := verbatim + "UNC" + bs
	switch {
	case strings.HasPrefix(s, unc):
		return bs + bs + s[len(unc):]
	case strings.HasPrefix(s, verbatim):
		return s[len(verbatim):]
	}
	return s
}

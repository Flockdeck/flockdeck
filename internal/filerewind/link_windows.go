package filerewind

import "syscall"

// isLink says path is a symbolic link, a junction or another reparse point.
// Go's Lstat reports a junction as a plain folder, so the attribute is read.
func isLink(path string) bool {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return true
	}
	attr, err := syscall.GetFileAttributes(p)
	if err != nil {
		return false
	}
	return attr&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

package main

import (
	"os"
	"syscall"
	"unsafe"
)

var procGetFileInformationByHandleEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetFileInformationByHandleEx")

// fileNameInfo is the class of GetFileInformationByHandleEx that names a file.
const fileNameInfo = 2

// isMsysTerminal reports whether f is the terminal of mintty -- Git Bash's
// window -- or another Cygwin or MSYS2 terminal. Those give a program pipes
// rather than a console, and the pipe's name, which says which pty is behind
// it, is the only thing that tells a person typing into it from a script.
func isMsysTerminal(f *os.File) bool {
	var buf [4 + 2*syscall.MAX_PATH]byte
	ok, _, _ := procGetFileInformationByHandleEx.Call(f.Fd(), fileNameInfo, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if ok == 0 {
		return false
	}
	n := *(*uint32)(unsafe.Pointer(&buf[0])) / 2
	if n == 0 || n > syscall.MAX_PATH {
		return false
	}
	name := (*[syscall.MAX_PATH]uint16)(unsafe.Pointer(&buf[4]))[:n:n]
	return isMsysPtyName(syscall.UTF16ToString(name))
}

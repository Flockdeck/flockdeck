package tool

import (
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var procMultiByteToWideChar = syscall.NewLazyDLL("kernel32.dll").NewProc("MultiByteToWideChar")

// cpOEM is the console's own code page, which is what cmd.exe and most older
// console programs write into a pipe: 850 or 437 on a Western machine, never
// UTF-8 unless somebody set it.
const cpOEM = 1

// oemText is b decoded from the console's code page, for output that is not
// UTF-8. A console program writing "café" into a pipe writes 63 61 66 82 on a
// machine using code page 850, which as UTF-8 is a replacement character: a
// file name the model is told of and then cannot open.
func oemText(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", false
	}
	n, _, _ := procMultiByteToWideChar.Call(cpOEM, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), 0, 0)
	if n == 0 {
		return "", false
	}
	wide := make([]uint16, n)
	if got, _, _ := procMultiByteToWideChar.Call(cpOEM, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)),
		uintptr(unsafe.Pointer(&wide[0])), n); got == 0 {
		return "", false
	}
	return string(utf16.Decode(wide[:n])), true
}

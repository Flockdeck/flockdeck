//go:build windows

package appwindow

import (
	"syscall"
	"unsafe"
)

// workArea is the size of the primary screen less the taskbar, or zeroes when
// it cannot be had. The process is not DPI-aware, so the answer comes in the
// same scaled pixels Chromium reads --window-size in.
func workArea() (int, int) {
	var r struct{ Left, Top, Right, Bottom int32 }
	const getWorkArea = 0x0030 // SPI_GETWORKAREA
	ok, _, _ := syscall.NewLazyDLL("user32.dll").NewProc("SystemParametersInfoW").Call(
		getWorkArea, 0, uintptr(unsafe.Pointer(&r)), 0)
	if ok == 0 {
		return 0, 0
	}
	return int(r.Right - r.Left), int(r.Bottom - r.Top)
}

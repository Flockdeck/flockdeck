package chat

import (
	"syscall"
	"unsafe"
)

var procGetConsoleScreenBufferInfo = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleScreenBufferInfo")

type consoleCoord struct{ X, Y int16 }

type consoleRect struct{ Left, Top, Right, Bottom int16 }

type consoleScreenBufferInfo struct {
	Size              consoleCoord
	CursorPosition    consoleCoord
	Attributes        uint16
	Window            consoleRect
	MaximumWindowSize consoleCoord
}

// terminalWidth is how many columns the console standard output is drawn in
// shows, or 0 when it is not a console. A pane is one, through ConPTY.
func terminalWidth() int {
	h, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil {
		return 0
	}
	var info consoleScreenBufferInfo
	if ok, _, _ := procGetConsoleScreenBufferInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&info))); ok == 0 {
		return 0
	}
	return int(info.Window.Right-info.Window.Left) + 1
}

package chat

import (
	"syscall"
	"unsafe"
)

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
	procSetConsoleMode             = kernel32.NewProc("SetConsoleMode")
)

// enableVirtualTerminalProcessing is the console mode that draws escape
// sequences rather than printing them.
const enableVirtualTerminalProcessing = 0x0004

// enableColour asks the console to draw escape sequences, and reports whether
// it will. A pane's console already does; a console window opened by hand from
// cmd.exe has it off, and would print every colour as a row of codes.
func enableColour() bool {
	h, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil {
		return false
	}
	var mode uint32
	if err := syscall.GetConsoleMode(h, &mode); err != nil {
		return false
	}
	if mode&enableVirtualTerminalProcessing != 0 {
		return true
	}
	ok, _, _ := procSetConsoleMode.Call(uintptr(h), uintptr(mode|enableVirtualTerminalProcessing))
	return ok != 0
}

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

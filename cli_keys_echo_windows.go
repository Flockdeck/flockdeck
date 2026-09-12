package main

import (
	"os"
	"syscall"
)

var procSetConsoleMode = syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleMode")

// enableEchoInput is the console mode that shows what is typed.
const enableEchoInput = 0x0004

// hideInput stops the console showing what is typed on standard input, and
// returns what puts it back, or nil where it could not.
func hideInput() func() {
	h := syscall.Handle(os.Stdin.Fd())
	var mode uint32
	if err := syscall.GetConsoleMode(h, &mode); err != nil {
		return nil
	}
	if ok, _, _ := procSetConsoleMode.Call(uintptr(h), uintptr(mode&^enableEchoInput)); ok == 0 {
		return nil
	}
	return func() { procSetConsoleMode.Call(uintptr(h), uintptr(mode)) }
}

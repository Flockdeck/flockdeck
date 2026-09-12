//go:build windows

package main

import (
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procGetMessageW      = user32.NewProc("GetMessageW")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmQueryEndSession = 0x0011
	wmEndSession      = 0x0016
)

// wndClassEx is WNDCLASSEXW.
type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   syscall.Handle
	icon       syscall.Handle
	cursor     syscall.Handle
	background syscall.Handle
	menuName   *uint16
	className  *uint16
	iconSm     syscall.Handle
}

// winMsg is MSG.
type winMsg struct {
	hwnd    syscall.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
	private uint32
}

// endSession is what the window does when the session ends. There is one
// watch in a run.
var endSession struct {
	stop func()
	done <-chan struct{}
}

var (
	endSessionClass     sync.Once
	endSessionClassName *uint16
	endSessionProcPtr   uintptr
)

// watchEndSession makes a logoff or a shutdown take the orderly stop, and
// waits for the save it makes before letting Windows go on. It returns the
// window it listens with, or 0 when none could be made.
//
// Windows tells a program the session is ending through its console, which
// Go's runtime turns into SIGTERM, or through its top-level windows. The
// release has no console once start-up is over, and the window on screen is
// the browser's, so Flockdeck heard nothing: it was ended where it stood,
// with every change to the layout since it started unsaved. So it keeps a
// window of its own, never shown, for the one message.
func watchEndSession(stop func(), done <-chan struct{}) syscall.Handle {
	endSession.stop, endSession.done = stop, done
	ready := make(chan syscall.Handle)
	go func() {
		// A window's messages go to the thread that made it.
		runtime.LockOSThread()
		hwnd := newEndSessionWindow()
		ready <- hwnd
		if hwnd == 0 {
			return
		}
		var m winMsg
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
			_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
	}()
	return <-ready
}

func newEndSessionWindow() syscall.Handle {
	instance, _, _ := procGetModuleHandleW.Call(0)
	endSessionClass.Do(func() {
		endSessionClassName, _ = syscall.UTF16PtrFromString("FlockdeckEndSession")
		endSessionProcPtr = syscall.NewCallback(endSessionProc)
		wc := wndClassEx{
			wndProc:   endSessionProcPtr,
			instance:  syscall.Handle(instance),
			className: endSessionClassName,
		}
		wc.size = uint32(unsafe.Sizeof(wc))
		_, _, _ = procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	})
	// A top-level window, since the end of a session is only sent to those,
	// and never shown.
	hwnd, _, _ := procCreateWindowExW.Call(0,
		uintptr(unsafe.Pointer(endSessionClassName)), uintptr(unsafe.Pointer(endSessionClassName)),
		0, 0, 0, 0, 0, 0, 0, instance, 0)
	return syscall.Handle(hwnd)
}

// endSessionProc agrees to the session ending, and once it does end asks
// for the orderly stop and answers only when the layout has been saved, or
// the stop's own deadline has passed: Windows may end the process as soon
// as the answer is given.
func endSessionProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmQueryEndSession:
		return 1
	case wmEndSession:
		if wParam != 0 {
			endSession.stop()
			select {
			case <-endSession.done:
			case <-time.After(shutdownGrace):
			}
		}
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}

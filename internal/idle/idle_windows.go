//go:build windows

package idle

import (
	"syscall"
	"time"
	"unsafe"
)

// The reading is taken through the plain syscall package rather than a
// helper module, as internal/session/usage_windows.go's is: Flockdeck is a
// single binary with no cgo and the module's dependencies are fixed.

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procGetLastInputInfo = user32.NewProc("GetLastInputInfo")
	procOpenInputDesktop = user32.NewProc("OpenInputDesktop")
	procSwitchDesktop    = user32.NewProc("SwitchDesktop")
	procCloseDesktop     = user32.NewProc("CloseDesktop")
	procGetTickCount     = kernel32.NewProc("GetTickCount")
)

// lastInputInfo is LASTINPUTINFO.
type lastInputInfo struct {
	cbSize uint32
	dwTime uint32
}

// desktopSwitchDesktop is DESKTOP_SWITCHDESKTOP, the one access right locked
// asks of the input desktop: enough to tell whether it can be switched to,
// which the secure desktop -- the lock screen, or a UAC prompt -- refuses.
const desktopSwitchDesktop = 0x0100

// Since reads GetLastInputInfo, which keyboard or mouse input anywhere on the
// machine sets, not any one window's own idea of itself.
func Since() (time.Duration, bool, bool) {
	var info lastInputInfo
	info.cbSize = uint32(unsafe.Sizeof(info))
	r, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 0, false, false
	}
	tick, _, _ := procGetTickCount.Call()
	return sinceTicks(uint32(tick), info.dwTime), locked(), true
}

// sinceTicks is Since's arithmetic, pulled out so it can be tested without
// the syscalls. GetLastInputInfo's dwTime and GetTickCount both wrap at 2^32
// milliseconds -- about 49.7 days -- so the difference is taken as a uint32,
// which comes out right across the wrap the same way any unsigned clock
// arithmetic does.
func sinceTicks(now, lastInput uint32) time.Duration {
	return time.Duration(now-lastInput) * time.Millisecond
}

// locked reports whether the secure desktop is up. OpenInputDesktop failing
// to open the desktop currently receiving input, or SwitchDesktop failing to
// switch to it once opened, both mean it is the lock screen or a UAC prompt,
// which this process has no rights over, rather than the desk.
func locked() bool {
	h, _, _ := procOpenInputDesktop.Call(0, 0, desktopSwitchDesktop)
	if h == 0 {
		return true
	}
	defer procCloseDesktop.Call(h)
	ok, _, _ := procSwitchDesktop.Call(h)
	return ok == 0
}

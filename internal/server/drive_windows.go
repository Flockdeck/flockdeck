//go:build windows

package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetDriveType   = kernel32.NewProc("GetDriveTypeW")
	procQueryDosDevice = kernel32.NewProc("QueryDosDeviceW")
	procFinalPath      = kernel32.NewProc("GetFinalPathNameByHandleW")
)

// GetDriveType's answers that matter here.
const (
	driveUnknown = 0
	driveNoRoot  = 1
	driveRemote  = 4
)

// localVolume refuses a path whose drive letter is not a local disk: a drive
// mapped to a network share, which looks local in the name and is not, a drive
// made with subst, which points at some other folder that may be anywhere, and a
// letter with nothing behind it. A path with no drive letter is left to the
// checks on its name.
func localVolume(path string) error {
	vol := filepath.VolumeName(filepath.Clean(path))
	if len(vol) != 2 || vol[1] != ':' {
		return nil
	}
	root, err := syscall.UTF16PtrFromString(vol + `\`)
	if err != nil {
		return err
	}
	switch t, _, _ := procGetDriveType.Call(uintptr(unsafe.Pointer(root))); t {
	case driveRemote:
		return fmt.Errorf("%s is a drive mapped to a network share, which a baton is not read from", vol)
	case driveUnknown, driveNoRoot:
		return fmt.Errorf("%s is not a drive that can be read", vol)
	}
	name, err := syscall.UTF16PtrFromString(vol)
	if err != nil {
		return err
	}
	buf := make([]uint16, 1024)
	n, _, _ := procQueryDosDevice.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n > 0 {
		// A local disk is \Device\HarddiskVolumeN; a drive made with subst is
		// \??\ and the folder it stands for.
		if target := syscall.UTF16ToString(buf); strings.HasPrefix(target, `\??\`) {
			return fmt.Errorf("%s is a drive made with subst, which stands for a folder that may not be local", vol)
		}
	}
	return nil
}

// verifyOpened asks Windows where an open file really is, and refuses one that
// is on a share or a drive that is not local. The name was checked before the
// open, and a folder swapped for a junction between the two would not show in it:
// the handle's final path does.
func verifyOpened(f *os.File) error {
	// VOLUME_NAME_DOS (0): the drive letter form, \\?\C:\..., or \\?\UNC\... for a
	// share. A path longer than the buffer is asked for again with one that holds
	// it: the call then returns the size needed, and only a return of 0 has an
	// error to read, so a long path is answered and not refused with a stale error.
	buf := make([]uint16, 1024)
	var n uintptr
	for range 2 {
		var errno error
		n, _, errno = procFinalPath.Call(f.Fd(), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0)
		if n == 0 {
			return fmt.Errorf("could not tell where %s is: %v", filepath.Base(f.Name()), errno)
		}
		if int(n) < len(buf) {
			break
		}
		if n > 32768 {
			return fmt.Errorf("the path of %s is longer than Windows allows", filepath.Base(f.Name()))
		}
		buf = make([]uint16, n+1)
	}
	if int(n) >= len(buf) {
		return fmt.Errorf("could not tell where %s is: its path kept growing", filepath.Base(f.Name()))
	}
	final := syscall.UTF16ToString(buf[:n])
	switch {
	case strings.HasPrefix(final, `\\?\UNC\`):
		return fmt.Errorf("%s turned out to be on a network share, which a baton is not read from", filepath.Base(f.Name()))
	case strings.HasPrefix(final, `\\?\`):
		final = strings.TrimPrefix(final, `\\?\`)
	}
	if networkPath(final) {
		return fmt.Errorf("%s turned out to be on a network or device path, which a baton is not read from", filepath.Base(f.Name()))
	}
	return localVolume(final)
}

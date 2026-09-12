//go:build windows

package store

import "syscall"

// lockFile opens path with no sharing at all, which is the lock: any other
// open of it, from this process or another, is refused with a sharing
// violation until the handle is closed.
func lockFile(path string) (unlock func(), err error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil,
		syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if heldByAnother(err) {
			return nil, ErrStartLocked
		}
		return nil, err
	}
	return func() { _ = syscall.CloseHandle(h) }, nil
}

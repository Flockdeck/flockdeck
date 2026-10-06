//go:build windows

package baton

import (
	"io"
	"os"
	"syscall"
)

// openShared opens a file to be read with every kind of sharing, which allows what the open
// of os.Open (no sharing of deletion) does not.
func openShared(path string) (io.ReadCloser, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

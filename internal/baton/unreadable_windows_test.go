//go:build windows

package baton

import (
	"syscall"
	"testing"
)

// makeUnreadable holds the file open with no sharing until the test ends, so that another
// open of it, to read it, is refused: a file that exists and cannot be read.
func makeUnreadable(t *testing.T, path string) {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Skipf("the file could not be held open: %v", err)
	}
	t.Cleanup(func() { syscall.CloseHandle(h) })
}

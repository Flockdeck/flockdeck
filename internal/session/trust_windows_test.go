package session

import (
	"syscall"
	"testing"
)

// lockAgainstReading holds a file open with no sharing until the test ends,
// which is how a file comes to be unreadable on Windows: permission bits do
// not stop a read there.
func lockAgainstReading(t *testing.T, path string) bool {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("lock %s: %v", path, err)
	}
	t.Cleanup(func() { syscall.CloseHandle(h) })
	return true
}

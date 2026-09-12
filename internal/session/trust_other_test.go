//go:build !windows

package session

import (
	"os"
	"testing"
)

// lockAgainstReading takes away every permission on a file until the test
// ends. It reports false where that does not stop a read, as for root.
func lockAgainstReading(t *testing.T, path string) bool {
	t.Helper()
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o644) })
	f, err := os.Open(path)
	if err == nil {
		f.Close()
		return false
	}
	return true
}

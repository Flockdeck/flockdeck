//go:build !windows

package workspace

import (
	"os"
	"testing"
)

// makeUnreadable takes every permission off the file: it exists and cannot be read.
func makeUnreadable(t *testing.T, path string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o600) })
}

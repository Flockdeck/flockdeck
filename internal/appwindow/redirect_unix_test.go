//go:build !windows

package appwindow

import (
	"os"
	"path/filepath"
	"testing"
)

// A redirect file carries a one-time link good for a minute, in the moment
// after it is written and before the browser has read it -- exactly what
// R3.7.2 is about. Its directory and the file itself are locked to this
// user alone: mode 0700 and 0600, regardless of what MkdirAll or WriteFile's
// own mode ended up as once a parent folder or the process's umask had a
// say.
func TestWriteRedirectFileLocksDownItsDirectoryAndFile(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(base, "run"))
	if err := os.MkdirAll(filepath.Join(base, "run"), 0o700); err != nil {
		t.Fatal(err)
	}

	_, path, ok := writeRedirectFile("http://127.0.0.1:1/?w=abc", "", kindNormal)
	if !ok {
		t.Fatal("writeRedirectFile did not find a directory")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 0600", perm)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("directory mode = %o, want 0700", perm)
	}
}

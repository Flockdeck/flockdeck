package workspace

import (
	"path/filepath"
	"runtime"
	"testing"
)

// TestPathsFoldCaseOnlyWhereTheFilesystemDoes covers sameDir, which ignored
// case on every system. On Linux two directories whose names differ only in
// case are two projects, and treating them as one opened the wrong one; the
// store, which keys each project's layout, already kept them apart.
func TestPathsFoldCaseOnlyWhereTheFilesystemDoes(t *testing.T) {
	was := foldsCase
	t.Cleanup(func() { foldsCase = was })

	upper, lower := filepath.Join("code", "Api"), filepath.Join("code", "api")
	sub := filepath.Join(upper, "cmd")

	foldsCase = false
	if sameDir(upper, lower) {
		t.Errorf("sameDir(%q, %q) where case matters = true, want two directories", upper, lower)
	}
	if !sameDir(upper, upper+string(filepath.Separator)) {
		t.Errorf("a trailing separator made %q a different directory", upper)
	}
	// filepath.Rel, which underDir asks, folds case itself on Windows, so only
	// elsewhere can it be asked about a system that does not.
	if runtime.GOOS != "windows" && underDir(sub, lower) {
		t.Errorf("underDir(%q, %q) where case matters = true, want it outside", sub, lower)
	}

	foldsCase = true
	if !sameDir(upper, lower) {
		t.Errorf("sameDir(%q, %q) where case is folded = false, want one directory", upper, lower)
	}
	if !underDir(sub, lower) {
		t.Errorf("underDir(%q, %q) where case is folded = false, want it inside", sub, lower)
	}
}

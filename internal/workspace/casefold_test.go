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

// TestSameDirKeepsApartLettersTheFilesystemDoes covers names that
// strings.ToLower joined but NTFS keeps apart: the dotted capital I and a
// plain i are two folders, while ordinary accented letters still fold.
func TestSameDirKeepsApartLettersTheFilesystemDoes(t *testing.T) {
	was := foldsCase
	t.Cleanup(func() { foldsCase = was })
	foldsCase = true

	if sameDir(filepath.Join("work", "\u0130app"), filepath.Join("work", "iapp")) {
		t.Error("a dotted capital I folded onto a plain i")
	}
	if sameDir(filepath.Join("work", "\u0131app"), filepath.Join("work", "Iapp")) {
		t.Error("a dotless i folded onto I")
	}
	if !sameDir(filepath.Join("work", "\u00c9tude"), filepath.Join("work", "\u00e9tude")) {
		t.Error("an accented letter no longer folds")
	}
}

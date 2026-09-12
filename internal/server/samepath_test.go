package server

import (
	"path/filepath"
	"testing"
)

// TestSamePathFollowsWhetherTheFilesystemCaresAboutCase covers how a pane on
// another project's tab is told apart. The snapshot names that project on the
// pane's header, and it compared the two directories without regard to case
// everywhere -- so on Linux, where App and app are two projects side by side,
// a pane from one on the other's tab was taken for one of its own and named
// nothing.
func TestSamePathFollowsWhetherTheFilesystemCaresAboutCase(t *testing.T) {
	was := foldPathCase
	t.Cleanup(func() { foldPathCase = was })
	upper, lower := filepath.FromSlash("/work/App"), filepath.FromSlash("/work/app")

	foldPathCase = true
	if !samePath(upper, lower) {
		t.Error("where case does not matter, App and app are one directory")
	}
	foldPathCase = false
	if samePath(upper, lower) {
		t.Error("where case matters, App and app are two directories")
	}
	if !samePath(lower, lower) {
		t.Error("a directory is not the same as itself")
	}
}

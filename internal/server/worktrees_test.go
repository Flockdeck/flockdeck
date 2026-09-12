package server

import (
	"runtime"
	"testing"
)

// TestUnderPathFollowsWhetherTheFilesystemCaresAboutCase covers which panes the
// worktree panel counts as working in a checkout, which is also what stops a
// removal from pulling a directory out from under an agent. A Mac folds case
// as Windows does, so a pane opened under another spelling of the path is in
// the checkout there; on Linux the other spelling is another directory.
func TestUnderPathFollowsWhetherTheFilesystemCaresAboutCase(t *testing.T) {
	was := foldPathCase
	t.Cleanup(func() { foldPathCase = was })

	foldPathCase = true
	if !underPath("/Users/me/Code/App/sub", "/Users/me/code/app") {
		t.Error("a pane under another spelling of the worktree was not counted where case is folded")
	}
	// filepath.Rel folds case itself on Windows, so the other answer can only
	// be seen where it does not.
	if runtime.GOOS == "windows" {
		return
	}
	foldPathCase = false
	if underPath("/home/me/Code/App/sub", "/home/me/code/app") {
		t.Error("a directory differing in case was taken for the worktree where case matters")
	}
}

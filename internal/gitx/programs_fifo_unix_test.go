//go:build !windows

package gitx

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A pipe where a file is read waits for a writer that never comes. It must be
// refused, and must not hold the scan or the slots of any other scan.
func TestAPipeWhereASubmodulesGitFileShouldBeIsNotOpened(t *testing.T) {
	outer := newRepo(t)
	mod := addSubmodule(t, outer)
	dot := filepath.Join(mod, ".git")
	if err := os.Remove(dot); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(dot, 0o600); err != nil {
		t.Skipf("no pipe can be made here: %v", err)
	}
	// Git would wait on it too, in any command run in the checkout afterwards.
	t.Cleanup(func() { os.Remove(dot) })
	start := time.Now()
	rep := scan(t, outer)
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("a scan with a pipe for a .git file took %s", d)
	}
	if !hasItem(rep, "not an ordinary file") {
		t.Errorf("no item says the pipe was not opened: %+v", rep.Items)
	}
	if n := leaked.Load(); n != 0 {
		t.Errorf("%d calls were left waiting on the pipe", n)
	}
}

func TestAPipeWhereARemotesCommondirShouldBeIsNotOpened(t *testing.T) {
	repo := newRepo(t)
	remote := newRepo(t)
	fifo := filepath.Join(remote, ".git", "commondir")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("no pipe can be made here: %v", err)
	}
	t.Cleanup(func() { os.Remove(fifo) })
	gitRun(t, repo, "remote", "add", "origin", filepath.ToSlash(remote))
	start := time.Now()
	rep := scan(t, repo)
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("a scan with a pipe for commondir took %s", d)
	}
	if !hasItem(rep, "not an ordinary file") {
		t.Errorf("no item says the pipe was not opened: %+v", rep.Items)
	}
}

package gitx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestAGitGivenUpOnIsEndedWithEverythingItStarted covers the deadline on
// Windows, where the git on PATH is a wrapper that starts the real git. The
// deadline killed the wrapper and left the real one running, and a checkout
// that hung gained another every refresh.
//
// git is asked to read its input from a pipe nobody writes to, so it waits
// until it is given up on. Whatever is still running afterwards still holds
// the pipe's other end, and a write into the pipe goes on succeeding: once
// everything git started has gone, the write fails.
func TestAGitGivenUpOnIsEndedWithEverythingItStarted(t *testing.T) {
	repo := newRepo(t)
	throughTheWrapper(t)
	in, feed, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// Closing the writing end also lets go of a git left behind, which then
	// reads the end of its input and exits.
	defer feed.Close()

	var out bytes.Buffer
	_, err = runTo(context.Background(), time.Second, repo, in, &out, "cat-file", "--batch")
	in.Close()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("git waiting on its input was answered %v, want it given up on", err)
	}
	select {
	case <-Exited(err):
	case <-time.After(10 * time.Second):
		t.Fatal("what git started was still running ten seconds after it was given up on")
	}
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if _, err := feed.Write([]byte("\n")); err != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("a git process was still reading git's input three seconds after every process it started was said to have gone")
		}
	}
}

// throughTheWrapper puts Git for Windows' cmd\git.exe first on PATH where
// there is one. It is the git a program started from the Start menu finds, and
// the one this is about: a terminal of Git's own finds the real git first,
// which leaves nothing behind when it is killed.
func throughTheWrapper(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		return
	}
	out, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		return
	}
	// The exec path is <top>\mingw64\libexec\git-core, and the wrapper is
	// <top>\cmd\git.exe.
	top := filepath.Join(filepath.FromSlash(strings.TrimSpace(string(out))), "..", "..", "..")
	wrapper := filepath.Join(top, "cmd")
	if _, err := os.Stat(filepath.Join(wrapper, "git.exe")); err != nil {
		t.Logf("no Git for Windows wrapper beside %s; testing the git on PATH", top)
		return
	}
	t.Setenv("PATH", wrapper+string(os.PathListSeparator)+os.Getenv("PATH"))
}

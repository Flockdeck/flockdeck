package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestStatusWithinSaysWhenGitDidNotAnswer makes a checkout slow on purpose,
// with a clean filter that does not return until it is let go: git status runs
// it on a tracked file whose times have changed, to see whether its content
// has, and waits for it. (It was an fsmonitor hook, which git run by Flockdeck
// no longer runs.) The read has to be given up on at its deadline and say it
// was, rather than come back empty like a checkout git had nothing to say
// about: the pane headers tell the two apart.
func TestStatusWithinSaysWhenGitDidNotAnswer(t *testing.T) {
	t.Parallel()
	if !Available() {
		t.Skip("git is not installed")
	}
	repo := newRepo(t)
	if _, err := StatusWithin(repo, commandTimeout); err != nil {
		t.Fatalf("an ordinary checkout: %v", err)
	}

	base := t.TempDir()
	release := filepath.Join(base, "release")
	gone := filepath.Join(base, "gone")
	hook := filepath.Join(base, "clean.sh")
	// Bounded in any case, so a filter nobody lets go of still ends.
	script := "#!/bin/sh\ncat >/dev/null\ni=0\nwhile [ ! -e '" + filepath.ToSlash(release) + "' ] && [ $i -lt 600 ]; do sleep 0.05; i=$((i+1)); done\n" +
		": > '" + filepath.ToSlash(gone) + "'\n"
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// Registered after both directories, so it runs before either is removed:
	// the filter is sitting in the checkout.
	// A process sitting in a directory keeps Windows from removing it, and a
	// shell there can take seconds to notice and go, so the filter says when
	// it has finished and that is waited for. On Windows giving up on git
	// ends the filter with it, which never says so, and there it is enough
	// that everything git started has gone.
	var err error
	t.Cleanup(func() {
		_ = os.WriteFile(release, nil, 0o644)
		for end := time.Now().Add(20 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			if _, err := os.Stat(gone); err == nil {
				return
			}
			select {
			case <-Exited(err):
				if runtime.GOOS == "windows" {
					return
				}
			default:
			}
		}
	})
	gitRun(t, repo, "config", "filter.slow.clean", "'"+filepath.ToSlash(hook)+"'")
	if err := os.WriteFile(filepath.Join(repo, ".git", "info", "attributes"), []byte("README.md filter=slow\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(repo, "README.md"), later, later); err != nil {
		t.Fatal(err)
	}

	const deadline = 300 * time.Millisecond
	start := time.Now()
	var st Status
	st, err = StatusWithin(repo, deadline)
	took := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a checkout git did not answer for in time gave err = %v, want a deadline exceeded", err)
	}
	if st != (Status{}) {
		t.Errorf("a read given up on still reported %+v", st)
	}
	// The hook, still running, holds git's error output once git is stopped,
	// and that is waited for too, for pipeGrace; nothing beyond it.
	if limit := deadline + pipeGrace + 3*time.Second; took > limit {
		t.Errorf("the read took %v, want it given up on well within %v", took, limit)
	}

	// And a failure that is not a deadline is not reported as one.
	if _, err := StatusWithin(t.TempDir(), commandTimeout); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a directory that is no repository gave err = %v, want an error that is not a deadline", err)
	}
}

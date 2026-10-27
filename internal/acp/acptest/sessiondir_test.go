package acptest

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func age(t *testing.T, path string, d time.Duration) {
	t.Helper()
	old := time.Now().Add(-d)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func mk(t *testing.T, root, name string, files ...string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestSweepStale(t *testing.T) {
	root := t.TempDir()
	oldEmpty := mk(t, root, "flockdeck-acp-1")
	oldMarked := mk(t, root, "flockdeck-acp-2", scratchMarker, "outcome.txt")
	oldForeign := mk(t, root, "flockdeck-acp-3", "mine.txt")
	freshEmpty := mk(t, root, "flockdeck-acp-4")
	freshMarked := mk(t, root, "flockdeck-acp-5", scratchMarker)
	otherName := mk(t, root, "something-else")
	for _, d := range []string{oldEmpty, oldMarked, oldForeign, otherName} {
		age(t, d, 48*time.Hour)
	}

	sweepStale(root, time.Now(), staleAfter)

	for _, gone := range []string{oldEmpty, oldMarked} {
		if exists(gone) {
			t.Errorf("%s was not swept", gone)
		}
	}
	for _, kept := range []string{oldForeign, freshEmpty, freshMarked, otherName} {
		if !exists(kept) {
			t.Errorf("%s was swept", kept)
		}
	}
}

func TestSweepStaleLeavesLinkedTargets(t *testing.T) {
	root, target := t.TempDir(), t.TempDir()
	keep := filepath.Join(target, "keep.txt")
	if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "flockdeck-acp-9")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	sweepStale(root, time.Now().Add(72*time.Hour), staleAfter)
	if !exists(keep) {
		t.Fatal("the target of a link was swept")
	}
}

func TestSessionDirMarksAndCleansUp(t *testing.T) {
	var dir string
	t.Run("inner", func(t *testing.T) {
		dir = SessionDir(t)
		if !exists(filepath.Join(dir, scratchMarker)) {
			t.Error("no marker in the session dir")
		}
		if err := os.WriteFile(filepath.Join(dir, "agent.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if exists(dir) {
		t.Fatalf("%s is left after the test", dir)
	}
}

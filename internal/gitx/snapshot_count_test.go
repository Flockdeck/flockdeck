package gitx

import (
	"context"
	"os"
	"sync"
	"testing"
)

// snapshotCommands are the git processes Snapshot itself runs on dir, which are what
// "copying a pane" costs: the candidate bases are chosen before, once for the project.
func snapshotCommands(t *testing.T, repo, dir string, dirty bool) ([]string, error) {
	t.Helper()
	common, err := CommonDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewScratch(common)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	base := BaseOf(ctx, repo)
	var mu sync.Mutex
	var cmds []string
	withHook(t, func(ctx context.Context, args []string) error {
		mu.Lock()
		cmds = append(cmds, args[0])
		mu.Unlock()
		return nil
	})
	_, err = Snapshot(ctx, dir, s, base, dirty)
	return cmds, err
}

// The documentation says how many git processes copying a pane is. This is where
// that is counted, so that the number cannot drift from the code.
func TestTheGitProcessesOfCopyingAPaneAreTheOnesTheDocsSay(t *testing.T) {
	repo, wt := radarRepo(t, "wa", "wb", "wc")
	editLine(t, wt["wa"], "a.txt", 4, "edited")
	write(t, wt["wa"], "new.txt", "x\n")
	editLine(t, wt["wb"], "a.txt", 4, "committed")
	commitAll(t, wt["wb"], "work")

	got, err := snapshotCommands(t, repo, wt["wa"], true)
	if err != nil || len(got) != 11 {
		t.Errorf("a pane with uncommitted work: %d processes (%v), err %v, want 11", len(got), got, err)
	}
	got, err = snapshotCommands(t, repo, wt["wb"], false)
	if err != nil || len(got) != 4 {
		t.Errorf("a pane with only commits: %d processes (%v), err %v, want 4", len(got), got, err)
	}

	// With the index not copyable the scratch index is built from HEAD: a process to ask whether the checkout is sparse, one to build it and two to read the gitlinks the checkout's own index holds, in place of the one that looks for unmerged files.
	old := copyIndexFn
	t.Cleanup(func() { copyIndexFn = old })
	copyIndexFn = func(from, to string) error { return os.ErrPermission }
	got, err = snapshotCommands(t, repo, wt["wa"], true)
	if err != nil || len(got) != 14 {
		t.Errorf("index rebuilt from HEAD: %d processes (%v), err %v, want 14", len(got), got, err)
	}
	gitRun(t, wt["wc"], "config", "core.sparseCheckout", "true")
	editLine(t, wt["wc"], "a.txt", 4, "edited")
	got, err = snapshotCommands(t, repo, wt["wc"], true)
	if err == nil || len(got) != 4 {
		t.Errorf("a sparse checkout whose index cannot be copied: %d processes (%v), err %v, want 4 and an error", len(got), got, err)
	}
}

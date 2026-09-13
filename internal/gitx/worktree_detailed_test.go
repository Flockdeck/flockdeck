package gitx

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestListDetailedAsksAboutAFewWorktreesAtATime covers the worktree panel on a
// repository a fan-out has left a dozen or more worktrees in. A git status
// was started for every one of them in the same instant, beside the agents
// working in them.
func TestListDetailedAsksAboutAFewWorktreesAtATime(t *testing.T) {
	repo := newRepo(t)
	const count = 2 * detailLookups
	for i := 0; i < count; i++ {
		path := filepath.Join(filepath.Dir(repo), fmt.Sprintf("%s-wt%02d", filepath.Base(repo), i))
		t.Cleanup(func() { os.RemoveAll(path) })
		gitRun(t, repo, "worktree", "add", "-b", fmt.Sprintf("wt%02d", i), path)
	}

	var inFlight, most atomic.Int32
	was := worktreeStatus
	worktreeStatus = func(string) Status {
		n := inFlight.Add(1)
		for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
		return Status{Branch: "read"}
	}
	t.Cleanup(func() { worktreeStatus = was })

	wts, err := ListDetailed(repo)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(wts) != count+1 {
		t.Fatalf("%d worktrees listed, want %d", len(wts), count+1)
	}
	for _, wt := range wts {
		if wt.Status.Branch != "read" {
			t.Errorf("%s was listed without its status", wt.Path)
		}
	}
	if got := most.Load(); got > detailLookups {
		t.Errorf("%d worktrees were asked about at once, want at most %d", got, detailLookups)
	}
}

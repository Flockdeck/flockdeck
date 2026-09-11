package workspace

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
)

// TestRefreshGitBoundsTheProcessesItStarts checks a refresh over many
// checkouts asks git about a few of them at a time. It started a process for
// every one at once, so a fan-out's dozen worktrees meant a dozen git processes
// starting together every refresh, beside the agents working in them.
func TestRefreshGitBoundsTheProcessesItStarts(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed, so nothing is refreshed")
	}
	w := &Workspace{panes: map[string]*Pane{}, BroadcastSet: map[string]bool{}}
	for i := 0; i < 3*branchLookups; i++ {
		id := fmt.Sprint("p", i)
		w.panes[id] = &Pane{ID: id, Cwd: fmt.Sprint("/checkout/", i)}
	}

	var inFlight, most atomic.Int32
	status := gitStatus
	gitStatus = func(string) gitx.Status {
		n := inFlight.Add(1)
		for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
		}
		time.Sleep(5 * time.Millisecond)
		inFlight.Add(-1)
		return gitx.Status{Branch: "trunk"}
	}
	t.Cleanup(func() { gitStatus = status })

	w.RefreshGit(func(apply func()) { apply() })

	if got := most.Load(); got > branchLookups {
		t.Errorf("%d git processes ran at once, want at most %d", got, branchLookups)
	}
	for _, p := range w.panes {
		if p.Branch != "trunk" {
			t.Errorf("pane in %s was not refreshed", p.Cwd)
		}
	}
}

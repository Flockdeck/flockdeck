package workspace

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
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

// TestRefreshGitAsksOncePerCheckoutHoweverItIsSpelt covers panes in one
// checkout whose directories are written differently — a trailing separator,
// and on Windows a drive letter in another case. Each spelling cost a
// whole-tree git status of its own, every refresh.
func TestRefreshGitAsksOncePerCheckoutHoweverItIsSpelt(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed, so nothing is refreshed")
	}
	dir := filepath.Join(t.TempDir(), "repo")
	other := dir + string(filepath.Separator)
	if runtime.GOOS == "windows" {
		other = strings.ToUpper(dir[:1]) + dir[1:] + `\`
		dir = strings.ToLower(dir[:1]) + dir[1:]
	}
	w := &Workspace{panes: map[string]*Pane{
		"p": {ID: "p", Cwd: dir},
		"q": {ID: "q", Cwd: other},
	}, BroadcastSet: map[string]bool{}}

	var calls atomic.Int32
	status := gitStatus
	gitStatus = func(string) gitx.Status {
		calls.Add(1)
		return gitx.Status{Branch: "trunk"}
	}
	t.Cleanup(func() { gitStatus = status })

	w.RefreshGit(func(apply func()) { apply() })

	if n := calls.Load(); n != 1 {
		t.Errorf("git was asked %d times about one checkout, want once", n)
	}
	for _, p := range w.panes {
		if p.Branch != "trunk" {
			t.Errorf("pane in %q was not given the checkout's branch", p.Cwd)
		}
	}
}

// TestRefreshGitNamesADetachedCheckout covers an agent that checks out a bare
// commit — a bisect, a look at an old revision. git reports no branch then,
// and the pane header went on naming the branch the checkout had left.
func TestRefreshGitNamesADetachedCheckout(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed, so nothing is refreshed")
	}
	w := &Workspace{panes: map[string]*Pane{
		"p": {ID: "p", Cwd: "/checkout", Branch: "trunk"},
		"q": {ID: "q", Cwd: "/elsewhere", Branch: "trunk"},
	}, BroadcastSet: map[string]bool{}}

	status := gitStatus
	gitStatus = func(dir string) gitx.Status {
		if dir == "/checkout" {
			return gitx.Status{Detached: true, Head: "abc1234"}
		}
		// git failing tells nothing about which branch is checked out.
		return gitx.Status{}
	}
	t.Cleanup(func() { gitStatus = status })

	w.RefreshGit(func(apply func()) { apply() })

	if got := w.panes["p"].Branch; got != "detached@abc1234" {
		t.Errorf("detached pane is labelled %q, want detached@abc1234", got)
	}
	if got := w.panes["q"].Branch; got != "trunk" {
		t.Errorf("a pane git said nothing about is labelled %q, want it left as trunk", got)
	}
}

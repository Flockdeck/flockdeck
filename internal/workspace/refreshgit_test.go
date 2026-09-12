package workspace

import (
	"context"
	"errors"
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
	gitStatus = func(string, time.Duration) (gitx.Status, error) {
		n := inFlight.Add(1)
		for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
		}
		time.Sleep(5 * time.Millisecond)
		inFlight.Add(-1)
		return gitx.Status{Branch: "trunk"}, nil
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
	gitStatus = func(string, time.Duration) (gitx.Status, error) {
		calls.Add(1)
		return gitx.Status{Branch: "trunk"}, nil
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
	gitStatus = func(dir string, _ time.Duration) (gitx.Status, error) {
		if dir == "/checkout" {
			return gitx.Status{Detached: true, Head: "abc1234"}, nil
		}
		// git failing tells nothing about which branch is checked out.
		return gitx.Status{}, errors.New("not a git repository")
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

// TestRefreshGitShowsEachCheckoutAsItAnswers covers one checkout that hangs
// beside one that answers straight away. Nothing was applied until every
// checkout had answered, so the one that hung held every pane's header at its
// old numbers for as long as git was given, twenty seconds.
func TestRefreshGitShowsEachCheckoutAsItAnswers(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed, so nothing is refreshed")
	}
	w := &Workspace{panes: map[string]*Pane{
		"slow": {ID: "slow", Cwd: "/stuck"},
		"fast": {ID: "fast", Cwd: "/fine"},
	}, BroadcastSet: map[string]bool{}}

	release := make(chan struct{})
	status := gitStatus
	gitStatus = func(dir string, _ time.Duration) (gitx.Status, error) {
		if dir == "/stuck" {
			<-release
		}
		return gitx.Status{Branch: "trunk", Dirty: 1}, nil
	}
	t.Cleanup(func() { gitStatus = status })

	applied := make(chan func(), 2)
	done := make(chan struct{})
	go func() {
		w.RefreshGit(func(apply func()) { applied <- apply })
		close(done)
	}()
	select {
	case apply := <-applied:
		apply()
	case <-time.After(5 * time.Second):
		close(release)
		<-done
		t.Fatal("nothing was shown while one checkout was still being read")
	}
	if got := w.panes["fast"].Git.Dirty; got != 1 {
		t.Errorf("the checkout that answered shows %d changed, want 1", got)
	}
	if got := w.panes["slow"].Git.Dirty; got != 0 {
		t.Errorf("the checkout still being read shows %d changed before it answered", got)
	}

	close(release)
	(<-applied)()
	<-done
	if got := w.panes["slow"].Git.Dirty; got != 1 {
		t.Errorf("the slow checkout shows %d changed once it answered, want 1", got)
	}
}

// TestRefreshGitMarksACheckoutThatTimedOut checks a checkout git did not
// answer for in time is marked as such, rather than going on showing what was
// read before it hung as though it were current, and that the mark goes once
// git answers again. A checkout git answered with an error is not marked: it
// was heard from, and said it was no repository.
func TestRefreshGitMarksACheckoutThatTimedOut(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed, so nothing is refreshed")
	}
	w := &Workspace{panes: map[string]*Pane{
		"p": {ID: "p", Cwd: "/stuck", Branch: "trunk", Git: gitx.Status{Branch: "trunk", Dirty: 3}},
		"q": {ID: "q", Cwd: "/gone", Branch: "trunk"},
	}, BroadcastSet: map[string]bool{}}

	var given atomic.Int64
	answer := false
	status := gitStatus
	gitStatus = func(dir string, deadline time.Duration) (gitx.Status, error) {
		if dir == "/gone" {
			return gitx.Status{}, errors.New("not a git repository")
		}
		given.Store(int64(deadline))
		if answer {
			return gitx.Status{Branch: "trunk", Dirty: 5}, nil
		}
		return gitx.Status{}, fmt.Errorf("git status: gave up: %w", context.DeadlineExceeded)
	}
	t.Cleanup(func() { gitStatus = status })

	w.RefreshGit(func(apply func()) { apply() })

	if got := time.Duration(given.Load()); got != gitDeadline {
		t.Errorf("git was given %v, want the refresh deadline of %v", got, gitDeadline)
	}
	p := w.panes["p"]
	if !p.GitTimedOut {
		t.Error("a checkout that did not answer in time is not marked")
	}
	if p.Git.Dirty != 3 || p.Branch != "trunk" {
		t.Errorf("what was read before the checkout hung was thrown away: %+v on %q", p.Git, p.Branch)
	}
	if w.panes["q"].GitTimedOut {
		t.Error("a checkout git answered with an error is marked as not having answered")
	}

	answer = true
	w.RefreshGit(func(apply func()) { apply() })
	if p.GitTimedOut {
		t.Error("the mark stayed after git answered again")
	}
	if p.Git.Dirty != 5 {
		t.Errorf("the checkout shows %d changed after it answered, want 5", p.Git.Dirty)
	}
}

// TestRefreshGitLeavesACheckoutBeingReadToTheRefreshReadingIt covers
// refreshes that overlap, which they do now that one does not wait for the
// last. A second git process in a checkout already slow would only queue
// behind the first; the checkouts that answer are still asked about.
func TestRefreshGitLeavesACheckoutBeingReadToTheRefreshReadingIt(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed, so nothing is refreshed")
	}
	w := &Workspace{panes: map[string]*Pane{
		"slow": {ID: "slow", Cwd: "/stuck"},
		"fast": {ID: "fast", Cwd: "/fine"},
	}, BroadcastSet: map[string]bool{}}

	release := make(chan struct{})
	var stuck, fine atomic.Int32
	status := gitStatus
	gitStatus = func(dir string, _ time.Duration) (gitx.Status, error) {
		if dir == "/stuck" {
			stuck.Add(1)
			<-release
		} else {
			fine.Add(1)
		}
		return gitx.Status{Branch: "trunk"}, nil
	}
	t.Cleanup(func() { gitStatus = status })

	ignore := func(func()) {}
	first := make(chan struct{})
	go func() { w.RefreshGit(ignore); close(first) }()
	for deadline := time.Now().Add(5 * time.Second); stuck.Load() == 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the slow checkout was never asked about")
		}
	}

	second := make(chan struct{})
	go func() { w.RefreshGit(ignore); close(second) }()
	select {
	case <-second:
	case <-time.After(5 * time.Second):
		close(release)
		<-first
		<-second
		t.Fatal("a second refresh waited on the checkout the first was still reading")
	}
	if n := stuck.Load(); n != 1 {
		t.Errorf("the slow checkout was asked about %d times at once, want once", n)
	}
	if n := fine.Load(); n != 2 {
		t.Errorf("the checkout that answers was asked about %d times in two refreshes, want 2", n)
	}

	close(release)
	<-first
	w.RefreshGit(ignore)
	if n := stuck.Load(); n != 2 {
		t.Errorf("the slow checkout was asked about %d times after it answered, want it asked again", n)
	}
}

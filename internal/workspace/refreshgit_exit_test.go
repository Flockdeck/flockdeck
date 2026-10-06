package workspace

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
)

// TestRefreshGitDoesNotAskAgainUntilTheGitGivenUpOnHasGone covers a checkout
// whose git was given up on at the deadline but has not finished ending, as
// one stuck on a network drive that has gone away does not. The checkout was
// free to be asked again as soon as the deadline passed, so every refresh
// started another git beside the ones still stuck there.
//
// Its panes are still marked at once, and the refresh does not wait for the
// git to go.
func TestRefreshGitDoesNotAskAgainUntilTheGitGivenUpOnHasGone(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed, so nothing is refreshed")
	}
	w := &Workspace{panes: map[string]*Pane{
		"p": {ID: "p", Cwd: "/stuck", Branch: "trunk"},
	}, BroadcastSet: map[string]bool{}}

	gone := make(chan struct{})
	var goneOnce sync.Once
	letGo := func() { goneOnce.Do(func() { close(gone) }) }
	var asked atomic.Int32
	status, exited := gitStatus, gitExited
	gitStatus = func(string, time.Duration) (gitx.Status, error) {
		asked.Add(1)
		return gitx.Status{}, fmt.Errorf("git status: gave up: %w", context.DeadlineExceeded)
	}
	gitExited = func(error) <-chan struct{} { return gone }
	t.Cleanup(func() { gitStatus, gitExited = status, exited })

	// Registered last, so it runs first: the git given up on is let go even when the
	// test ends early, and waitGitIdle, which runs next, has nothing to wait for.
	t.Cleanup(w.waitGitIdle)
	t.Cleanup(letGo)

	refresh := func() {
		t.Helper()
		done := make(chan struct{})
		go func() {
			w.RefreshGit(func(apply func()) { apply() })
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			letGo()
			t.Fatal("a refresh waited for a git it had given up on to finish ending")
		}
	}

	refresh()
	if !w.panes["p"].GitTimedOut {
		t.Error("the checkout was not marked as soon as git was given up on")
	}
	refresh()
	if n := asked.Load(); n != 1 {
		t.Errorf("the checkout was asked about %d times while the git given up on was still ending, want once", n)
	}

	letGo()
	for deadline := time.Now().Add(5 * time.Second); asked.Load() < 2; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the checkout was never asked about again once its git had gone")
		}
		refresh()
	}
	awaitNoGitRead(t, w)
}

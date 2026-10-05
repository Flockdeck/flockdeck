package workspace

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/record"
)

// TestRefreshGitRefreshesTheIndexOfASlowCheckoutOnce covers a checkout whose
// files were all touched without changing. The headers' status takes no lock,
// so it never writes down what it found, and every status after it looked at
// every one of those files again: five and a half seconds a time on a large
// checkout, for as long as nothing else refreshed the index. A slow status now
// has the index refreshed once; a fast one, and the same checkout again soon
// after, do not.
func TestRefreshGitRefreshesTheIndexOfASlowCheckoutOnce(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed, so nothing is refreshed")
	}
	w := &Workspace{panes: map[string]*Pane{
		"slow": {ID: "slow", Cwd: "/slow"},
		"fast": {ID: "fast", Cwd: "/fast"},
	}, BroadcastSet: map[string]bool{}}

	status, refresh, threshold := gitStatus, indexRefresh, slowStatus
	slowStatus = 50 * time.Millisecond
	gitStatus = func(dir string, _ time.Duration) (gitx.Status, error) {
		if dir == "/slow" {
			time.Sleep(100 * time.Millisecond)
		}
		return gitx.Status{Branch: "trunk"}, nil
	}
	var mu sync.Mutex
	refreshed := map[string]int{}
	indexRefresh = func(_ context.Context, dir string) error {
		mu.Lock()
		refreshed[dir]++
		mu.Unlock()
		return nil
	}
	t.Cleanup(func() { gitStatus, indexRefresh, slowStatus = status, refresh, threshold })

	w.RefreshGit(func(apply func()) { apply() })
	w.RefreshGit(func(apply func()) { apply() })

	mu.Lock()
	defer mu.Unlock()
	if n := refreshed["/slow"]; n != 1 {
		t.Errorf("the slow checkout's index was refreshed %d times over two refreshes, want once", n)
	}
	if n := refreshed["/fast"]; n != 0 {
		t.Errorf("the fast checkout's index was refreshed %d times, want never", n)
	}
}

// A checkout whose index refresh takes long does not hold up the others: the refresh
// is waited for a little, and RefreshGit returns, with the checkout still marked
// busy until the refresh ends. One that found the index in use is tried again.
func TestAnIndexRefreshThatTakesLongDoesNotHoldUpRefreshGit(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed, so nothing is refreshed")
	}
	w := &Workspace{panes: map[string]*Pane{
		"slow": {ID: "slow", Cwd: "/slow"},
		"fast": {ID: "fast", Cwd: "/fast"},
	}, BroadcastSet: map[string]bool{}}
	status, refresh, threshold, hold := gitStatus, indexRefresh, slowStatus, indexRefreshHold
	slowStatus, indexRefreshHold = 10*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { gitStatus, indexRefresh, slowStatus, indexRefreshHold = status, refresh, threshold, hold })
	gitStatus = func(dir string, _ time.Duration) (gitx.Status, error) {
		if dir == "/slow" {
			time.Sleep(30 * time.Millisecond)
		}
		return gitx.Status{Branch: "trunk"}, nil
	}
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	var calls atomic.Int32
	indexRefresh = func(_ context.Context, dir string) error {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return nil
	}

	began := time.Now()
	w.RefreshGit(func(apply func()) { apply() })
	if took := time.Since(began); took > 3*time.Second {
		t.Fatalf("RefreshGit took %v behind one checkout's index refresh, want about the hold of 100ms", took)
	}
	<-started
	// Still refreshing: the checkout is busy, and is not asked about again.
	w.gitMu.Lock()
	busy := w.gitBusy[pathKey("/slow")]
	w.gitMu.Unlock()
	if !busy {
		t.Error("the checkout was released while its index was still being refreshed")
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		w.gitMu.Lock()
		busy = w.gitBusy[pathKey("/slow")]
		w.gitMu.Unlock()
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the checkout stayed busy after its refresh ended")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A refresh that did nothing because the index was in use is asked for again at the
// next slow answer, where one that ran is not asked again for a while.
func TestAnIndexRefreshThatFoundTheIndexBusyIsTriedAgain(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed, so nothing is refreshed")
	}
	w := &Workspace{panes: map[string]*Pane{"slow": {ID: "slow", Cwd: "/slow"}}, BroadcastSet: map[string]bool{}}
	status, refresh, threshold := gitStatus, indexRefresh, slowStatus
	slowStatus = 10 * time.Millisecond
	t.Cleanup(func() { gitStatus, indexRefresh, slowStatus = status, refresh, threshold })
	gitStatus = func(dir string, _ time.Duration) (gitx.Status, error) {
		time.Sleep(30 * time.Millisecond)
		return gitx.Status{Branch: "trunk"}, nil
	}
	var calls atomic.Int32
	indexRefresh = func(_ context.Context, dir string) error {
		if calls.Add(1) == 1 {
			return gitx.ErrIndexBusy
		}
		return nil
	}
	for i := 0; i < 4; i++ {
		w.RefreshGit(func(apply func()) { apply() })
		time.Sleep(20 * time.Millisecond) // for the checkout to be released
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("the index was refreshed %d times over four refreshes, want two: the busy one, tried again, and then not again", n)
	}
}

// Closing a workspace stops its index refreshes being waited for, and only that
// workspace's: another workspace in the process (a second window, another test) keeps
// its own. Nothing is killed and no lock is touched: the process is left to finish by
// itself (gitx tests that with a real child process, which this package cannot start).
// What is stubbed here is the call into gitx, to see that Close hands it a context that
// ends, and ends only this workspace's.
func TestClosingAWorkspaceEndsItsIndexRefreshAndNotAnothers(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed, so nothing is refreshed")
	}
	status, refresh, threshold, hold := gitStatus, indexRefresh, slowStatus, indexRefreshHold
	slowStatus, indexRefreshHold = 10*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { gitStatus, indexRefresh, slowStatus, indexRefreshHold = status, refresh, threshold, hold })
	gitStatus = func(dir string, _ time.Duration) (gitx.Status, error) {
		time.Sleep(30 * time.Millisecond)
		return gitx.Status{Branch: "trunk"}, nil
	}
	ended := map[string]chan struct{}{"/one": make(chan struct{}), "/two": make(chan struct{})}
	started := make(chan string, 2)
	indexRefresh = func(ctx context.Context, dir string) error {
		started <- dir
		<-ctx.Done() // a refresh that is waited for until Close says to stop
		close(ended[dir])
		return gitx.ErrRefreshRunning
	}
	mk := func(cwd string) *Workspace {
		return &Workspace{panes: map[string]*Pane{"p": {ID: "p", Cwd: cwd}}, BroadcastSet: map[string]bool{}, rec: record.NewManager(func() (string, error) { return t.TempDir(), nil })}
	}
	one, two := mk("/one"), mk("/two")
	one.RefreshGit(func(apply func()) { apply() })
	two.RefreshGit(func(apply func()) { apply() })
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("a refresh did not start")
		}
	}

	began := time.Now()
	one.Close()
	if took := time.Since(began); took > 2*time.Second {
		t.Errorf("Close took %v with a refresh running", took)
	}
	select {
	case <-ended["/one"]:
	case <-time.After(2 * time.Second):
		t.Fatal("closing the workspace did not end its index refresh")
	}
	select {
	case <-ended["/two"]:
		t.Fatal("closing one workspace ended another's index refresh")
	case <-time.After(200 * time.Millisecond):
	}
	two.Close()
	select {
	case <-ended["/two"]:
	case <-time.After(2 * time.Second):
		t.Fatal("closing the second workspace did not end its refresh")
	}
}

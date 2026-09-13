package workspace

import (
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
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
	indexRefresh = func(dir string) error {
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

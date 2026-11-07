package gitx

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A commit that meets the index lock of Flockdeck's own refresh waits for the
// refresh to end, though it takes longer than lockWait, and is not failed with
// "another git command is using this repository". The stand-in for update-index
// makes the lock as git does and removes it when told to end.
func TestACommitWaitsForARefreshHoldingTheIndexLock(t *testing.T) {
	restoreWait := lockWait
	t.Cleanup(func() { lockWait = restoreWait })
	lockWait = 200 * time.Millisecond

	repo, _ := staleRepo(t, 3)
	write(t, repo, "README.md", "changed for the commit\n")

	var started atomic.Bool
	release := make(chan struct{})
	withHook(t, func(ctx context.Context, args []string) error {
		if len(args) == 0 || args[0] != "update-index" {
			return nil
		}
		if err := os.WriteFile(lockOf(repo), nil, 0o600); err != nil {
			return err
		}
		started.Store(true)
		<-release
		_ = os.Remove(lockOf(repo))
		return errors.New("the stand-in ended") // so no real git follows it
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	refreshed := make(chan error, 1)
	go func() { refreshed <- RefreshIndexCtx(ctx, repo) }()
	eventually(t, "the refresh holds the lock", started.Load)
	cancel() // nobody waits for it any more; it goes on
	<-refreshed

	committed := make(chan error, 1)
	go func() { committed <- CommitAll(repo, "waited for the refresh") }()

	// Well past lockWait: the commit is still waiting, not failed.
	select {
	case err := <-committed:
		t.Fatalf("the commit ended while the refresh held the lock: %v", err)
	case <-time.After(3 * lockWait):
	}
	// A refresh does not start under a commit that is waiting.
	if err := RefreshIndexCtx(context.Background(), repo); !errors.Is(err, ErrIndexBusy) {
		t.Errorf("a refresh beside a waiting commit = %v, want ErrIndexBusy", err)
	}
	close(release)
	select {
	case err := <-committed:
		if err != nil {
			t.Fatalf("the commit failed after the refresh ended: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the commit never finished")
	}
	if got := strings.TrimSpace(gitRun(t, repo, "log", "-1", "--format=%s")); got != "waited for the refresh" {
		t.Errorf("the commit is not there: %q", got)
	}
}

// The wait for a refresh has a bound: a refresh that never ends does not hold a
// commit for ever, and the commit then says what a held lock says.
func TestACommitDoesNotWaitForARefreshWithoutEnd(t *testing.T) {
	restoreWait, restoreCap := lockWait, commitRefreshWait
	t.Cleanup(func() { lockWait, commitRefreshWait = restoreWait, restoreCap })
	lockWait, commitRefreshWait = 100*time.Millisecond, 400*time.Millisecond

	repo, _ := staleRepo(t, 3)
	write(t, repo, "README.md", "changed for the commit\n")
	release := make(chan struct{})
	var started atomic.Bool
	withHook(t, func(ctx context.Context, args []string) error {
		if len(args) == 0 || args[0] != "update-index" {
			return nil
		}
		_ = os.WriteFile(lockOf(repo), nil, 0o600)
		started.Store(true)
		<-release
		_ = os.Remove(lockOf(repo))
		return errors.New("the stand-in ended")
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	refreshed := make(chan error, 1)
	go func() { refreshed <- RefreshIndexCtx(ctx, repo) }()
	eventually(t, "the refresh holds the lock", started.Load)
	cancel()
	<-refreshed

	err := CommitAll(repo, "never")
	if err == nil || !strings.Contains(err.Error(), "another git command is using this repository") {
		t.Fatalf("got %v, want the held-lock message", err)
	}
}

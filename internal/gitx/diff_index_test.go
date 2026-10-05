package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// staleRepo is a checkout of n files, one of them (README.md) changed, whose files
// can be given stale stat data again with the returned function: what git diff and
// update-index rewrite the index for.
func staleRepo(t *testing.T, n int) (repo string, makeStale func(round int)) {
	t.Helper()
	repo = newRepo(t)
	for i := 0; i < n; i++ {
		write(t, repo, fmt.Sprintf("f%03d.txt", i), fmt.Sprintf("file %d\nbody\n", i))
	}
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-q", "-m", "many")
	write(t, repo, "README.md", "changed\n")
	return repo, func(round int) {
		when := time.Now().Add(time.Duration(round+1) * time.Hour)
		for i := 0; i < n; i += 3 {
			_ = os.Chtimes(filepath.Join(repo, fmt.Sprintf("f%03d.txt", i)), when, when)
		}
	}
}

// Changes, Diff and RefreshIndex run side by side on a checkout whose files have
// stale stat data, which is when git diff and update-index rewrite the index. None
// of them may be refused it, and what Changes lists is what it lists with nothing
// else going on.
func TestChangesDiffAndRefreshIndexTogetherAreNeverRefusedTheIndex(t *testing.T) {
	t.Parallel()
	repo, stale := staleRepo(t, 90)
	var wg sync.WaitGroup
	errs := make(chan string, 1000)
	report := func(what string, err error) {
		if err != nil && !errors.Is(err, ErrIndexBusy) {
			errs <- what + ": " + err.Error()
		}
	}
	for round := 0; round < 200; round++ {
		stale(round)
		wg.Add(3)
		go func() {
			defer wg.Done()
			files, err := Changes(repo)
			report("Changes", err)
			if err == nil && (len(files) != 1 || files[0].Path != "README.md") {
				errs <- fmt.Sprintf("Changes listed %+v, want README.md alone", files)
			}
		}()
		go func() { defer wg.Done(); _, err := Diff(repo, "README.md"); report("Diff", err) }()
		go func() { defer wg.Done(); report("RefreshIndex", RefreshIndex(repo)) }()
		wg.Wait()
	}
	close(errs)
	var all []string
	for e := range errs {
		all = append(all, e)
	}
	if len(all) > 0 {
		t.Errorf("%d problems, the first: %s", len(all), all[0])
	}
}

// A command refused the index because something was replacing it asks again; any
// other failure is not asked about again, and the number of asks is bounded.
func TestRetryIndexAsksAgainOnlyForARefusedIndex(t *testing.T) {
	oldWait, oldN := indexRetryWait, indexRetries
	indexRetryWait, indexRetries = time.Millisecond, 3
	t.Cleanup(func() { indexRetryWait, indexRetries = oldWait, oldN })

	calls := 0
	err := retryIndex(func() error {
		calls++
		if calls < 3 {
			return errors.New("git status: .git/index: index file open failed: Permission denied")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Errorf("a refused index: %d calls, %v, want 3 calls and success", calls, err)
	}
	calls = 0
	err = retryIndex(func() error { calls++; return errors.New("git status: not a git repository") })
	if err == nil || calls != 1 {
		t.Errorf("another failure: %d calls, %v, want one call and the error", calls, err)
	}
	calls = 0
	err = retryIndex(func() error {
		calls++
		return errors.New("index file open failed: Permission denied")
	})
	if err == nil || calls != 4 {
		t.Errorf("an index that stays refused: %d calls, %v, want the first and three more", calls, err)
	}
	for msg, want := range map[string]bool{
		"fatal: .git/index: index file open failed: Permission denied": true,
		"error: C:/repo/.git/index: Permission denied":                 true,
		"index file open failed: Permission denied":                    true,
		// A path with "index" in it is not the index.
		"error: open(\"src/index.js\"): Permission denied":                   false,
		"fatal: unable to read C:/work/index-notes/a.txt: Permission denied": false,
		"fatal: Unable to create '.git/index.lock': Permission denied":       false,
		"fatal: cannot open C:/Users/me/index/pack: Permission denied":       false,
		"warning: could not index the repository: Operation not permitted":   false,
		"fatal: unable to access 'index.html': Permission denied":            false,
		"fatal: bad object index: No such file":                              false,
	} {
		if got := transientIndexError(errors.New(msg)); got != want {
			t.Errorf("transientIndexError(%q) = %v, want %v", msg, got, want)
		}
	}
}

// withHook sets gitHook for the test, and puts it back.
func withHook(t *testing.T, hook func(ctx context.Context, args []string) error) {
	t.Helper()
	old := gitHook
	gitHook = hook
	restoreAfterRefreshes(t, func() { gitHook = old })
}

// The retry is wired into every command that reads the index: Changes' status and
// numstat, the header's status read, and the panel's per-file diff. Each is made
// to be refused the index once and must still succeed. Bypassing retryIndex in any
// of them fails here. Not parallel: it owns gitHook.
func TestEveryIndexReaderAsksAgainWhenRefusedTheIndex(t *testing.T) {
	oldWait := indexRetryWait
	indexRetryWait = time.Millisecond
	t.Cleanup(func() { indexRetryWait = oldWait })
	repo, _ := staleRepo(t, 5)

	for _, sub := range []string{"status", "diff"} {
		var refused atomic.Int32
		withHook(t, func(ctx context.Context, args []string) error {
			if len(args) > 0 && args[0] == sub && refused.Add(1) == 1 {
				return errors.New(".git/index: index file open failed: Permission denied")
			}
			return nil
		})
		switch sub {
		case "status":
			if files, err := Changes(repo); err != nil || len(files) != 1 {
				t.Errorf("Changes with status refused once: %+v, %v", files, err)
			}
			refused.Store(0)
			if _, err := StatusWithin(repo, 20*time.Second); err != nil {
				t.Errorf("the header's status read refused once: %v", err)
			}
		case "diff":
			// Changes' numstat, then the panel's diff of one file.
			counts := lineCounts(context.Background(), repo)
			if _, ok := counts["README.md"]; !ok {
				t.Errorf("the line counts after a refused numstat = %v, want README.md counted", counts)
			}
			refused.Store(0)
			if out, err := Diff(repo, "README.md"); err != nil || !strings.Contains(out, "changed") {
				t.Errorf("Diff refused once: %q, %v", out, err)
			}
		}
		if refused.Load() == 0 {
			t.Errorf("%s was never asked, so the case was not made", sub)
		}
	}
}

// A refresh that cannot get the index to itself because a reader holds it does not
// wait for ever, and does not hold anybody else up while it waits.
func TestRefreshIndexSkipsWhileAReaderHoldsTheIndexAndHoldsNobodyUp(t *testing.T) {
	oldWait := indexWriterWait
	indexWriterWait = 400 * time.Millisecond
	t.Cleanup(func() { indexWriterWait = oldWait })
	repo, _ := staleRepo(t, 5)

	held, err := readIndex(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	refreshed := make(chan error, 1)
	go func() { refreshed <- RefreshIndex(repo) }()
	time.Sleep(100 * time.Millisecond) // the refresh is waiting its turn

	started := time.Now()
	if files, err := Changes(repo); err != nil || len(files) != 1 {
		t.Fatalf("Changes while a refresh waited: %+v, %v", files, err)
	}
	if took := time.Since(started); took > 5*time.Second {
		t.Errorf("Changes took %v behind a refresh that was only waiting", took)
	}
	select {
	case err := <-refreshed:
		if !errors.Is(err, ErrIndexBusy) {
			t.Errorf("RefreshIndex = %v, want ErrIndexBusy", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RefreshIndex waited for a reader that never let go")
	}
	held()
	if err := RefreshIndex(repo); err != nil {
		t.Errorf("RefreshIndex once the reader had gone: %v", err)
	}
}

// A reader that never lets go -- a git that hung -- does not freeze the Changes
// calls that come after it, or stop the checkout being refreshed later.
func TestAHungReaderDoesNotFreezeLaterCalls(t *testing.T) {
	oldWait := indexWriterWait
	indexWriterWait = 100 * time.Millisecond
	t.Cleanup(func() { indexWriterWait = oldWait })
	repo, _ := staleRepo(t, 5)
	if _, err := readIndex(context.Background(), repo); err != nil { // never released
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		done := make(chan error, 1)
		go func() { _, err := Changes(repo); done <- err }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Changes %d: %v", i, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("Changes %d froze behind a hung reader", i)
		}
	}
	if err := RefreshIndex(repo); !errors.Is(err, ErrIndexBusy) {
		t.Errorf("RefreshIndex with a hung reader = %v, want ErrIndexBusy and no wait for ever", err)
	}
}

// A refresh that hangs gives the gate back when waiting for it stops, and is not
// killed: the readers that arrived meanwhile were not held for more than their bound.
func TestAStuckRefreshReleasesTheIndexWhenWaitingStops(t *testing.T) {
	oldStop, oldWait := indexStopWaiting, indexReaderWait
	indexStopWaiting, indexReaderWait = 300*time.Millisecond, 150*time.Millisecond
	t.Cleanup(func() { indexStopWaiting, indexReaderWait = oldStop, oldWait })
	repo, _ := staleRepo(t, 5)
	b := hangRefreshes(t)
	refreshed := make(chan error, 1)
	go func() { refreshed <- RefreshIndex(repo) }()
	time.Sleep(50 * time.Millisecond)

	started := time.Now()
	release, err := readIndex(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if took := time.Since(started); took > time.Second {
		t.Errorf("a reader waited %v for a stuck refresh, want about its bound", took)
	}
	select {
	case err := <-refreshed:
		if !errors.Is(err, ErrRefreshRunning) {
			t.Errorf("a refresh that hung = %v, want ErrRefreshRunning", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the wait did not end")
	}
	// The index is free: a writer takes it at once, while the process is still there.
	release2, err := tryWriteIndex(context.Background(), repo, time.Millisecond)
	if err != nil {
		t.Fatalf("the index was not given back when waiting stopped: %v", err)
	}
	release2()
	if b.live.Load() != 1 {
		t.Errorf("%d refresh processes live, want the one that was not killed", b.live.Load())
	}
}

// A reader waiting for a running refresh is let go by its context.
func TestAWaitingReaderIsCancelledByItsContext(t *testing.T) {
	oldWait := indexReaderWait
	indexReaderWait = time.Minute
	t.Cleanup(func() { indexReaderWait = oldWait })
	repo, _ := staleRepo(t, 2)
	release, err := tryWriteIndex(context.Background(), repo, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := readIndex(ctx, repo); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("readIndex = %v, want the context's error", err)
	}
	if took := time.Since(started); took > 2*time.Second {
		t.Errorf("the wait ended after %v, want about the context's 100ms", took)
	}
}

// A reader goes ahead after its bound, even while the refresh still runs, and one
// that arrives while a writer only waits is not held at all.
func TestReadersAreBoundedAndNeverWaitBehindAPendingWriter(t *testing.T) {
	oldWait := indexReaderWait
	indexReaderWait = 150 * time.Millisecond
	t.Cleanup(func() { indexReaderWait = oldWait })
	repo, _ := staleRepo(t, 2)

	release, err := tryWriteIndex(context.Background(), repo, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	r, err := readIndex(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	r()
	if took := time.Since(started); took < 100*time.Millisecond || took > 2*time.Second {
		t.Errorf("a reader behind a running writer waited %v, want about 150ms", took)
	}
	release()

	held, _ := readIndex(context.Background(), repo)
	pending := make(chan error, 1)
	go func() {
		// It gets the index once held lets go, and gives it back: left holding it, the
		// checkout's gate stays busy for ever.
		rel, err := tryWriteIndex(context.Background(), repo, 500*time.Millisecond)
		if err == nil {
			rel()
		}
		pending <- err
	}()
	time.Sleep(100 * time.Millisecond)
	started = time.Now()
	r2, err := readIndex(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	r2()
	if took := time.Since(started); took > 80*time.Millisecond {
		t.Errorf("a reader waited %v behind a writer that was only waiting, want none", took)
	}
	held()
	<-pending
}

// With Changes, Diff, a header status and RefreshIndex all running at once on one
// checkout, for a while, nothing deadlocks and nothing is refused the index. Run
// it under -race.
func TestMixedIndexCommandsDoNotDeadlock(t *testing.T) {
	t.Parallel()
	repo, stale := staleRepo(t, 30)
	deadline := time.Now().Add(5 * time.Second)
	var wg sync.WaitGroup
	var bad atomic.Int32
	var first atomic.Value
	note := func(what string, err error) {
		if err != nil && !errors.Is(err, ErrIndexBusy) && bad.Add(1) == 1 {
			first.Store(what + ": " + err.Error())
		}
	}
	ops := []func(){
		func() { _, err := Changes(repo); note("Changes", err) },
		func() { _, err := Diff(repo, "README.md"); note("Diff", err) },
		func() { _, err := StatusWithin(repo, 20*time.Second); note("StatusWithin", err) },
		func() { note("RefreshIndex", RefreshIndex(repo)) },
	}
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; time.Now().Before(deadline); i++ {
				if g%4 == 0 {
					stale(i)
				}
				ops[(g+i)%len(ops)]()
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the mixed commands did not finish: a deadlock")
	}
	if bad.Load() > 0 {
		t.Errorf("%d failures, the first: %v", bad.Load(), first.Load())
	}
}

// When Changes has more files than it will count, it cancels the line counts that
// are still running; a refresh waiting for the index must not stand in the way.
func TestChangesStopsCountingWhileARefreshIsPending(t *testing.T) {
	oldWait := indexWriterWait
	indexWriterWait = 2 * time.Second
	t.Cleanup(func() { indexWriterWait = oldWait })
	repo, _ := staleRepo(t, 5)
	write(t, repo, "second.txt", "another change\n")
	held, _ := readIndex(context.Background(), repo)
	refreshDone := make(chan struct{})
	go func() { defer close(refreshDone); _ = RefreshIndex(repo) }()
	// The refresh goes on to run git once the held index is let go, below, after this
	// test's own work: the test waits for it, so that git has stopped writing into
	// the checkout before its folder is removed.
	t.Cleanup(func() { <-refreshDone })
	time.Sleep(100 * time.Millisecond)

	started := time.Now()
	files, err := changes(repo, 1) // two files, one is the most that is counted
	held()
	if err != nil || len(files) != 2 {
		t.Fatalf("changes = %+v, %v", files, err)
	}
	if took := time.Since(started); took > 5*time.Second {
		t.Errorf("changes took %v with a refresh pending", took)
	}
}

// The key is the checkout's top level, found without git, so two folders of one
// checkout share a gate and two worktrees do not.
func TestIndexKeyIsTheCheckoutsTopLevel(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa")
	sub := filepath.Join(repo, "deep", "er")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if indexKey(sub) != indexKey(repo) {
		t.Error("a folder of a checkout has another key than its top level")
	}
	if indexKey(wt["wa"]) == indexKey(repo) {
		t.Error("a linked worktree shares the main checkout's key")
	}
}

// A key is worked out once for a while, and a folder whose file system does not
// answer is its own key rather than a command held up before git starts.
func TestIndexKeyIsCachedAndBounded(t *testing.T) {
	oldFn, oldTTL, oldWait := findTopFn, indexKeyTTL, indexKeyWait
	t.Cleanup(func() { findTopFn, indexKeyTTL, indexKeyWait = oldFn, oldTTL, oldWait })
	var looked atomic.Int32
	findTopFn = func(abs string) string { looked.Add(1); return "top:" + abs }
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		if k := indexKey(dir); !strings.HasPrefix(k, "top:") {
			t.Fatalf("key = %q", k)
		}
	}
	if looked.Load() != 1 {
		t.Errorf("the file system was asked %d times for five calls, want once", looked.Load())
	}
	indexKeyTTL = 0
	indexKey(dir)
	if looked.Load() != 2 {
		t.Errorf("the key was not worked out again once its time was up (%d)", looked.Load())
	}

	// A file system that does not answer.
	indexKeyTTL, indexKeyWait = time.Minute, 50*time.Millisecond
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	findTopFn = func(abs string) string { <-block; return "never" }
	other := t.TempDir()
	started := time.Now()
	k := indexKey(other)
	if took := time.Since(started); took > time.Second {
		t.Errorf("indexKey waited %v for a file system that does not answer", took)
	}
	if k == "never" || k == "" {
		t.Errorf("key = %q, want the folder itself", k)
	}
}

// Gates of checkouts nobody has touched for an hour are let go of, and ones in use
// are kept.
func TestUnusedIndexGatesArePruned(t *testing.T) {
	oldTTL, oldEvery := gateTTL, gateSweepEvery
	t.Cleanup(func() { gateTTL, gateSweepEvery = oldTTL, oldEvery })
	a, b := t.TempDir(), t.TempDir()
	ga, gb := gateFor(a), gateFor(b)
	_ = ga
	held, _ := readIndex(context.Background(), b) // b is in use
	defer held()
	gateTTL, gateSweepEvery = time.Nanosecond, time.Nanosecond
	time.Sleep(5 * time.Millisecond)
	gatesMu.Lock()
	lastSweep = time.Now().Add(-time.Hour)
	gatesMu.Unlock()
	_ = gateFor(t.TempDir()) // runs the sweep
	gatesMu.Lock()
	_, haveA := gates[indexKey(a)]
	_, haveB := gates[indexKey(b)]
	gatesMu.Unlock()
	if haveA {
		t.Error("the gate of an unused checkout was kept")
	}
	if !haveB {
		t.Error("the gate of a checkout in use was dropped")
	}
	_ = gb
}

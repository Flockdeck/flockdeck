package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// captureLog collects what the index refresh logs.
func captureLog(t *testing.T) func() string {
	t.Helper()
	var mu sync.Mutex
	var lines []string
	old := logf
	logf = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	t.Cleanup(func() { logf = old })
	return func() string { mu.Lock(); defer mu.Unlock(); return strings.Join(lines, "\n") }
}

func lockOf(repo string) string { return filepath.Join(repo, ".git", "index.lock") }

func exists(path string) bool { _, err := os.Lstat(path); return err == nil }

// fakeClock replaces the gate's clock for a test, and is moved by hand.
type fakeClock struct {
	mu sync.Mutex
	at time.Time
}

func newFakeClock(t *testing.T) *fakeClock {
	t.Helper()
	c := &fakeClock{at: time.Now()}
	old := setNow(func() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.at })
	t.Cleanup(func() { setNow(old) })
	return c
}

func (c *fakeClock) advance(d time.Duration) { c.mu.Lock(); c.at = c.at.Add(d); c.mu.Unlock() }

// A stand-in for update-index: it is told to block until released, and is never
// given a context that anything cancels, as the real process is not.
type blockedRefresh struct {
	release  chan struct{}
	runs     atomic.Int32
	live     atomic.Int32
	maxLive  atomic.Int32
	failWith error
}

func hangRefreshes(t *testing.T) *blockedRefresh {
	t.Helper()
	b := &blockedRefresh{release: make(chan struct{})}
	withHook(t, func(ctx context.Context, args []string) error {
		if len(args) == 0 || args[0] != "update-index" {
			return nil
		}
		b.runs.Add(1)
		n := b.live.Add(1)
		for {
			m := b.maxLive.Load()
			if n <= m || b.maxLive.CompareAndSwap(m, n) {
				break
			}
		}
		defer b.live.Add(-1)
		if ctx.Done() != nil {
			t.Error("the stand-in process was given a context that something can cancel")
		}
		<-b.release
		return b.failWith
	})
	t.Cleanup(func() {
		select {
		case <-b.release:
		default:
			close(b.release)
		}
	})
	return b
}

func (b *blockedRefresh) let() {
	select {
	case <-b.release:
	default:
		close(b.release)
	}
}

func eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("never happened: %s", what)
}

func shortStop(t *testing.T) {
	t.Helper()
	old := indexStopWaiting
	indexStopWaiting = 150 * time.Millisecond
	t.Cleanup(func() { indexStopWaiting = old })
}

// Waiting for the refresh stops at its bound, with the gate given back and the
// refresh left running: it is not killed, and the checkout is marked as having one
// in flight until the process has exited.
func TestARefreshNobodyWaitsForIsLeftRunningAndTheGateIsFree(t *testing.T) {
	shortStop(t)
	repo, _ := staleRepo(t, 3)
	b := hangRefreshes(t)
	err := RefreshIndex(repo)
	if !errors.Is(err, ErrRefreshRunning) || !errors.Is(err, ErrIndexBusy) {
		t.Fatalf("RefreshIndex = %v, want ErrRefreshRunning (an ErrIndexBusy)", err)
	}
	g := gateFor(repo)
	if !g.refreshing() {
		t.Error("the checkout is not marked as having a refresh in flight")
	}
	if b.live.Load() != 1 {
		t.Errorf("%d refresh processes live, want the one that was not killed", b.live.Load())
	}
	rel, err := tryWriteIndex(context.Background(), repo, time.Millisecond)
	if err != nil {
		t.Fatalf("the gate was not given back when waiting stopped: %v", err)
	}
	rel()
	b.let()
	eventually(t, "the flag is cleared when the process exits", func() bool { return !g.refreshing() })
	if g.backedOff() {
		t.Error("a refresh that nobody waited for, and that worked, backs the checkout off")
	}
}

// While one refresh of a checkout is in flight, however many are asked for, no
// second update-index is started beside it.
func TestTwoRefreshesOfOneCheckoutNeverOverlap(t *testing.T) {
	repo, _ := staleRepo(t, 3)
	b := hangRefreshes(t)
	// The first is waited for until the test says to stop, by its context, and not by
	// a timer that a slow start of a process could race.
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { first <- RefreshIndexCtx(ctx, repo) }()
	eventually(t, "the first refresh starts", func() bool { return b.live.Load() == 1 })
	cancel()
	if err := <-first; !errors.Is(err, ErrRefreshRunning) {
		t.Fatalf("first: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := RefreshIndex(repo); !errors.Is(err, ErrIndexBusy) {
				t.Errorf("a refresh beside one in flight = %v, want ErrIndexBusy", err)
			}
		}()
	}
	wg.Wait()
	if b.runs.Load() != 1 || b.maxLive.Load() != 1 {
		t.Errorf("%d update-index started, %d at once, want one", b.runs.Load(), b.maxLive.Load())
	}
	b.let()
	eventually(t, "the process exits", func() bool { return !gateFor(repo).refreshing() })
	if err := RefreshIndex(repo); err != nil {
		t.Errorf("a refresh after the first had exited: %v", err)
	}
	if b.runs.Load() != 2 {
		t.Errorf("%d runs, want a new one after the first exited", b.runs.Load())
	}
}

// A context that is cancelled, before or during, is not a failed refresh: no back-off
// and no failure counted. A refresh still running when waiting stopped is judged by
// how it ends.
func TestACancelledContextIsNotAFailedRefresh(t *testing.T) {
	repo, _ := staleRepo(t, 3)
	var ran atomic.Int32
	withHook(t, func(ctx context.Context, args []string) error {
		if len(args) > 0 && args[0] == "update-index" {
			ran.Add(1)
		}
		return nil
	})
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RefreshIndexCtx(gone, repo); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled context: %v, want its error", err)
	}
	g := gateFor(repo)
	if ran.Load() != 0 || g.failures != 0 || g.backedOff() {
		t.Errorf("a cancelled context started %d refreshes, %d failures, backed off %v", ran.Load(), g.failures, g.backedOff())
	}

	// Cancelled while the process runs, which then works.
	b := hangRefreshes(t)
	ctx, cancel2 := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RefreshIndexCtx(ctx, repo) }()
	eventually(t, "the process starts", func() bool { return b.live.Load() == 1 })
	cancel2()
	select {
	case err := <-done:
		if !errors.Is(err, ErrRefreshRunning) {
			t.Errorf("a cancelled wait = %v, want ErrRefreshRunning", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling did not end the wait")
	}
	if g.failures != 0 || g.backedOff() {
		t.Errorf("cancelling the wait counted a failure: %d, backed off %v", g.failures, g.backedOff())
	}
	b.let()
	eventually(t, "the process exits", func() bool { return !g.refreshing() })
	if g.failures != 0 || g.backedOff() {
		t.Errorf("a refresh whose waiter went away and which worked: %d failures, backed off %v", g.failures, g.backedOff())
	}
}

// A refresh that ran and failed backs the checkout off, 10 minutes, then an hour,
// then six hours, and a refresh that works starts over. The clock is by hand.
func TestARefreshThatFailsBacksOffMoreEachTime(t *testing.T) {
	clock := newFakeClock(t)
	repo, _ := staleRepo(t, 3)
	var fail atomic.Bool
	fail.Store(true)
	withHook(t, func(ctx context.Context, args []string) error {
		if len(args) > 0 && args[0] == "update-index" && fail.Load() {
			return errors.New("fatal: unable to write the index")
		}
		return nil
	})
	g := gateFor(repo)
	want := []time.Duration{10 * time.Minute, time.Hour, 6 * time.Hour, 6 * time.Hour}
	for i, step := range want {
		if err := RefreshIndex(repo); err == nil || errors.Is(err, ErrIndexBusy) {
			t.Fatalf("round %d: %v, want the failure of a refresh that ran", i, err)
		}
		g.mu.Lock()
		got := g.backoffUntil.Sub(clock.at)
		g.mu.Unlock()
		if got != step {
			t.Errorf("round %d: backed off for %v, want %v", i, got, step)
		}
		if err := RefreshIndex(repo); !errors.Is(err, ErrIndexBusy) {
			t.Errorf("round %d: a refresh during the back-off = %v, want ErrIndexBusy", i, err)
		}
		clock.advance(step + time.Second)
	}
	fail.Store(false)
	if err := RefreshIndex(repo); err != nil {
		t.Fatalf("a refresh after the back-off that works: %v", err)
	}
	if g.failures != 0 || g.backedOff() {
		t.Errorf("after a refresh that worked: %d failures, backed off %v", g.failures, g.backedOff())
	}
}

// One that runs past its long limit is said by the goroutine that waits for the process,
// after the caller has stopped waiting (the order in production: the caller stops
// after a minute and the limit is ten), and again every hour while it runs. One that
// ran past the limit backs the checkout off.
func TestARefreshThatRunsLongIsSaidAfterTheCallerStoppedWaiting(t *testing.T) {
	clock := newFakeClock(t)
	got := captureLog(t)
	oldLong, oldRepeat, oldStop := indexLongRun, indexLongRepeat, indexStopWaiting
	indexStopWaiting, indexLongRun, indexLongRepeat = 100*time.Millisecond, 300*time.Millisecond, 150*time.Millisecond
	t.Cleanup(func() { indexLongRun, indexLongRepeat, indexStopWaiting = oldLong, oldRepeat, oldStop })
	repo, _ := staleRepo(t, 3)
	b := hangRefreshes(t)
	if err := RefreshIndex(repo); !errors.Is(err, ErrRefreshRunning) {
		t.Fatal(err)
	}
	says := func() int { return strings.Count(got(), "has been running for") }
	eventually(t, "the first saying", func() bool { return says() >= 1 })
	eventually(t, "the saying again", func() bool { return says() >= 2 })
	clock.advance(11 * time.Minute) // by the clock the gate keeps, it ran past its limit
	b.let()
	g := gateFor(repo)
	eventually(t, "the process exits", func() bool { return !g.refreshing() })
	if !g.backedOff() {
		t.Error("a refresh that ran past its limit did not back the checkout off")
	}
	n := says()
	time.Sleep(400 * time.Millisecond)
	if says() != n {
		t.Error("the refresh was said to be running after it had exited")
	}
}

// A refresh in flight for more than an hour does not keep the checkout from being
// refreshed for ever: it is said, and one more is started, if no index.lock is there.
// The first is not killed.
func TestARefreshInFlightForAnHourLetsAnotherStart(t *testing.T) {
	clock := newFakeClock(t)
	got := captureLog(t)
	shortStop(t)
	repo, _ := staleRepo(t, 3)
	b := hangRefreshes(t)
	if err := RefreshIndex(repo); !errors.Is(err, ErrRefreshRunning) {
		t.Fatal(err)
	}
	clock.advance(30 * time.Minute)
	if err := RefreshIndex(repo); !errors.Is(err, ErrIndexBusy) || errors.Is(err, ErrRefreshRunning) || b.runs.Load() != 1 {
		t.Errorf("half an hour in: %v, %d runs, want ErrIndexBusy and no new run", err, b.runs.Load())
	}
	clock.advance(31 * time.Minute)
	if err := os.WriteFile(lockOf(repo), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RefreshIndex(repo); !errors.Is(err, ErrIndexBusy) || b.runs.Load() != 1 {
		t.Errorf("an hour in with a lock there: %v, %d runs, want none started", err, b.runs.Load())
	}
	if err := os.Remove(lockOf(repo)); err != nil {
		t.Fatal(err)
	}
	if err := RefreshIndex(repo); !errors.Is(err, ErrRefreshRunning) || b.runs.Load() != 2 {
		t.Errorf("an hour in: %v, %d runs, want a second started", err, b.runs.Load())
	}
	if !strings.Contains(got(), "in flight for more than") {
		t.Errorf("it was not said: %q", got())
	}
	if err := RefreshIndex(repo); !errors.Is(err, ErrIndexBusy) || b.runs.Load() != 2 {
		t.Errorf("straight after: %v, %d runs, want the new one to be waited for", err, b.runs.Load())
	}
}

// Where the lock is is not kept when it was not found, and is looked for again after
// ten minutes: a failure, a timeout or a worktree moved to another git directory is not
// remembered for the life of the process.
func TestTheLockPathIsNotKeptWhenItWasNotFoundAndExpires(t *testing.T) {
	clock := newFakeClock(t)
	repo, _ := staleRepo(t, 3)
	var asked atomic.Int32
	var fail atomic.Bool
	fail.Store(true)
	withHook(t, func(ctx context.Context, args []string) error {
		if len(args) > 1 && args[0] == "rev-parse" && args[1] == "--git-path" {
			asked.Add(1)
			if fail.Load() {
				return errors.New("fatal: not a git repository")
			}
		}
		return nil
	})
	_ = RefreshIndex(repo)
	_ = RefreshIndex(repo)
	if asked.Load() != 2 {
		t.Errorf("a failed lookup was asked %d times over 2 refreshes, want it asked each time", asked.Load())
	}
	fail.Store(false)
	_ = RefreshIndex(repo)
	_ = RefreshIndex(repo)
	if asked.Load() != 3 {
		t.Errorf("%d lookups, want the one that worked to be kept", asked.Load())
	}
	clock.advance(11 * time.Minute)
	_ = RefreshIndex(repo)
	if asked.Load() != 4 {
		t.Errorf("%d lookups, want a new one after ten minutes", asked.Load())
	}
}

// update-index that exits non-zero because a lock is there now is a collision with
// somebody's git, not a refresh that failed: nothing is counted.
func TestARefreshThatMetALockIsNotAFailure(t *testing.T) {
	repo, _ := staleRepo(t, 3)
	withHook(t, func(ctx context.Context, args []string) error {
		if len(args) > 0 && args[0] == "update-index" {
			_ = os.WriteFile(lockOf(repo), nil, 0o600) // somebody's commit began
			return errors.New("fatal: Unable to create index.lock: File exists.")
		}
		return nil
	})
	if err := RefreshIndex(repo); err == nil {
		t.Fatal("a refresh that failed returned no error")
	}
	g := gateFor(repo)
	if g.failures != 0 || g.backedOff() {
		t.Errorf("a lock collision counted: %d failures, backed off %v", g.failures, g.backedOff())
	}
}

// A gate with a refresh in flight is not swept away as idle: a second update-index
// would otherwise start beside the first.
func TestAGateWithARefreshInFlightIsNotSwept(t *testing.T) {
	shortStop(t)
	repo, _ := staleRepo(t, 3)
	other := newRepo(t)
	b := hangRefreshes(t)
	if err := RefreshIndex(repo); !errors.Is(err, ErrRefreshRunning) {
		t.Fatal(err)
	}
	oldTTL, oldEvery := gateTTL, gateSweepEvery
	gateTTL, gateSweepEvery = time.Millisecond, time.Millisecond
	t.Cleanup(func() { gateTTL, gateSweepEvery = oldTTL, oldEvery })
	time.Sleep(20 * time.Millisecond)
	gateFor(other) // sweeps what is idle
	if err := RefreshIndex(repo); !errors.Is(err, ErrIndexBusy) || b.runs.Load() != 1 {
		t.Errorf("after a sweep: %v, %d runs, want the one in flight waited for", err, b.runs.Load())
	}
}

// An index.lock that is there is never removed and never waited for: the refresh
// skips, counts nothing, and says once an hour that the lock is old.
func TestAnOldLockIsLeftAndSaidOnceAnHour(t *testing.T) {
	got := captureLog(t)
	repo, _ := staleRepo(t, 3)
	var ran atomic.Int32
	withHook(t, func(ctx context.Context, args []string) error {
		if len(args) > 0 && args[0] == "update-index" {
			ran.Add(1)
		}
		return nil
	})
	lock := lockOf(repo)
	if err := os.WriteFile(lock, []byte("left by something"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	clock := newFakeClock(t)
	count := func() int { return strings.Count(got(), "Flockdeck does not remove it") }
	for i := 0; i < 5; i++ {
		if err := RefreshIndex(repo); !errors.Is(err, ErrIndexBusy) {
			t.Fatalf("with a lock there: %v, want ErrIndexBusy", err)
		}
	}
	if count() != 1 {
		t.Errorf("the old lock was said %d times, want once: %q", count(), got())
	}
	clock.advance(61 * time.Minute)
	_ = RefreshIndex(repo)
	if count() != 2 {
		t.Errorf("the old lock was said %d times after an hour, want twice", count())
	}
	g := gateFor(repo)
	if ran.Load() != 0 || g.failures != 0 || g.backedOff() {
		t.Errorf("a lock made %d runs, %d failures, backed off %v, want none of them", ran.Load(), g.failures, g.backedOff())
	}
	if b, err := os.ReadFile(lock); err != nil || string(b) != "left by something" {
		t.Errorf("the lock was touched: %q, %v", b, err)
	}
}

// A lock younger than an hour is not said, though it is still skipped.
func TestAYoungLockIsSkippedQuietly(t *testing.T) {
	got := captureLog(t)
	repo, _ := staleRepo(t, 3)
	if err := os.WriteFile(lockOf(repo), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RefreshIndex(repo); !errors.Is(err, ErrIndexBusy) {
		t.Fatal(err)
	}
	if got() != "" {
		t.Errorf("a young lock was said: %q", got())
	}
}

// A GIT_INDEX_FILE this process was started with is not given to any git command,
// so the lock that is looked for is the checkout's own, and the status of the
// checkout is the checkout's.
func TestAnInheritedIndexFileIsNotGivenToGitAndDoesNotMoveTheLock(t *testing.T) {
	repo, _ := staleRepo(t, 3)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "elsewhere-index"))
	for _, kv := range gitEnv(nil) {
		if strings.HasPrefix(strings.ToUpper(kv), "GIT_INDEX_FILE=") {
			t.Fatalf("gitEnv carries %q", kv)
		}
	}
	has := false
	for _, kv := range gitEnv([]string{"GIT_INDEX_FILE=scratch"}) {
		has = has || kv == "GIT_INDEX_FILE=scratch"
	}
	if !has {
		t.Error("a scratch index that is asked for on purpose was stripped too")
	}
	if files, err := Changes(repo); err != nil || len(files) != 1 {
		t.Errorf("Changes with GIT_INDEX_FILE inherited = %+v, %v, want the checkout's own change", files, err)
	}
	var ran atomic.Int32
	withHook(t, func(ctx context.Context, args []string) error {
		if len(args) > 0 && args[0] == "update-index" {
			ran.Add(1)
		}
		return nil
	})
	if err := os.WriteFile(lockOf(repo), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RefreshIndex(repo); !errors.Is(err, ErrIndexBusy) || ran.Load() != 0 {
		t.Errorf("the checkout's own lock was not the one skipped: %v, %d runs", err, ran.Load())
	}
	if !exists(lockOf(repo)) {
		t.Error("the lock was removed")
	}
}

// The path of the lock is asked of git once for each checkout, not at every refresh.
func TestTheLockPathIsAskedOnce(t *testing.T) {
	repo, _ := staleRepo(t, 3)
	var asked atomic.Int32
	withHook(t, func(ctx context.Context, args []string) error {
		if len(args) > 1 && args[0] == "rev-parse" && args[1] == "--git-path" {
			asked.Add(1)
		}
		return nil
	})
	for i := 0; i < 4; i++ {
		_ = RefreshIndex(repo)
	}
	if asked.Load() != 1 {
		t.Errorf("git was asked for the lock's path %d times over 4 refreshes, want once", asked.Load())
	}
}

// A git commit -a holds the index lock while a slow pre-commit hook runs. A refresh
// that is cancelled around it, before or after, does not touch the lock, and the commit
// succeeds. Whether deleting a lock that git holds open is refused depends on the
// system: where it is allowed this fails if anything removes the lock, and where it is
// not it still fails if the refresh runs update-index beside the commit.
func TestALiveCommitKeepsItsLockWhileARefreshIsCancelled(t *testing.T) {
	repo, _ := staleRepo(t, 3)
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nsleep 3\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, repo, "README.md", "changed for the commit\n")

	// A refresh whose waiter is cancelled and which is still running, as the commit
	// begins: the gap in which anything that cleaned up after a refresh could remove
	// the commit's lock.
	b := hangRefreshes(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RefreshIndexCtx(ctx, repo) }()
	eventually(t, "the refresh starts", func() bool { return b.live.Load() == 1 })
	cancel()
	<-done

	commit := exec.Command("git", "commit", "-a", "-q", "-m", "a commit with a slow hook")
	commit.Dir = repo
	var out strings.Builder
	commit.Stdout, commit.Stderr = &out, &out
	if err := commit.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- commit.Wait() }()
	var before os.FileInfo // the lock, as first seen
	for i := 0; i < 500 && before == nil; i++ {
		if fi, err := os.Lstat(lockOf(repo)); err == nil {
			before = fi
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if before == nil {
		select {
		case err := <-finished:
			t.Skipf("the commit finished (%v) before its lock was seen, so the case was not made", err)
		default:
			t.Skip("git did not make its index.lock before running the hook here")
		}
	}
	// Both of the things a refresh might do while the commit holds its lock.
	b.failWith = errors.New("the stand-in ended") // it ends, and does not go on to a real git
	b.let()
	eventually(t, "the refresh process exits", func() bool { return !gateFor(repo).refreshing() })
	gateFor(repo).refreshWorked() // its failure is not what is being tried
	if err := RefreshIndexCtx(context.Background(), repo); !errors.Is(err, ErrIndexBusy) {
		t.Errorf("a refresh with the commit's lock there = %v, want ErrIndexBusy", err)
	}
	if !exists(lockOf(repo)) {
		t.Fatal("the commit's lock was removed while it was held")
	}
	if err := <-finished; err != nil {
		t.Fatalf("the user's commit failed: %v\n%s", err, out.String())
	}
	if exists(lockOf(repo)) {
		t.Error("the lock is still there after the commit")
	}
	if got := strings.TrimSpace(gitRun(t, repo, "log", "-1", "--format=%s")); got != "a commit with a slow hook" {
		t.Errorf("the commit is not there: %q", got)
	}
}

// ---- a real process that hangs, standing in for git

// fakeGit is what the copy of the test binary named git does, for the test that
// needs a real child process that does not come back: update-index makes the lock
// as git does, says it started, and holds on until it is told to finish, when it
// removes its own lock and exits. rev-parse --git-path says where the lock is.
func fakeGit() int {
	dir := os.Getenv("GITX_FAKE_DIR")
	args := os.Args[1:]
	for _, a := range args {
		if a == "rev-parse" {
			fmt.Println(filepath.ToSlash(filepath.Join(dir, "index.lock")))
			return 0
		}
	}
	for _, a := range args {
		if a == "update-index" {
			_ = os.WriteFile(filepath.Join(dir, "index.lock"), nil, 0o600)
			_ = os.WriteFile(filepath.Join(dir, "started"), []byte(fmt.Sprint(os.Getpid())), 0o600)
			for {
				if _, err := os.Lstat(filepath.Join(dir, "release")); err == nil {
					_ = os.Remove(filepath.Join(dir, "index.lock"))
					_ = os.WriteFile(filepath.Join(dir, "exited"), nil, 0o600)
					return 0
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
	}
	return 0
}

// The real part of what closing the application does: the waiter is cancelled while a
// real child process of the refresh is running. Waiting ends at once, the process is
// not signalled and keeps its lock, it is flagged in flight until it has really
// exited, and when it finishes it removes its own lock and the checkout is not backed
// off.
func TestCancellingTheWaitLeavesARealChildProcessRunning(t *testing.T) {
	if runtime.GOOS == "windows" && testing.Short() {
		t.Skip("copies the test binary")
	}
	self, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	bin := t.TempDir()
	name := "git"
	if runtime.GOOS == "windows" {
		name = "git.exe"
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Skip(err)
	}
	if err := os.WriteFile(filepath.Join(bin, name), data, 0o755); err != nil {
		t.Skip(err)
	}
	repo, _ := staleRepo(t, 3) // made with the real git, before PATH is changed
	fake := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GITX_FAKE_GIT", "1")
	t.Setenv("GITX_FAKE_DIR", fake)
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(fake, "release"), nil, 0o600) }) // the child never outlives the test

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RefreshIndexCtx(ctx, repo) }()
	eventually(t, "the child process starts", func() bool { return exists(filepath.Join(fake, "started")) })
	began := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, ErrRefreshRunning) {
			t.Errorf("a cancelled wait = %v, want ErrRefreshRunning", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling did not end the wait")
	}
	if took := time.Since(began); took > 2*time.Second {
		t.Errorf("stopping the wait took %v", took)
	}
	g := gateFor(repo)
	if !g.refreshing() {
		t.Error("the checkout is not flagged as having a refresh in flight")
	}
	time.Sleep(300 * time.Millisecond)
	if exists(filepath.Join(fake, "exited")) {
		t.Error("the child was ended by the cancel")
	}
	if !exists(filepath.Join(fake, "index.lock")) {
		t.Error("the child's lock was removed by somebody else")
	}
	if err := RefreshIndex(repo); !errors.Is(err, ErrIndexBusy) {
		t.Errorf("a second refresh beside the running child = %v, want ErrIndexBusy", err)
	}
	if err := os.WriteFile(filepath.Join(fake, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the child exits and the flag clears", func() bool { return exists(filepath.Join(fake, "exited")) && !g.refreshing() })
	if exists(filepath.Join(fake, "index.lock")) {
		t.Error("the child left its lock")
	}
	if g.failures != 0 || g.backedOff() {
		t.Errorf("a child that finished by itself: %d failures, backed off %v", g.failures, g.backedOff())
	}
}

// The real thing, on a checkout big enough for update-index to run for seconds:
// waiting stops long before it ends, nothing is signalled, git finishes by itself
// and removes its own lock, the user's commit works straight after, and the checkout
// is not backed off. It builds 20,000 files, which takes two minutes.
func TestARealLongRefreshIsLeftToFinishByItself(t *testing.T) {
	if testing.Short() || os.Getenv("FLOCKDECK_SLOW") == "" {
		t.Skip("builds a checkout of 20,000 files, which takes two minutes: set FLOCKDECK_SLOW=1")
	}
	old := indexStopWaiting
	indexStopWaiting = 1500 * time.Millisecond
	t.Cleanup(func() { indexStopWaiting = old })
	repo := staleBig(t, 20000)
	began := time.Now()
	err := RefreshIndex(repo)
	if err == nil {
		t.Skip("the refresh finished inside 1.5s here, so nobody stopped waiting for it")
	}
	if !errors.Is(err, ErrRefreshRunning) {
		t.Fatalf("RefreshIndex = %v, want ErrRefreshRunning", err)
	}
	t.Logf("waiting stopped after %v", time.Since(began))
	g := gateFor(repo)
	eventually2(t, 2*time.Minute, "git finishes by itself", func() bool { return !g.refreshing() })
	t.Logf("git finished after %v", time.Since(began))
	if exists(lockOf(repo)) {
		t.Fatal("index.lock is there after git finished by itself")
	}
	if g.failures != 0 || g.backedOff() {
		t.Errorf("a refresh that finished by itself: %d failures, backed off %v", g.failures, g.backedOff())
	}
	write(t, repo, "after.txt", "x\n")
	gitRun(t, repo, "add", "after.txt")
	gitRun(t, repo, "commit", "-q", "-m", "after the long refresh")
}

func eventually2(t *testing.T, limit time.Duration, what string, f func() bool) {
	t.Helper()
	for end := time.Now().Add(limit); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if f() {
			return
		}
	}
	t.Fatalf("never happened: %s", what)
}

// staleBig is a checkout of n files with stale stat data, made quickly, for the
// tests that need a refresh that takes seconds.
func staleBig(t *testing.T, n int) string {
	t.Helper()
	repo := newRepo(t)
	for i := 0; i < n; i++ {
		d := filepath.Join(repo, fmt.Sprintf("d%03d", i%100))
		_ = os.MkdirAll(d, 0o700)
		write(t, d, fmt.Sprintf("f%d.txt", i), fmt.Sprintf("file %d\nbody\n", i))
	}
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-q", "-m", "many")
	later := time.Now().Add(time.Hour)
	for i := 0; i < n; i++ {
		later = later.Add(time.Second)
		_ = os.Chtimes(filepath.Join(repo, fmt.Sprintf("d%03d", i%100), fmt.Sprintf("f%d.txt", i)), later, later)
	}
	return repo
}

// Per-run control of the stand-in for update-index: each run blocks until it is told
// to end, with the error it is told to end with.
type eachRefresh struct {
	mu    sync.Mutex
	runs  int
	chans map[int]chan error
}

func hangEach(t *testing.T) *eachRefresh {
	t.Helper()
	e := &eachRefresh{chans: map[int]chan error{}}
	withHook(t, func(ctx context.Context, args []string) error {
		if len(args) == 0 || args[0] != "update-index" {
			return nil
		}
		e.mu.Lock()
		e.runs++
		ch := make(chan error, 1)
		e.chans[e.runs] = ch
		e.mu.Unlock()
		return <-ch
	})
	return e
}

func (e *eachRefresh) count() int { e.mu.Lock(); defer e.mu.Unlock(); return e.runs }

// end tells run n to end with err, once it has started.
func (e *eachRefresh) end(t *testing.T, n int, err error) {
	t.Helper()
	eventually(t, "the run starts", func() bool { return e.count() >= n })
	e.mu.Lock()
	ch := e.chans[n]
	e.mu.Unlock()
	ch <- err
}

// Never more than two update-index processes of one checkout are alive: the one after
// an hour is allowed, a third is refused and said once.
func TestNoMoreThanTwoRefreshesOfOneCheckoutAreAlive(t *testing.T) {
	clock := newFakeClock(t)
	got := captureLog(t)
	shortStop(t)
	repo, _ := staleRepo(t, 3)
	e := hangEach(t)
	defer func() {
		e.end(t, 1, errors.New("done"))
		e.end(t, 2, errors.New("done"))
		g := gateFor(repo)
		eventually(t, "both have exited", func() bool { g.mu.Lock(); defer g.mu.Unlock(); return g.live == 0 })
	}()
	if err := RefreshIndex(repo); !errors.Is(err, ErrRefreshRunning) {
		t.Fatal(err)
	}
	clock.advance(61 * time.Minute)
	if err := RefreshIndex(repo); !errors.Is(err, ErrRefreshRunning) || e.count() != 2 {
		t.Fatalf("an hour in: %v, %d runs, want a second started", err, e.count())
	}
	for i := 0; i < 3; i++ {
		clock.advance(61 * time.Minute)
		if err := RefreshIndex(repo); !errors.Is(err, ErrIndexBusy) || errors.Is(err, ErrRefreshRunning) || e.count() != 2 {
			t.Errorf("with two alive: %v, %d runs, want ErrIndexBusy and no third", err, e.count())
		}
	}
	if n := strings.Count(got(), "not starting another"); n < 1 {
		t.Errorf("the refusal was not said: %q", got())
	}
}

// When the newer refresh finishes while the older is still alive the checkout stays in
// flight, so that a third does not start beside the old one; and when the old one ends
// late it records nothing against the gate: no failure, no back-off.
func TestAnOlderRefreshThatOutlivesTheNewerKeepsTheFlagAndRecordsNothing(t *testing.T) {
	clock := newFakeClock(t)
	got := captureLog(t)
	shortStop(t)
	repo, _ := staleRepo(t, 3)
	e := hangEach(t)
	if err := RefreshIndex(repo); !errors.Is(err, ErrRefreshRunning) {
		t.Fatal(err)
	}
	clock.advance(61 * time.Minute)
	if err := RefreshIndex(repo); !errors.Is(err, ErrRefreshRunning) {
		t.Fatal(err)
	}
	g := gateFor(repo)
	e.end(t, 2, nil) // the newer finishes first (and then really runs git, which works)
	eventually(t, "the newer has exited", func() bool { g.mu.Lock(); defer g.mu.Unlock(); return g.live == 1 })
	if !g.refreshing() {
		t.Error("the checkout is not in flight with the older refresh still alive")
	}
	if err := RefreshIndex(repo); !errors.Is(err, ErrIndexBusy) || e.count() != 2 {
		t.Errorf("a third while the older lives: %v, %d runs, want it refused", err, e.count())
	}
	e.end(t, 1, errors.New("fatal: the old one failed late"))
	eventually(t, "the older has exited", func() bool { g.mu.Lock(); defer g.mu.Unlock(); return g.live == 0 })
	if g.failures != 0 || g.backedOff() {
		t.Errorf("a late old exit recorded %d failures, backed off %v", g.failures, g.backedOff())
	}
	if g.refreshing() {
		t.Error("the checkout is still in flight with none alive")
	}
	if !strings.Contains(got(), "an older index refresh") {
		t.Errorf("the late exit was not said: %q", got())
	}
}

// An exit status of 128, or the git directory the lock path was found in being gone (a
// worktree removed and added again under another name), drops the cached lock path.
type exit128 struct{}

func (exit128) Error() string { return "exit status 128" }
func (exit128) ExitCode() int { return 128 }

func TestTheLockPathIsDroppedWhenARefreshExits128OrItsDirectoryIsGone(t *testing.T) {
	repo, _ := staleRepo(t, 3)
	var asked atomic.Int32
	var fail atomic.Bool
	withHook(t, func(ctx context.Context, args []string) error {
		if len(args) > 1 && args[0] == "rev-parse" && args[1] == "--git-path" {
			asked.Add(1)
		}
		if len(args) > 0 && args[0] == "update-index" && fail.Load() {
			return exit128{}
		}
		return nil
	})
	if err := RefreshIndex(repo); err != nil {
		t.Fatal(err)
	}
	if err := RefreshIndex(repo); err != nil || asked.Load() != 1 {
		t.Fatalf("setup: %v, %d lookups, want the path kept", err, asked.Load())
	}
	fail.Store(true)
	_ = RefreshIndex(repo) // exits 128
	fail.Store(false)
	gateFor(repo).refreshWorked()
	_ = RefreshIndex(repo)
	if asked.Load() != 2 {
		t.Errorf("%d lookups after an exit 128, want the path asked again", asked.Load())
	}
	// The directory the path was found in is gone.
	g := gateFor(repo)
	g.mu.Lock()
	g.lockPath, g.lockAt = filepath.Join(t.TempDir(), "removed-admin", "index.lock"), now()
	g.mu.Unlock()
	_ = RefreshIndex(repo)
	if asked.Load() != 3 {
		t.Errorf("%d lookups with the lock's directory gone, want the path asked again", asked.Load())
	}
}

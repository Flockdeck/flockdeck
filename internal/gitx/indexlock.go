package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// What these guard, and why they are built the way they are.
//
// git diff and git update-index --refresh rewrite the index when they find a
// file's recorded stat data stale: git diff does it whatever GIT_OPTIONAL_LOCKS
// says, by renaming a lock file over the index. A git status that opens the index
// as that happens fails on Windows with "index file open failed: Permission
// denied". Changes starts a status and a diff together, and the header's refresh
// of a slow checkout runs update-index while a review panel is open.
//
// Turning off diff's refresh (diff.autoRefreshIndex=false) removed the write and
// cost: on 20,000 files with stale stat data Changes took about 7.0s on every
// call where it took 9.1s the first time and about 2.8s after, because the diff
// then never records what it learned. Making status and diff take turns cost 11.2s
// the first time and 5.0s after. So the commands keep running side by side, and
// the part that can be kept apart is kept apart, under four rules:
//
//   - a command that only reads (status, numstat, diff) never waits behind a
//     writer that is merely waiting its turn, and waits for one that is running
//     for a bounded time only, indexReaderWait, after which it runs anyway,
//     because a read refused the index is asked again (retryIndex);
//   - RefreshIndex, which is optional housekeeping, waits at most indexWriterWait
//     for the readers to finish, and if they do not it does not run
//     (ErrIndexBusy) and is tried again later;
//   - what it runs is not killed, ever, and waiting for it stops after
//     indexStopWaiting, when the gate is given back and git is left to finish and
//     remove its own lock; no lock is removed by anybody else (see RefreshIndex);
//   - every wait ends when the caller's context does.
//
// What this does not cover is another program, an agent's own git among them.
type indexGate struct {
	mu      sync.Mutex
	readers int
	// active is true while a writer is running; activeDone is closed when it
	// stops. pending is set while a writer waits for the readers to finish, and is
	// closed when the last of them has, which is when the writer becomes active.
	active     bool
	activeDone chan struct{}
	pending    chan struct{}
	used       time.Time

	// busyRuns counts the refreshes in a row that found the readers never let go.
	// After indexBusyBeforeDrain of them the next one raises drain for
	// indexDrainWait: readers that arrive while it is up wait for it to end, at
	// most that long, so that the ones already running can finish and the refresh
	// can get in. Without it, readers overlapping continuously starve the refresh
	// for ever.
	busyRuns   int
	drainUntil time.Time
	drainCh    chan struct{}

	// failures counts the refreshes of this checkout that ran and failed or ran past
	// indexLongRun, and backoffUntil is when the next may run: after one the
	// checkout is left alone for 10 minutes, then an hour, then six, and a refresh
	// that works starts the count again. A refresh that was not run, that was
	// cancelled, that met a lock somebody else holds, or that is not the newest when
	// it exits is not counted.
	failures     int
	backoffUntil time.Time

	// inflight is true while an update-index process is alive, whoever stopped waiting
	// for it. No second one is started for an hour; after that one more may be, and
	// never more than two are alive (live counts them), so a refresh that never exits
	// does not collect more. Only the newest, by its token, records an outcome.
	// lockPath is where the checkout's index.lock was found to be, and is asked of git
	// again after indexLockPathTTL, when a refresh exits 128, or when its directory is
	// gone; lockLogged is when an old lock was last said.
	inflight      bool
	inflightSince time.Time
	inflightTok   int
	live          int       // update-index processes started and not yet exited
	capLogged     time.Time // when "not starting another" was last said
	lockPath      string
	lockAt        time.Time
	lockLogged    time.Time
}

var (
	// ErrIndexBusy is what RefreshIndex returns when it did nothing: the readers
	// did not let go in time, a lock file of somebody else's is there, or the
	// checkout is backing off after a refresh that failed, or one is in flight.
	ErrIndexBusy = errors.New("the index was in use, so it was not refreshed")

	// indexReaderWait is the longest a reader waits, in all, for a writer that is
	// running or a drain that is up.
	// 500 ms and not a second: with a refresh loop running, the third Changes call on
	// 20,000 stale files took 4.0 s with a second (3.05 s on main) and 3.5 s with this.
	indexReaderWait = 500 * time.Millisecond
	// indexWriterWait is the longest RefreshIndex waits for the readers.
	indexWriterWait = 2 * time.Second
	// indexBusyBeforeDrain and indexDrainWait: see indexGate.
	indexBusyBeforeDrain = 3
	indexDrainWait       = time.Second
	// indexBackoffs are how long a checkout is left alone after its first, second
	// and later refreshes that ran and failed, or ran past indexLongRun.
	indexBackoffs = []time.Duration{10 * time.Minute, time.Hour, 6 * time.Hour}

	gatesMu   sync.Mutex
	gates     = map[string]*indexGate{}
	lastSweep = time.Now()
)

// gateTTL is how long a checkout's gate is kept unused, and gateSweepEvery how
// often unused ones are looked for.
var (
	gateTTL        = time.Hour
	gateSweepEvery = 10 * time.Minute
)

func gateFor(dir string) *indexGate {
	key := indexKey(dir)
	gatesMu.Lock()
	defer gatesMu.Unlock()
	if time.Since(lastSweep) > gateSweepEvery {
		lastSweep = time.Now()
		for k, g := range gates {
			g.mu.Lock()
			idle := g.readers == 0 && !g.active && g.pending == nil && !g.inflight && time.Since(g.used) > gateTTL &&
				now().After(g.backoffUntil)
			g.mu.Unlock()
			if idle {
				delete(gates, k)
			}
		}
	}
	g := gates[key]
	if g == nil {
		g = &indexGate{}
		gates[key] = g
	}
	g.mu.Lock()
	g.used = now()
	g.mu.Unlock()
	return g
}

// readIndex marks the start of a command that reads the checkout's index and may
// rewrite it beside others like it, and returns what ends it. It waits, for
// indexReaderWait in all, while a writer is running or a drain is up, looking
// again each time one ends, and then goes ahead. The error is ctx's, if it ended
// the wait first.
func readIndex(ctx context.Context, dir string) (release func(), err error) {
	g := gateFor(dir)
	deadline := time.Now().Add(indexReaderWait)
	for {
		g.mu.Lock()
		var wait chan struct{}
		if g.active {
			wait = g.activeDone
		} else if g.drainCh != nil && time.Now().Before(g.drainUntil) {
			wait = g.drainCh
		}
		remaining := time.Until(deadline)
		if wait == nil || remaining <= 0 {
			g.readers++
			g.mu.Unlock()
			return func() { g.endRead() }, nil
		}
		g.mu.Unlock()
		timer := time.NewTimer(remaining)
		select {
		case <-wait:
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		}
		timer.Stop()
	}
}

func (g *indexGate) endRead() {
	g.mu.Lock()
	g.readers--
	if g.readers == 0 && g.pending != nil {
		// The writer's turn: it is active from this moment, so that no reader that
		// arrives after is let in beside it without waiting its bound.
		g.active = true
		g.activeDone = make(chan struct{})
		close(g.pending)
		g.pending = nil
	}
	g.mu.Unlock()
}

// tryWriteIndex waits up to wait for the readers of the checkout's index to finish
// and takes the index to itself, or says ErrIndexBusy. Readers that arrive while it
// waits are not held up, except for the drain that follows repeated failures.
func tryWriteIndex(ctx context.Context, dir string, wait time.Duration) (release func(), err error) {
	g := gateFor(dir)
	g.mu.Lock()
	if g.active || g.pending != nil {
		g.mu.Unlock()
		return nil, ErrIndexBusy
	}
	if g.readers == 0 {
		g.active = true
		g.activeDone = make(chan struct{})
		g.busyRuns = 0
		g.mu.Unlock()
		return g.endWrite, nil
	}
	p := make(chan struct{})
	g.pending = p
	if g.busyRuns >= indexBusyBeforeDrain && g.drainCh == nil {
		// The refresh has been turned away again and again: for a moment new
		// readers wait, so that the ones running can finish.
		ch := make(chan struct{})
		g.drainCh = ch
		g.drainUntil = time.Now().Add(indexDrainWait)
		time.AfterFunc(indexDrainWait, func() { g.endDrain(ch) })
	}
	g.mu.Unlock()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	var cause error
	select {
	case <-p:
		g.mu.Lock()
		g.busyRuns = 0
		g.mu.Unlock()
		return g.endWrite, nil
	case <-timer.C:
		cause = ErrIndexBusy
	case <-ctx.Done():
		cause = ctx.Err()
	}
	g.mu.Lock()
	if g.pending == p {
		g.pending = nil
		if cause == ErrIndexBusy {
			g.busyRuns++
		}
		g.mu.Unlock()
		return nil, cause
	}
	g.mu.Unlock()
	// The last reader finished as the wait ran out: the writer is already active.
	return g.endWrite, nil
}

func (g *indexGate) endDrain(ch chan struct{}) {
	g.mu.Lock()
	if g.drainCh == ch {
		g.drainCh = nil
		close(ch)
	}
	g.mu.Unlock()
}

func (g *indexGate) endWrite() {
	g.mu.Lock()
	g.active = false
	close(g.activeDone)
	if g.drainCh != nil {
		close(g.drainCh)
		g.drainCh = nil
	}
	g.mu.Unlock()
}

// backedOff says whether the checkout is being left alone after a refresh that
// failed.
func (g *indexGate) backedOff() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return now().Before(g.backoffUntil)
}

// refreshFailed notes a refresh that ran and failed, or ran for more than
// indexLongRun, and starts the back-off; refreshWorked ends it. A refresh that was
// not run, or that nobody waited for, is neither.
func (g *indexGate) refreshFailed() {
	g.mu.Lock()
	defer g.mu.Unlock()
	i := g.failures
	if i >= len(indexBackoffs) {
		i = len(indexBackoffs) - 1
	}
	g.failures++
	g.backoffUntil = now().Add(indexBackoffs[i])
}

func (g *indexGate) refreshWorked() {
	g.mu.Lock()
	g.failures = 0
	g.backoffUntil = time.Time{}
	g.mu.Unlock()
}

// keyEntry is a checkout's key as it was worked out, and when.
type keyEntry struct {
	key string
	at  time.Time
}

var (
	keyMu    sync.Mutex
	keyCache = map[string]keyEntry{}
)

// indexKeyTTL is how long a folder's checkout key is believed, and indexKeyWait how
// long working it out may take before the folder is simply its own key: finding it
// looks at the file system, and a drive that has gone away would hold a command up
// before git was even started.
var (
	indexKeyTTL  = 30 * time.Second
	indexKeyWait = time.Second
)

// indexKey names the checkout dir is in, by its top-level folder, found without a
// git process: the nearest folder above dir with a .git, a file in a linked
// worktree and a folder in the main one. A folder with none is its own key. The
// answer is kept for indexKeyTTL, and what it cost to find is bounded by
// indexKeyWait.
func indexKey(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	keyMu.Lock()
	if e, ok := keyCache[abs]; ok && time.Since(e.at) < indexKeyTTL {
		keyMu.Unlock()
		return e.key
	}
	keyMu.Unlock()

	found := make(chan string, 1)
	fn := findTopFn
	go func() { found <- fn(abs) }()
	var key string
	timer := time.NewTimer(indexKeyWait)
	select {
	case key = <-found:
	case <-timer.C:
		key = foldPath(abs)
	}
	timer.Stop()

	keyMu.Lock()
	if len(keyCache) >= 1024 {
		keyCache = map[string]keyEntry{}
	}
	keyCache[abs] = keyEntry{key: key, at: time.Now()}
	keyMu.Unlock()
	return key
}

// findTopFn is findCheckoutTop, a variable so a test can make it slow.
var findTopFn = findCheckoutTop

// findCheckoutTop walks up from abs to the folder with a .git in it.
func findCheckoutTop(abs string) string {
	for d := abs; ; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return foldPath(d)
		}
		parent := filepath.Dir(d)
		if parent == d {
			return foldPath(abs)
		}
		d = parent
	}
}

// transientIndexError reports whether err is git being refused the index because
// something was replacing it at that moment, which is over in a moment.
func transientIndexError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "index file open failed") ||
		(strings.Contains(msg, "Permission denied") && strings.Contains(msg, "index"))
}

// indexRetryWait is how long a command refused the index waits before asking
// again, and indexRetries how many times it asks again. A variable so a test need
// not wait.
var (
	indexRetryWait = 100 * time.Millisecond
	indexRetries   = 3
)

// retryIndex runs f, and again, up to indexRetries more times, while it fails
// for a transient refusal of the index.
func retryIndex(f func() error) error {
	err := f()
	for i := 0; i < indexRetries && transientIndexError(err); i++ {
		time.Sleep(indexRetryWait)
		err = f()
	}
	return err
}

// now is the clock of the gate's back-off and of the age of a lock; a test sets its
// own (setNow) so that it need not sleep.
func now() time.Time { return (*clock.Load())() }

var clock atomic.Pointer[func() time.Time]

func init() { setNow(time.Now) }

// setNow replaces the clock and returns the one it replaced; the clock is read by
// goroutines that outlive a call, so it is not a plain variable.
func setNow(f func() time.Time) func() time.Time {
	old := clock.Swap(&f)
	if old == nil {
		return time.Now
	}
	return *old
}

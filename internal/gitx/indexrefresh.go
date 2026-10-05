package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jmwri/flockdeck/internal/sysproc"
)

// RefreshIndex writes down what git found about files that were touched
// without changing, which the pane headers' status never does.
//
// That status takes no lock (GIT_OPTIONAL_LOCKS=0), so that polling the
// headers never holds the index lock an agent's commit needs, and so it never
// writes back what it learned either. A checkout whose files were all touched
// at once -- by a build, or by a checkout another tool made -- had every one
// of them read again by every status after, five and a half seconds a time on
// a checkout of sixty thousand files, until something else refreshed the
// index. This refreshes it.
//
// It is housekeeping, and it never gets in anybody's way and never touches
// anything of anybody's:
//
//   - It does nothing, with ErrIndexBusy, when an index.lock is already there
//     (an agent's commit, say; or one left behind, which is said in the log once
//     an hour and never removed), when the commands that read the index did not
//     let go within indexWriterWait, when a refresh of this checkout is still
//     running, or when the checkout is backing off.
//   - It never kills update-index and never removes a lock, whatever the reason:
//     a git killed while it writes the index is how a lock is left behind, and a
//     lock found afterwards cannot be known to be that git's and not a commit's
//     that began since. Waiting stops instead, after indexStopWaiting or when ctx
//     ends (the application closing), with ErrRefreshRunning: the gate is let go,
//     and git finishes, and removes its own lock, by itself. The checkout is
//     marked as having a refresh in flight until the process has really exited, so
//     that no second one is started beside it. One still running after
//     indexLongRun is said in the log, and again every hour it still runs, by the
//     goroutine that waits for the process, whoever else has stopped waiting. One
//     in flight for more than indexStuckAfter (a stuck network drive) no longer
//     keeps the checkout from being refreshed: it is said, and another may be
//     started, once an hour, if no index.lock is there. None is ever killed.
//   - Only a refresh that ran and exited with an error, or ran past indexLongRun,
//     leaves the checkout alone afterwards, for 10 minutes, then an hour, then six
//     hours. One that was never started, whose waiter went away, that exited
//     because a lock was there when it ended (a collision with somebody's git), or
//     that is not the newest refresh when it exits, is nothing.
func RefreshIndex(dir string) error { return RefreshIndexCtx(context.Background(), dir) }

// ErrRefreshRunning is the result of a refresh that nobody waits for any longer: it
// is still running, and will finish by itself. It is an ErrIndexBusy as far as
// anyone asking whether to try again is concerned.
var ErrRefreshRunning = fmt.Errorf("%w: a refresh is still running", ErrIndexBusy)

var (
	// indexStopWaiting is how long RefreshIndex waits for update-index.
	indexStopWaiting = 60 * time.Second
	// indexLongRun is how long update-index may run before it is said to have.
	indexLongRun = 10 * time.Minute
	// indexOldLock is how old an index.lock is before it is said to be old.
	indexOldLock = time.Hour
	// indexLongRepeat is how often a refresh that still runs is said again.
	indexLongRepeat = time.Hour
	// indexStuckAfter is how long a refresh may be in flight before another is allowed.
	indexStuckAfter = time.Hour
	// indexLockPathTTL is how long where the lock is stays known.
	indexLockPathTTL = 10 * time.Minute
)

// RefreshIndexCtx is RefreshIndex stopped being waited for by ctx as well: a
// cancelled ctx -- the application closing -- ends the wait and nothing else. A
// ctx already cancelled starts nothing.
func RefreshIndexCtx(ctx context.Context, dir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	g := gateFor(dir)
	if g.backedOff() || g.refreshing() {
		return ErrIndexBusy
	}
	if g.lockIsThere(ctx, dir) {
		return ErrIndexBusy
	}
	release, err := tryWriteIndex(ctx, dir, indexWriterWait)
	if err != nil {
		return err
	}
	defer release()
	tok := g.startRefresh(dir)
	if tok == 0 {
		return ErrIndexBusy
	}
	started := now()
	realStart := time.Now()
	firstSaid, saidAgain := indexLongRun, indexLongRepeat
	done := make(chan error, 1)
	go func() {
		// This goroutine waits for the process, however long, and says so while it
		// does: the caller stopped waiting long ago.
		stop := make(chan struct{})
		go func() {
			for wait := firstSaid; ; wait = saidAgain {
				select {
				case <-stop:
					return
				case <-time.After(wait):
					logf("index refresh for %s has been running for %d minutes; it is left to finish by itself", dir, int(time.Since(realStart).Minutes()))
				}
			}
		}()
		err := runUpdateIndex(dir)
		close(stop)
		took := now().Sub(started)
		if err != nil && exitStatus(err) == 128 {
			g.dropLockPath() // git could not use the path it was thought to be at
		}
		switch {
		case !g.isNewest(tok):
			// An older refresh that outlived a newer one says how it ended, once, and
			// records nothing: the gate's outcome is the newest refresh's.
			logf("an older index refresh for %s has exited: %v", dir, err)
		case err != nil && g.lockExists():
			// A collision with somebody's lock is not a refresh that failed.
		case err != nil:
			g.refreshFailed()
		case took > indexLongRun:
			g.refreshFailed()
		default:
			g.refreshWorked()
		}
		g.endRefresh()
		done <- err
	}()
	timer := time.NewTimer(indexStopWaiting)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
	case <-ctx.Done():
	}
	return ErrRefreshRunning
}

// refreshing says whether a refresh of the checkout is in flight, and the newest has
// been for less than indexStuckAfter.
func (g *indexGate) refreshing() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.live > 0 && now().Sub(g.inflightSince) < indexStuckAfter
}

// startRefresh counts a refresh as live and returns its token, or 0 if none may
// start. None may while one is live and has been for less than indexStuckAfter. After
// that one more may, so that a checkout whose refresh is stuck is not left for ever,
// and never more than two are live at once: with two, nothing more is started (and
// that is said once an hour). The token says which is the newest, whose outcome is
// the one the gate records.
func (g *indexGate) startRefresh(dir string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case g.live >= 2:
		if g.capLogged.IsZero() || now().Sub(g.capLogged) > time.Hour {
			g.capLogged = now()
			logf("%d index refreshes for %s are still running; not starting another", g.live, dir)
		}
		return 0
	case g.live == 1:
		if now().Sub(g.inflightSince) < indexStuckAfter {
			return 0
		}
		logf("index refresh for %s has been in flight for more than %v; one more is tried, and the first is not killed", dir, indexStuckAfter)
	}
	g.live++
	g.inflight = true
	g.inflightSince = now()
	g.inflightTok++
	return g.inflightTok
}

// isNewest says whether tok is the newest refresh started.
func (g *indexGate) isNewest(tok int) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inflightTok == tok
}

// endRefresh counts a refresh as no longer live. The checkout is in flight until none is.
func (g *indexGate) endRefresh() {
	g.mu.Lock()
	g.live--
	if g.live <= 0 {
		g.live = 0
		g.inflight = false
	}
	g.mu.Unlock()
}

// dropLockPath forgets where the lock is, to be asked of git again.
func (g *indexGate) dropLockPath() {
	g.mu.Lock()
	g.lockPath = ""
	g.mu.Unlock()
}

// exitStatus is the exit status a failed command had, or -1; anything with an
// ExitCode method will do.
func exitStatus(err error) int {
	var e interface{ ExitCode() int }
	if errors.As(err, &e) {
		return e.ExitCode()
	}
	return -1
}

// lockExists says whether the lock is there now, by where it was last found to be.
func (g *indexGate) lockExists() bool {
	g.mu.Lock()
	lock := g.lockPath
	g.mu.Unlock()
	if lock == "" {
		return false
	}
	_, err := os.Lstat(lock)
	return err == nil
}

// runUpdateIndex runs update-index and returns when it has exited. It is not given
// a context: nothing here ends it.
func runUpdateIndex(dir string) error {
	args := []string{"update-index", "-q", "--refresh"}
	if gitHook != nil {
		// The hook stands in for the process, in a test: it is given a context nothing
		// cancels, as the real process would be.
		if err := gitHook(context.Background(), args); err != nil {
			return err
		}
	}
	cmd := exec.Command("git", append(append([]string{}, ownConfig...), args...)...)
	cmd.Dir = dir
	sysproc.NoWindow(cmd)
	cmd.Env = gitEnv(nil)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	tree := newTree()
	err := tree.start(cmd)
	if err == nil {
		err = cmd.Wait()
	}
	tree.settle(false)
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return &commandError{msg: "git update-index: " + msg, err: err}
	}
	return nil
}

// lockIsThere says whether the checkout's index.lock exists. Where it is is asked of
// git, and kept for indexLockPathTTL (with GIT_INDEX_FILE out of the environment, as it is
// out of every git command's, so the index and its lock are the checkout's own). A
// lock more than indexOldLock old is said in the log, once an hour for each
// checkout, and is not touched.
func (g *indexGate) lockIsThere(ctx context.Context, dir string) bool {
	g.mu.Lock()
	lock := g.lockPath
	fresh := lock != "" && now().Sub(g.lockAt) < indexLockPathTTL
	g.mu.Unlock()
	if fresh {
		// A worktree removed and added again under another name has its git directory
		// elsewhere: the one the path was in is gone.
		if _, err := os.Lstat(filepath.Dir(lock)); err != nil {
			fresh = false
		}
	}
	if !fresh {
		// Only a path that was found is kept, and for a while: a failure, a timeout,
		// or a worktree that was moved is asked again next time.
		lock = indexLockPath(ctx, dir)
		if lock == "" {
			return false
		}
		g.mu.Lock()
		g.lockPath, g.lockAt = lock, now()
		g.mu.Unlock()
	}
	info, err := os.Lstat(lock)
	if err != nil {
		return false
	}
	if age := now().Sub(info.ModTime()); age > indexOldLock {
		g.mu.Lock()
		say := g.lockLogged.IsZero() || now().Sub(g.lockLogged) > time.Hour
		if say {
			g.lockLogged = now()
		}
		g.mu.Unlock()
		if say {
			logf("index.lock has been there for %d minutes in %s (another git is running or it was left behind; Flockdeck does not remove it)", int(age.Minutes()), dir)
		}
	}
	return true
}

// indexLockPath is where git keeps the lock for the checkout's index, or "" if git
// cannot say.
func indexLockPath(ctx context.Context, dir string) string {
	out, err := runEnvOut(ctx, dir, nil, "rev-parse", "--git-path", "index.lock")
	if err != nil || out == "" {
		return ""
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(dir, out)
	}
	return out
}

// logf logs what the index refresh did that nobody asked it for. The function that
// does is replaced by a test (setLogf) so that it can read what was said; it is read
// by goroutines that outlive a call, so it is not a plain variable.
func logf(format string, args ...any) { (*logger.Load())(format, args...) }

var logger atomic.Pointer[func(string, ...any)]

func init() { setLogf(log.Printf) }

// setLogf replaces the function logf says things with, and returns the one it replaced.
func setLogf(f func(string, ...any)) func(string, ...any) {
	old := logger.Swap(&f)
	if old == nil {
		return log.Printf
	}
	return *old
}

package filerewind

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Live runs are the ones some Flockdeck process still holds checkpoints for. A
// run is the second part of a checkpoint ref, refs/flockdeck/rewind/<run>/.
//
// This process knows its own runs. Another Flockdeck process on the same
// checkout shows that its runs are live with a marker file in the git folder,
// which the owner touches every heartbeatEvery for as long as the run is
// registered and removes when it ends. A marker not touched for liveTTL is the
// leftover of a process that died.
var (
	liveMu   sync.Mutex
	liveRuns = map[string]*liveRun{}
	swept    sync.Map // repository roots swept by this process
)

type liveRun struct {
	n    int
	stop chan struct{}
}

const (
	liveDirName    = "flockdeck-rewind-live"
	heartbeatEvery = 5 * time.Minute
	liveTTL        = 30 * time.Minute
)

// Register says run is live, for the checkout that holds dir, until Unregister.
func Register(dir, run string) {
	liveMu.Lock()
	defer liveMu.Unlock()
	if l := liveRuns[run]; l != nil {
		l.n++
		return
	}
	l := &liveRun{n: 1, stop: make(chan struct{})}
	liveRuns[run] = l
	go beat(dir, run, l.stop)
}

// Unregister ends a Register.
func Unregister(run string) {
	liveMu.Lock()
	defer liveMu.Unlock()
	l := liveRuns[run]
	if l == nil {
		return
	}
	if l.n--; l.n <= 0 {
		close(l.stop)
		delete(liveRuns, run)
	}
}

func isLive(run string) bool {
	liveMu.Lock()
	defer liveMu.Unlock()
	return liveRuns[run] != nil
}

// beat keeps the marker of a run fresh until stop is closed, then removes it.
func beat(dir, run string, stop chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	r, err := locate(ctx, dir)
	cancel()
	if err != nil {
		return
	}
	r.touchMarker(run)
	defer r.removeMarker(run)
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			r.touchMarker(run)
		case <-stop:
			return
		}
	}
}

// markerPath is the marker file of a run, or "" for a name that is not one of
// ours.
func (r repo) markerPath(run string) string {
	if run == "" || run == "." || run == ".." || strings.ContainsAny(run, `/\:`) {
		return ""
	}
	return filepath.Join(r.commonDir, liveDirName, run)
}

func (r repo) touchMarker(run string) {
	p := r.markerPath(run)
	if p == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if os.WriteFile(p, nil, 0o644) == nil {
		now := time.Now()
		_ = os.Chtimes(p, now, now)
	}
}

func (r repo) removeMarker(run string) {
	if p := r.markerPath(run); p != "" {
		_ = os.Remove(p)
	}
}

// liveElsewhere says another process touched the marker of run lately.
func (r repo) liveElsewhere(run string, now time.Time) bool {
	p := r.markerPath(run)
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	if err != nil {
		return false
	}
	age := now.Sub(fi.ModTime())
	return age < liveTTL && age > -liveTTL
}

// sweepOnce sweeps a repository the first time this process saves files in it.
func sweepOnce(r repo) {
	if _, done := swept.LoadOrStore(r.root, true); done {
		return
	}
	go r.sweep(time.Now())
}

// Sweep removes the checkpoints of runs that no process holds live and that are
// older than StaleRunAge, which a crash or a kill leaves behind, and trims the
// refs under UndoPrefix to UndoMaxCount and UndoMaxAge. It is called once per
// repository when the first checkpoint is made. A run another Flockdeck process
// is still using is left alone however old its checkpoints are.
func Sweep(dir string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if r, err := locate(ctx, dir); err == nil {
		r.sweep(time.Now())
	}
}

func (r repo) sweep(now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := git(ctx, r.root, nil, nil, "for-each-ref", "--sort=-committerdate", "--format=%(refname) %(committerdate:unix)", "refs/flockdeck/")
	if err != nil {
		return
	}
	undo := 0
	for _, line := range strings.Split(out, "\n") {
		ref, stamp, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		secs, err := strconv.ParseInt(stamp, 10, 64)
		if err != nil {
			continue
		}
		age := now.Sub(time.Unix(secs, 0))
		drop := false
		switch {
		case strings.HasPrefix(ref, UndoPrefix):
			undo++
			drop = undo > UndoMaxCount || age > UndoMaxAge
		case strings.HasPrefix(ref, RefPrefix):
			run, _, _ := strings.Cut(strings.TrimPrefix(ref, RefPrefix), "/")
			drop = age > StaleRunAge && !isLive(run) && !r.liveElsewhere(run, now)
		}
		if drop {
			_, _ = git(ctx, r.root, nil, nil, "update-ref", "-d", ref)
		}
	}
	r.sweepMarkers(now)
}

// sweepMarkers removes the markers of runs that died.
func (r repo) sweepMarkers(now time.Time) {
	entries, err := os.ReadDir(filepath.Join(r.commonDir, liveDirName))
	if err != nil {
		return
	}
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && !isLive(e.Name()) && now.Sub(fi.ModTime()) > liveTTL {
			_ = os.Remove(filepath.Join(r.commonDir, liveDirName, e.Name()))
		}
	}
}

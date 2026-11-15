package filerewind

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Live runs are the ones this process still holds checkpoints for. A run is the
// second part of a checkpoint ref, refs/flockdeck/rewind/<run>/.
var (
	liveMu   sync.Mutex
	liveRuns = map[string]int{}
	swept    sync.Map // repository roots swept by this process
)

// Register says run is live until Unregister.
func Register(run string) {
	liveMu.Lock()
	liveRuns[run]++
	liveMu.Unlock()
}

// Unregister ends a Register.
func Unregister(run string) {
	liveMu.Lock()
	if liveRuns[run] <= 1 {
		delete(liveRuns, run)
	} else {
		liveRuns[run]--
	}
	liveMu.Unlock()
}

func isLive(run string) bool {
	liveMu.Lock()
	defer liveMu.Unlock()
	return liveRuns[run] > 0
}

// sweepOnce sweeps a repository the first time this process saves files in it.
func sweepOnce(r repo) {
	if _, done := swept.LoadOrStore(r.root, true); done {
		return
	}
	go r.sweep(time.Now())
}

// Sweep removes the checkpoints of runs that are not live and older than
// StaleRunAge, which a crash or a kill leaves behind, and trims the refs under
// UndoPrefix to UndoMaxCount and UndoMaxAge. It is called once per repository
// when the first checkpoint is made; the age limit keeps it from touching the
// checkpoints of another running Flockdeck.
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
			drop = !isLive(run) && age > StaleRunAge
		}
		if drop {
			_, _ = git(ctx, r.root, nil, nil, "update-ref", "-d", ref)
		}
	}
}

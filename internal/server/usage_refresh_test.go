package server

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
)

// TestUsageIsReadAgainWhileNothingElseChanges covers a pane's CPU and memory
// figures, which are read only when a snapshot is built. Nothing built one on a
// timer, so an agent that had just stopped kept the share it was last sent with
// -- its fifteen-second average, still high, marked hot in its header -- for as
// long as nothing else in the workspace changed.
func TestUsageIsReadAgainWhileNothingElseChanges(t *testing.T) {
	var cpu atomic.Int64
	cpu.Store(3)
	was := usageOf
	usageOf = func(*session.Session) session.Usage {
		return session.Usage{CPUPercent: float64(cpu.Load()), RSSBytes: 50 << 20, Procs: 2, Known: true}
	}
	t.Cleanup(func() { usageOf = was })
	wasEvery := usageRefresh
	usageRefresh = 200 * time.Millisecond
	t.Cleanup(func() { usageRefresh = wasEvery })

	srv, _ := newTestServer(t)
	r := readControl(dialControl(t, srv))
	r.settle(t)
	// The shell goes idle a second or two after its prompt, and that change is
	// broadcast, which would carry the change under test out by itself.
	r.shellsHaveStarted(t)

	cpu.Store(90)
	if _, ok := r.stateWithin(2*time.Second, func(s stateMsg) bool {
		for _, p := range s.Panes {
			if p.CPU == 90 {
				return true
			}
		}
		return false
	}); !ok {
		t.Fatal("a pane's CPU share was not read again while nothing else changed")
	}
}

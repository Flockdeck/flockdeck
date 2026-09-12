package server

import (
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
)

// TestAStagedUpdateReachesAQuietWindow covers the release that is staged in the
// background, hours into a run. A snapshot goes out when something changes, and
// with the agents waiting and nobody touching the window nothing does -- so the
// window went on without the update's badge until something unrelated happened.
func TestAStagedUpdateReachesAQuietWindow(t *testing.T) {
	// Held still, so that a process-table reading landing in the wait below is
	// not what carries the update out.
	was := usageOf
	usageOf = func(*session.Session) session.Usage {
		return session.Usage{CPUPercent: 3, RSSBytes: 50 << 20, Procs: 2, Known: true}
	}
	t.Cleanup(func() { usageOf = was })

	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	r := readControl(conn)
	r.settle(t)

	srv.SetUpdate(&UpdateView{Version: "9.9.9"})
	if _, ok := r.stateWithin(2*time.Second, func(s stateMsg) bool {
		return s.Update != nil && s.Update.Version == "9.9.9"
	}); !ok {
		t.Fatal("a quiet window was not told about the staged update")
	}
}

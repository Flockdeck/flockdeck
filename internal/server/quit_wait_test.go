package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestQuitWaitsForTheInstanceToExitNotJustToStopListening covers `flockdeck
// -quit && flockdeck`. An instance asked to quit closes its port first, and
// only then saves every project, stops its agents and clears its record --
// so "stopped" said as the port closed was said with all of that still to
// come, and the next launch, finding nothing listening, started a second
// instance beside the first one's still-living agents. The quit is over when
// the process the instance said it was has exited.
func TestQuitWaitsForTheInstanceToExitNotJustToStopListening(t *testing.T) {
	const instancePID = 4242
	var exited atomic.Bool
	wasAlive, wasPoll := instanceAlive, quitPoll
	t.Cleanup(func() { instanceAlive, quitPoll = wasAlive, wasPoll })
	instanceAlive = func(pid int) bool { return pid == instancePID && !exited.Load() }
	quitPoll = 10 * time.Millisecond

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/quit" {
			w.WriteHeader(http.StatusNoContent)
			// The port goes first; the saving and the agents take a while
			// longer, and the process ends after them.
			go func() {
				time.Sleep(50 * time.Millisecond)
				srv.Close()
				time.Sleep(400 * time.Millisecond)
				exited.Store(true)
			}()
		}
	}))
	t.Cleanup(srv.Close)

	if err := RequestQuit(srv.URL, "t", instancePID); err != nil {
		t.Fatalf("RequestQuit = %v", err)
	}
	if !exited.Load() {
		t.Error("RequestQuit returned while the instance's process was still running, saving and stopping its agents")
	}
}

// TestQuitSaysSoWhenTheInstanceNeverExits covers a shutdown that wedges after
// the port has closed. That is not "stopped", and it is not waited on for
// ever either.
func TestQuitSaysSoWhenTheInstanceNeverExits(t *testing.T) {
	wasAlive, wasGrace, wasPoll := instanceAlive, quitGrace, quitPoll
	t.Cleanup(func() { instanceAlive, quitGrace, quitPoll = wasAlive, wasGrace, wasPoll })
	instanceAlive = func(int) bool { return true }
	quitGrace, quitPoll = 300*time.Millisecond, 10*time.Millisecond

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/quit" {
			w.WriteHeader(http.StatusNoContent)
			go func() { time.Sleep(50 * time.Millisecond); srv.Close() }()
		}
	}))
	t.Cleanup(srv.Close)

	err := RequestQuit(srv.URL, "t", 4242)
	if err == nil || !strings.Contains(err.Error(), "still running") {
		t.Errorf("RequestQuit = %v, want it to say the instance is still running", err)
	}
}

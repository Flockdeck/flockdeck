package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// isolateState points the state directory at somewhere of the test's own, so
// the instance record written here cannot be the user's.
func isolateState(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for _, v := range []string{"APPDATA", "XDG_CONFIG_HOME", "HOME"} {
		t.Setenv(v, dir)
	}
}

// An instance whose workspace has wedged answers its health check as too busy
// to report, and that read as nothing running at all, so -quit said there was
// nothing to stop to the one instance somebody most wants stopped. The request
// to quit needs nothing from the workspace, so it has to be sent.
func TestQuitReachesAWedgedInstance(t *testing.T) {
	isolateState(t)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"app":"flockdeck"}`))
		case "/quit":
			w.WriteHeader(http.StatusNoContent)
			go func() { time.Sleep(50 * time.Millisecond); srv.Close() }()
		}
	}))
	t.Cleanup(srv.Close)
	if err := store.SaveInstance(&store.Instance{PID: os.Getpid(), URL: srv.URL, Token: "t"}); err != nil {
		t.Fatal(err)
	}

	if err := quitRunning(); err != nil {
		t.Errorf("quitRunning = %v, want the wedged instance stopped", err)
	}
}

// -quit while an instance is still starting, with nothing on record yet,
// said nothing was running and let it come up. It waits for the start under
// way and stops what it started.
func TestQuitWaitsForAStartUnderWay(t *testing.T) {
	isolateState(t)
	starting, err := store.TryLockStart()
	if err != nil {
		t.Fatal(err)
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			// Asked who it is before it is asked to quit, as a real instance
			// is, and answering as one.
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"app":"flockdeck","ready":true}`))
		case "/quit":
			w.WriteHeader(http.StatusNoContent)
			go func() { time.Sleep(50 * time.Millisecond); srv.Close() }()
		}
	}))
	t.Cleanup(srv.Close)
	go func() {
		// The start finishes: on record, then the lock let go.
		time.Sleep(200 * time.Millisecond)
		_ = store.SaveInstance(&store.Instance{PID: os.Getpid(), URL: srv.URL, Token: "t"})
		starting()
	}()

	if err := quitRunning(); err != nil {
		t.Errorf("quitRunning = %v, want the instance that was starting stopped", err)
	}
}

// -new starts one project from nothing; it has no business dropping the other
// projects the user had open from what the next start brings back.
func TestKeepOpenProjectsAfterANewRun(t *testing.T) {
	isolateState(t)
	a, b := t.TempDir(), t.TempDir()
	before := &store.Session{Open: []string{a, b}, Active: b}
	// What the -new run on a saved on its way out: only itself.
	now := &store.Session{Open: []string{a}, Active: a}
	if err := store.SaveSession(now); err != nil {
		t.Fatal(err)
	}

	if err := keepOpenProjects(before, now, nil); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadSession()
	if err != nil || got == nil {
		t.Fatalf("LoadSession = %v, %v", got, err)
	}
	if len(got.Open) != 2 || got.Open[0] != a || got.Open[1] != b || got.Active != a {
		t.Errorf("session = %+v, want both projects open and this run's active", got)
	}

	if err := keepOpenProjects(nil, now, nil); err != nil {
		t.Errorf("a run without -new: %v", err)
	}
}

// The projects a -new run skipped are kept even when the list it saved on its
// way out cannot be read back. They were added to what was read back, and a
// read that failed, or a list that came back damaged, returned without saving:
// the only copy of them, the one in memory, went with it.
func TestKeepOpenProjectsWhenTheSavedListCannotBeReadBack(t *testing.T) {
	isolateState(t)
	a, b := t.TempDir(), t.TempDir()
	before := &store.Session{Open: []string{a, b}, Active: b}
	now := &store.Session{Open: []string{a}, Active: a}
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte(`{"open": [`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := keepOpenProjects(before, now, nil); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadSession()
	if err != nil || got == nil {
		t.Fatalf("LoadSession = %v, %v; want the projects kept", got, err)
	}
	if len(got.Open) != 2 || got.Open[0] != a || got.Open[1] != b || got.Active != a {
		t.Errorf("session = %+v, want both projects open and this run's active", got)
	}
}

// A project closed during a -new run was put back by the list of projects
// open before it, so closing it did not stick: the next start reopened it.
// Only the ones the run did not close are put back.
func TestKeepOpenProjectsLeavesOutWhatTheRunClosed(t *testing.T) {
	isolateState(t)
	a, b, c := t.TempDir(), t.TempDir(), t.TempDir()
	before := &store.Session{Open: []string{a, b, c}, Active: b}
	// The -new run on a opened b, and then closed it.
	now := &store.Session{Open: []string{a}, Active: a}

	if err := keepOpenProjects(before, now, []string{b}); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadSession()
	if err != nil || got == nil {
		t.Fatalf("LoadSession = %v, %v", got, err)
	}
	if len(got.Open) != 2 || got.Open[0] != a || got.Open[1] != c {
		t.Errorf("session = %+v, want %s and %s open, and not %s, which the run closed", got, a, c, b)
	}
}

// A -new run does not look for the projects it puts back, so one whose folder
// was away before it is still away, and keeps its count of starts away: the
// run is not a start that found it there.
func TestKeepOpenProjectsKeepsWhatIsAway(t *testing.T) {
	isolateState(t)
	a := t.TempDir()
	away := filepath.Join(t.TempDir(), "usb")
	before := &store.Session{Open: []string{a, away}, Active: a, Away: map[string]int{away: 3}}
	now := &store.Session{Open: []string{a}, Active: a}

	if err := keepOpenProjects(before, now, nil); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadSession()
	if err != nil || got == nil {
		t.Fatalf("LoadSession = %v, %v", got, err)
	}
	if len(got.Open) != 2 || got.Open[1] != away || got.Away[away] != 3 {
		t.Errorf("session = %+v, want %s kept, away for the 3 starts it had been", got, away)
	}
}

// A record left behind by an instance that has gone still means nothing is
// running, and the record is cleared on the way.
func TestQuitWithOnlyAStaleRecord(t *testing.T) {
	isolateState(t)
	if err := store.SaveInstance(&store.Instance{PID: os.Getpid(), URL: "http://127.0.0.1:1", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := quitRunning(); !errors.Is(err, errNoneRunning) {
		t.Errorf("quitRunning = %v, want %v", err, errNoneRunning)
	}
	if inst, _ := store.LoadInstance(); inst != nil {
		t.Errorf("the stale record is still there: %+v", inst)
	}
}

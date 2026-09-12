package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
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

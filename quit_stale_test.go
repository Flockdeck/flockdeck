package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// TestQuitLeavesAPortSomethingElseHasTakenAlone covers a record left by an
// instance that has gone, whose port something else has taken since. -quit
// sent it the old token with a request to quit: a service that answers a POST
// with a 2xx was reported "stopped", and another user's flockdeck, refusing
// the token, as an error -- with the record never cleared, and so every -quit
// after it the same. What answers is asked who it is first, and neither is
// asked to quit.
func TestQuitLeavesAPortSomethingElseHasTakenAlone(t *testing.T) {
	for _, tc := range []struct {
		name   string
		health func(http.ResponseWriter)
	}{
		{"a service of another kind", func(w http.ResponseWriter) { w.WriteHeader(http.StatusNotFound) }},
		{"another user's flockdeck", func(w http.ResponseWriter) { http.Error(w, "forbidden", http.StatusForbidden) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateState(t)
			var quits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/health":
					tc.health(w)
				case "/quit":
					quits.Add(1)
					w.WriteHeader(http.StatusNoContent)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(srv.Close)
			if err := store.SaveInstance(&store.Instance{PID: os.Getpid(), URL: srv.URL, Token: "t"}); err != nil {
				t.Fatal(err)
			}

			if err := quitRunning(); !errors.Is(err, errNoneRunning) {
				t.Errorf("quitRunning = %v, want %v", err, errNoneRunning)
			}
			if n := quits.Load(); n != 0 {
				t.Errorf("what answers at the recorded address was asked to quit %d times", n)
			}
			if inst, _ := store.LoadInstance(); inst != nil {
				t.Errorf("the stale record is still there: %+v", inst)
			}
		})
	}
}

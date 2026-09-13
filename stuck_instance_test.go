package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// An instance holding its port and answering nothing -- wedged, or with its
// machine too busy to reply in time -- had its record cleared as a stale one,
// and the launch started a second instance beside its agents without a word.
// Only a refused connection or something else answering says the record is
// stale. A launch stops at one that is running and says so.
func TestAnInstanceThatDoesNotAnswerIsNotTakenForAStaleOne(t *testing.T) {
	isolateState(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	if err := store.SaveInstance(&store.Instance{PID: 4242, URL: srv.URL, Token: secretToken, Started: time.Now()}); err != nil {
		t.Fatal(err)
	}
	was := instanceGoing
	t.Cleanup(func() { instanceGoing = was })
	instanceGoing = func(*store.Instance) bool { return true }

	var warned []string
	inst, _, err := joinRunning(runningInstance, func(text string) { warned = append(warned, text) })
	if inst != nil || !errors.Is(err, errNotAnswering) {
		t.Fatalf("joinRunning = %v, %v; want the launch stopped at an instance that is running and not answering", inst, err)
	}
	if len(warned) > 0 {
		t.Errorf("warned %q and would have started a second instance", warned)
	}
	for _, want := range []string{"process 4242", "did not answer within"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the launch says %q, want %q in it", err, want)
		}
	}
	if strings.Contains(err.Error(), secretToken) {
		t.Errorf("the launch's error shows the token: %v", err)
	}
	if rec, _ := store.LoadInstance(); rec == nil {
		t.Error("the record of an instance that is still running was cleared")
	}
}

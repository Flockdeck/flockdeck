package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// secretToken is the token the stand-in instances here are recorded with, and
// what no error may show.
const secretToken = "s3cret-token-never-shown"

// hangUp ends the request without a reply, which is an error from the client
// naming the address it was sent to, token and all.
func hangUp(w http.ResponseWriter) {
	conn, _, err := w.(http.Hijacker).Hijack()
	if err == nil {
		conn.Close()
	}
}

// Go's error for a failed request names the address it went to, and a request
// to an instance carries its token there. -quit and a launch joining an
// instance put that error in front of the user, and fail() writes it to
// error.log and, on Windows, an error box: the token went with it.
func TestAFailedRequestToTheInstanceDoesNotShowItsToken(t *testing.T) {
	isolateState(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"app":"flockdeck","ready":true}`))
		default:
			hangUp(w)
		}
	}))
	t.Cleanup(srv.Close)
	inst := &store.Instance{PID: os.Getpid(), URL: srv.URL, Token: secretToken, Started: time.Now()}
	if err := store.SaveInstance(inst); err != nil {
		t.Fatal(err)
	}

	err := quitRunning()
	if err == nil {
		t.Fatal("quitRunning succeeded against an instance that hung up on the request")
	}
	if strings.Contains(err.Error(), secretToken) {
		t.Errorf("-quit's error shows the token: %v", err)
	}

	err = attach(inst, srv.URL, t.TempDir(), true)
	if err == nil {
		t.Fatal("attach succeeded against an instance that hung up on the request")
	}
	if strings.Contains(err.Error(), secretToken) {
		t.Errorf("a launch's error shows the token: %v", err)
	}
	if !strings.Contains(err.Error(), "-solo") {
		t.Errorf("a launch's error is %q, want it to offer -solo for an instance that answers but refuses", err)
	}
}

// An instance holding its port and answering nothing was reported as a URL, the
// token in it, and "context deadline exceeded". It says which instance, how
// long it was given, and what to do -- and not to start another beside it.
func TestAnInstanceThatDoesNotAnswerIsNamedWithWhatToDo(t *testing.T) {
	isolateState(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	if err := store.SaveInstance(&store.Instance{PID: 4242, URL: srv.URL, Token: secretToken, Started: time.Now()}); err != nil {
		t.Fatal(err)
	}
	host := strings.TrimPrefix(srv.URL, "http://")

	err := quitRunning()
	if err == nil {
		t.Fatal("quitRunning succeeded against an instance that never answered")
	}
	for _, want := range []string{host, "process 4242", "did not answer within 2 s", "end process 4242"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("-quit's error is %q, want %q in it", err, want)
		}
	}
	if strings.Contains(err.Error(), secretToken) {
		t.Errorf("-quit's error shows the token: %v", err)
	}

	// The same for a launch whose request to open a project timed out, which
	// offered -solo: a second set of agents is no answer to a stuck first.
	timeout := &url.Error{Op: "Post", URL: srv.URL + "/open?t=" + secretToken, Err: context.DeadlineExceeded}
	got := instanceRequestFailed(&store.Instance{PID: 4242, URL: srv.URL, Token: secretToken}, "open", timeout, 15*time.Second)
	if strings.Contains(got.Error(), secretToken) || strings.Contains(got.Error(), "-solo") || !strings.Contains(got.Error(), "within 15 s") {
		t.Errorf("a timed-out request reads %q, want how long it was given, without the token or -solo", got)
	}
}

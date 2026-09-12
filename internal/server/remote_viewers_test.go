package server

import (
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
)

// TestTheDeskSeesAPhoneComeAndGo covers the Remote chip's count of windows open
// through the relay, which is there to say somebody may be typing. A phone
// arriving or leaving changes nothing else a snapshot carries, so with the
// agents quiet the desk went on showing no viewer for one who had come, and one
// for a viewer who had gone.
func TestTheDeskSeesAPhoneComeAndGo(t *testing.T) {
	was := usageOf
	usageOf = func(*session.Session) session.Usage {
		return session.Usage{CPUPercent: 3, RSSBytes: 50 << 20, Procs: 2, Known: true}
	}
	t.Cleanup(func() { usageOf = was })

	srv, _ := newTestServer(t)
	srv.SetRemote(&fakeRemote{ok: true})
	desk := readControl(dialControl(t, srv))
	desk.settle(t)
	// The shell going idle is broadcast a second or two after the connection
	// first falls quiet, and would carry the count out by itself.
	for deadline := time.Now().Add(20 * time.Second); ; {
		if _, ok := desk.stateWithin(3*time.Second, nil); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the control connection never went quiet")
		}
	}
	viewers := func(n int) func(stateMsg) bool {
		return func(s stateMsg) bool { return s.Remote != nil && s.Remote.Viewers == n }
	}

	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatalf("dial through the tunnel: %v", err)
	}
	if _, ok := desk.stateWithin(2*time.Second, viewers(1)); !ok {
		t.Fatal("the desk was not told a window had opened through the relay")
	}
	phone.CloseNow()
	if _, ok := desk.stateWithin(2*time.Second, viewers(0)); !ok {
		t.Fatal("the desk was not told the window through the relay had gone")
	}
}

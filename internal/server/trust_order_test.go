package server

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestTrustPassesTakeTurns covers two fan-outs carrying folder trust over at
// the same moment, from two windows. Each pass reads Claude Code's
// configuration, adds its worktrees and writes the whole file back through
// one fixed temporary name, so side by side one lost the other's entries --
// or renamed the other's half-written copy into place. A pass now waits for
// any other to finish.
func TestTrustPassesTakeTurns(t *testing.T) {
	var ran atomic.Bool
	jobs := []*fanoutJob{{task: "one", cwd: "/wt/one"}}
	inherit := func(string, string) error { ran.Store(true); return nil }

	// Another pass, part way through.
	trustWrites.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		inheritTrust(jobs, "/repo", inherit, func(string) {})
	}()
	time.Sleep(300 * time.Millisecond)
	early := ran.Load()
	trustWrites.Unlock()
	if early {
		t.Fatal("a trust pass wrote while another was part way through")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the trust pass never finished")
	}
	if !ran.Load() {
		t.Fatal("the trust pass never carried anything over")
	}
}

package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/e2e"
)

// The push preferences are the desk's: no prompt guards them, so a phone with
// Control agents could mute the desk's own notifications.
func TestSealedPushPreferencesAreNotOnTheSocket(t *testing.T) {
	for _, n := range []string{"pushNotify", "pushAnonymous", "pushDelay"} {
		if tier, ok := sealedCommands[n]; ok {
			t.Errorf("%s is on the sealed socket at tier %q", n, tier)
		}
	}
	if sealedCommands["pushPrefsGet"] != sealedTierA {
		t.Error("pushPrefsGet should stay a read")
	}
}

// A device is let attach a bounded number of bytes in a window, and a pane's
// folder is kept to a bounded size, the oldest going first.
func TestAttachImageHasQuotasAndCleanup(t *testing.T) {
	d := &sealedDevice{}
	now := time.Now()
	if !d.reserveUpload(attachDeviceBytes-10, now) {
		t.Fatal("a picture inside the quota was refused")
	}
	if d.reserveUpload(11, now) {
		t.Error("the quota was exceeded")
	}
	if !d.reserveUpload(11, now.Add(attachDeviceWindow+time.Second)) {
		t.Error("the quota did not reset after its window")
	}

	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 30; i++ {
		p := filepath.Join(dir, string(rune('a'+i%26))+string(rune('a'+i/26))+".jpg")
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		at := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	trimPaneUploads(dir, attachPaneFiles-1, attachPaneBytes)
	left, _ := os.ReadDir(dir)
	if len(left) != attachPaneFiles-1 {
		t.Fatalf("%d files left, want %d", len(left), attachPaneFiles-1)
	}
	if _, err := os.Stat(filepath.Join(dir, "aa.jpg")); err == nil {
		t.Error("the oldest picture was kept")
	}
	trimPaneUploads(dir, 100, 5)
	left, _ = os.ReadDir(dir)
	if len(left) != 5 {
		t.Errorf("%d files left under a 5 byte cap, want 5", len(left))
	}
}

// Asking for a device's allowance allocates, so the sealed socket does it only
// after the device passed the roster and access checks, and a full table drops
// one entry and not all.
func TestSealedDeviceTableAllocatesAfterAuthAndEvictsOne(t *testing.T) {
	e := newSealedEnv(t)
	has := func(id string) bool {
		e.srv.sealed.mu.Lock()
		defer e.srv.sealed.mu.Unlock()
		return e.srv.sealed.devices[id] != nil
	}
	// A device the roster does not know, and one with no key, allocate nothing.
	for _, id := range []string{"stranger", "nokey"} {
		conn := e.dial(t, e.header(id))
		closeOf(t, conn)
		if has(id) {
			t.Errorf("%s got an allowance before it was let in", id)
		}
	}

	st := &e.srv.sealed
	for i := 0; i < sealedDeviceMax; i++ {
		st.device("d" + string(rune('A'+i%26)) + string(rune('a'+i/26%26)) + string(rune('0'+i/676)))
	}
	keep := st.device("dAa0")
	st.device("one-more")
	st.mu.Lock()
	size := len(st.devices)
	st.mu.Unlock()
	if size != sealedDeviceMax {
		t.Errorf("the table holds %d after a full table was added to, want %d", size, sealedDeviceMax)
	}
	if !has("one-more") {
		t.Error("the new device was not kept")
	}
	_ = keep
}

// The desk's refusals lengthen the wait before the device may ask again, and a
// yes clears it.
func TestSealedDenyPenaltyGrows(t *testing.T) {
	d := &sealedDevice{}
	now := time.Now()
	if !d.mayAskAt(now) {
		t.Fatal("a device never refused was held back")
	}
	d.noteDenied(now)
	if d.mayAskAt(now.Add(sealedDenyBase - time.Second)) {
		t.Error("asked again inside the first wait")
	}
	if !d.mayAskAt(now.Add(sealedDenyBase)) {
		t.Error("the first wait did not end")
	}
	d.noteDenied(now)
	if d.mayAskAt(now.Add(sealedDenyBase + time.Second)) {
		t.Error("the second refusal did not double the wait")
	}
	for i := 0; i < 20; i++ {
		d.noteDenied(now)
	}
	if d.mayAskAt(now.Add(sealedDenyMax - time.Second)) {
		t.Error("many refusals did not reach the maximum wait")
	}
	if !d.mayAskAt(now.Add(sealedDenyMax)) {
		t.Error("the wait went past its maximum")
	}
	d.noteAllowed()
	if !d.mayAskAt(now) {
		t.Error("a yes did not clear the wait")
	}
	// Refusals older than the memory are forgotten.
	d.noteDenied(now)
	d.noteDenied(now.Add(2 * sealedDenyMemory))
	if d.mayAskAt(now.Add(2*sealedDenyMemory + sealedDenyBase - time.Second)) {
		t.Error("the new refusal was not held to the base wait")
	}
	if !d.mayAskAt(now.Add(2*sealedDenyMemory + sealedDenyBase)) {
		t.Error("old refusals still counted")
	}
}

// A live socket is looked at again with no request: a device taken off the
// roster is cut, and so is one that outlives the maximum age.
func TestSealedLiveSessionIsRechecked(t *testing.T) {
	t.Run("off the roster", func(t *testing.T) {
		e := newSealedEnv(t)
		e.srv.sealed.timing = sealedTimings{recheck: 50 * time.Millisecond}
		p, _ := e.open(t, "full")
		e.fake.mu.Lock()
		e.fake.capable["full"] = false
		e.fake.mu.Unlock()
		code, reason := closeOf(t, p.conn)
		if code != artifactCloseRefused || reason != sealedReasonRoster {
			t.Errorf("closed with %d %q, want %d %q", code, reason, artifactCloseRefused, sealedReasonRoster)
		}
	})
	// Paired again under the same id: the roster still lists a key, but not the one
	// this socket handshook with.
	t.Run("paired again with another key", func(t *testing.T) {
		e := newSealedEnv(t)
		e.srv.sealed.timing = sealedTimings{recheck: 50 * time.Millisecond}
		p, _ := e.open(t, "full")
		next, err := e2e.GenerateStaticKey()
		if err != nil {
			t.Fatal(err)
		}
		e.fake.kmu.Lock()
		e.fake.keys["full"] = next.PublicKey().Bytes()
		e.fake.kmu.Unlock()
		code, reason := closeOf(t, p.conn)
		if code != artifactCloseRefused || reason != sealedReasonRekeyed {
			t.Errorf("closed with %d %q, want %d %q", code, reason, artifactCloseRefused, sealedReasonRekeyed)
		}
	})
	t.Run("too old", func(t *testing.T) {
		e := newSealedEnv(t)
		e.srv.sealed.timing = sealedTimings{recheck: 50 * time.Millisecond, maxAge: 120 * time.Millisecond}
		p, _ := e.open(t, "full")
		code, reason := closeOf(t, p.conn)
		// Not the desk switching it off and not a refusal: the phone reconnects at once.
		if code != sealedCloseRenew || reason != sealedReasonRenew {
			t.Errorf("closed with %d %q, want %d %q", code, reason, sealedCloseRenew, sealedReasonRenew)
		}
	})
}

// A listing of the known worktrees in progress does not hold the cache's lock,
// and callers that arrive meanwhile wait for it and share its answer.
func TestKnownWorktreesWaitsOutsideTheLock(t *testing.T) {
	srv, _ := newTestServer(t)
	c := &srv.wtCache
	flight := make(chan struct{})
	c.mu.Lock()
	c.flight = flight
	c.mu.Unlock()

	got := make(chan []string, 1)
	go func() { got <- srv.knownWorktrees() }()
	time.Sleep(100 * time.Millisecond)
	if !c.mu.TryLock() {
		t.Fatal("the cache lock is held while a listing runs")
	}
	c.at, c.list, c.flight = time.Now(), []string{"shared"}, nil
	c.mu.Unlock()
	close(flight)
	select {
	case l := <-got:
		if len(l) != 1 || l[0] != "shared" {
			t.Errorf("the waiter got %v", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiter never returned")
	}
}

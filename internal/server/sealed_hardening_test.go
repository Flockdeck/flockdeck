package server

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	trimPaneUploads(dir, "", attachPaneFiles-1, attachPaneBytes)
	left, _ := os.ReadDir(dir)
	if len(left) != attachPaneFiles-1 {
		t.Fatalf("%d files left, want %d", len(left), attachPaneFiles-1)
	}
	if _, err := os.Stat(filepath.Join(dir, "aa.jpg")); err == nil {
		t.Error("the oldest picture was kept")
	}
	trimPaneUploads(dir, "", 100, 5)
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

// A listing that panics must not leave the flight set: the next caller would wait
// on it for good.
func TestKnownWorktreesDoesNotWedgeAfterAPanic(t *testing.T) {
	srv, _ := newTestServer(t)
	c := &srv.wtCache
	c.lister = func() []string { panic("git listing blew up") }
	func() {
		defer func() { _ = recover() }()
		srv.knownWorktrees()
		t.Error("the panic was swallowed")
	}()
	c.mu.Lock()
	wedged := c.flight != nil
	cached := !c.at.IsZero()
	c.mu.Unlock()
	if wedged {
		t.Fatal("the flight is still set after the panic")
	}
	if cached {
		t.Error("a listing that did not finish was kept")
	}
	c.lister = func() []string { return []string{"fine"} }
	got := make(chan []string, 1)
	go func() { got <- srv.knownWorktrees() }()
	select {
	case l := <-got:
		if len(l) != 1 || l[0] != "fine" {
			t.Errorf("got %v", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the next caller waits for ever")
	}
}

// A device's cap on a pane's folder is its own pictures: its uploads do not push
// out the desk's own, or another device's.
func TestPaneUploadCapIsPerOwner(t *testing.T) {
	dir := t.TempDir()
	devA, devB := uploadOwner("phone-a"), uploadOwner("phone-b")
	if devA == devB || devA == "" {
		t.Fatalf("owner tags %q %q", devA, devB)
	}
	base := time.Now().Add(-time.Hour)
	put := func(i int, owner, label string) string {
		name := fmt.Sprintf("%d-%s%s%d.jpg", 1000+i, owner, label, i)
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		at := base.Add(time.Duration(i) * time.Minute)
		_ = os.Chtimes(p, at, at)
		return p
	}
	// The desk's pictures are the oldest of all.
	var desk []string
	for i := 0; i < 3; i++ {
		desk = append(desk, put(i, "", "desk"))
	}
	other := put(3, devB, "other")
	for i := 4; i < 4+attachPaneFiles+5; i++ {
		put(i, devA, "mine")
	}
	trimPaneUploads(dir, devA, attachPaneFiles-1, attachPaneBytes)
	for _, p := range append(desk, other) {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("a picture that is not this device's was removed: %s", filepath.Base(p))
		}
	}
	left, _ := os.ReadDir(dir)
	mine := 0
	for _, f := range left {
		if ownedUpload(f.Name(), devA) {
			mine++
		}
	}
	if mine != attachPaneFiles-1 {
		t.Errorf("the device holds %d pictures, want %d", mine, attachPaneFiles-1)
	}
	// And the desk trimming its own leaves the devices'.
	trimPaneUploads(dir, "", 1, attachPaneBytes)
	after, _ := os.ReadDir(dir)
	if len(after) != 1+1+attachPaneFiles-1 {
		t.Errorf("%d files after the desk trimmed its own, want one desk picture, one of the other device's and the device's %d", len(after), attachPaneFiles-1)
	}
	if !uploadFileName.MatchString(fmt.Sprintf("%d-%s%s.jpg", 5, devA, strings.Repeat("n", 60-len(devA)))) {
		t.Error("a tagged name is not one the upload reader accepts")
	}
}

// A picture saved for a device carries its tag and the file is one a message can
// point at; the desk's own carries none.
func TestSavedPictureCarriesItsOwnersTag(t *testing.T) {
	srv, _ := newTestServer(t)
	_ = srv
	png := base64.StdEncoding.EncodeToString(testPNG(t, 2, 2))
	mine, err := saveAttachedImageFor(uploadOwner("phone-a"), "pane-x", "shot", "image/png", png)
	if err != nil {
		t.Fatal(err)
	}
	desk, err := saveAttachedImage("pane-x", "shot", "image/png", png)
	if err != nil {
		t.Fatal(err)
	}
	if !ownedUpload(filepath.Base(mine), uploadOwner("phone-a")) || ownedUpload(filepath.Base(mine), "") {
		t.Errorf("the device's picture is %s", filepath.Base(mine))
	}
	if !ownedUpload(filepath.Base(desk), "") {
		t.Errorf("the desk's picture is %s", filepath.Base(desk))
	}
	for _, p := range []string{mine, desk} {
		if !uploadFileName.MatchString(filepath.Base(p)) {
			t.Errorf("%s is not a name the upload reader accepts", filepath.Base(p))
		}
	}
}

// A request for a pane that is not there costs the device none of its allowance.
func TestUploadAllowanceIsChargedAfterThePaneIsKnown(t *testing.T) {
	e := newSealedEnv(t)
	e.grantControl(t, "full")
	p, _ := e.open(t, "full")
	data := strings.Repeat("A", 4096)
	for i := 0; i < 5; i++ {
		p.send(command{Cmd: "attachImage", ID: "no-such-pane", Name: "a.jpg", Data: data, Req: "img"})
		if res := p.readType("attachImageResult"); res["error"] != paneGone {
			t.Fatalf("attachImage = %v, want %q", res, paneGone)
		}
	}
	d := e.srv.sealed.device("full")
	d.dmu.Lock()
	charged := d.upBytes
	d.dmu.Unlock()
	if charged != 0 {
		t.Errorf("a pane that is not there cost %d bytes of the device's allowance", charged)
	}
}

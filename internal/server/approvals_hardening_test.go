package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/e2e"
	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/store"
)

// ---------------------------------------------------------------------------
// The one-time question: the nonce, the wording version, the window.
// ---------------------------------------------------------------------------

func TestTurningOnNeedsTheHostsChallengeForThisWindowAndDevice(t *testing.T) {
	e := newApproveEnv(t)
	deskA := dialControlPage(t, e.srv)
	deskB := dialControlPage(t, e.srv)

	on := func(c interface{}, dev, nonce string, v int) command {
		return command{Cmd: "setDeviceApprove", ID: dev, On: true, Nonce: nonce, V: v}
	}
	want := func(t *testing.T, note noticeMsg, text string) {
		t.Helper()
		if !note.Error || note.Text != text {
			t.Errorf("answered %+v, want the error %q", note, text)
		}
	}

	// No nonce at all: what a command sent blind looks like.
	want(t, noticeAfter(t, deskA, on(nil, "d1", "", approveAckVersion)), approveNeedsChallenge)
	// A guessed nonce.
	want(t, noticeAfter(t, deskA, on(nil, "d1", "0123456789abcdef", approveAckVersion)), approveNeedsChallenge)

	// A nonce given to another window.
	chB := askChallenge(t, deskB, "d1")
	want(t, noticeAfter(t, deskA, on(nil, "d1", chB.Nonce, chB.V)), approveNeedsChallenge)
	// ...and it was spent by the attempt, even for its own window.
	want(t, noticeAfter(t, deskB, on(nil, "d1", chB.Nonce, chB.V)), approveNeedsChallenge)

	// A nonce about another device.
	ch := askChallenge(t, deskA, "d2")
	want(t, noticeAfter(t, deskA, on(nil, "d1", ch.Nonce, ch.V)), approveNeedsChallenge)

	// A nonce that expired.
	ch = askChallenge(t, deskA, "d1")
	e.srv.approveAsk.mu.Lock()
	c := e.srv.approveAsk.by[ch.Nonce]
	c.expires = time.Now().Add(-time.Second)
	e.srv.approveAsk.by[ch.Nonce] = c
	e.srv.approveAsk.mu.Unlock()
	want(t, noticeAfter(t, deskA, on(nil, "d1", ch.Nonce, ch.V)), approveNeedsChallenge)

	// The wording the person saw is not the wording in force.
	ch = askChallenge(t, deskA, "d1")
	want(t, noticeAfter(t, deskA, on(nil, "d1", ch.Nonce, approveAckVersion-1)), approveWordingChanged)
	want(t, noticeAfter(t, deskA, on(nil, "d1", ch.Nonce, 0)), approveWordingChanged)

	stillOff(t, e.srv, "d1", "after every refused try")

	// A nonce is used once: the real answer works, a replay of it does not.
	ch = askChallenge(t, deskA, "d1")
	if ch.Nonce == "" || ch.V != approveAckVersion || ch.Text != approveAckText || ch.ID != "d1" {
		t.Fatalf("challenge = %+v", ch)
	}
	if note := noticeAfter(t, deskA, on(nil, "d1", ch.Nonce, ch.V)); note.Error {
		t.Fatalf("refused: %+v", note)
	}
	waitUntil(t, "approval to be on", func() bool { return e.srv.approveSwitchOn("d1") })
	desk := dialControlPage(t, e.srv)
	sendCmd(t, desk, command{Cmd: "setDeviceApprove", ID: "d1", On: false})
	waitUntil(t, "approval to be off", func() bool { return !e.srv.approveSwitchOn("d1") })
	want(t, noticeAfter(t, deskA, on(nil, "d1", ch.Nonce, ch.V)), approveNeedsChallenge)
	stillOff(t, e.srv, "d1", "after a replay")
}

func TestAChallengeIsGivenOnlyToAPageAtTheDesk(t *testing.T) {
	e := newApproveEnv(t)
	script := dialControl(t, e.srv)
	note := noticeAfter(t, script, command{Cmd: "approveChallenge", ID: "d1"})
	if !note.Error || note.Text != pageOnlyApprove {
		t.Errorf("a script was answered %+v", note)
	}
	phone, err := dialRemoteControlDevice(e.ts, e.ts.URL, "d2")
	if err != nil {
		t.Fatal(err)
	}
	defer phone.CloseNow()
	note = noticeAfter(t, phone, command{Cmd: "approveChallenge", ID: "d2"})
	if !note.Error || note.Text != deskOnlyApprove {
		t.Errorf("a relay window was answered %+v", note)
	}
	if len(e.srv.approveAsk.by) != 0 {
		t.Errorf("%d challenges were given", len(e.srv.approveAsk.by))
	}
	desk := dialControlPage(t, e.srv)
	if note := noticeAfter(t, desk, command{Cmd: "approveChallenge", ID: "../x"}); !note.Error {
		t.Errorf("a bad id was answered %+v", note)
	}
}

func TestChallengesAreBoundedAndForgottenWithTheirWindow(t *testing.T) {
	e := newApproveEnv(t)
	var wins []*controlClient
	for i := 0; i < approveChallengeMax+4; i++ {
		c := &controlClient{page: true}
		wins = append(wins, c)
		e.srv.approveAsk.give(c, "d1")
	}
	if n := len(e.srv.approveAsk.by); n != approveChallengeMax {
		t.Errorf("%d challenges held, want at most %d", n, approveChallengeMax)
	}
	// One window has one: asking again replaces it.
	e.srv.approveAsk.dropWindow(wins[0])
	n1, _ := e.srv.approveAsk.give(wins[1], "d1")
	n2, _ := e.srv.approveAsk.give(wins[1], "d1")
	if n1 != "" && e.srv.approveAsk.take(n1, wins[1], "d1", approveAckVersion) {
		t.Error("an older challenge of the same window still works")
	}
	if !e.srv.approveAsk.take(n2, wins[1], "d1", approveAckVersion) {
		t.Error("the newest challenge does not work")
	}
}

// ---------------------------------------------------------------------------
// A disable always wins over an enable still being checked.
// ---------------------------------------------------------------------------

// slowEnable starts an enable that stops at the relay's key lookup, and returns
// what the test needs to finish it.
func slowEnable(t *testing.T, e *approveEnv) (release func(), notes <-chan noticeMsg) {
	t.Helper()
	gate, reached := make(chan struct{}), make(chan struct{}, 1)
	e.fake.kmu.Lock()
	e.fake.gate, e.fake.reached = gate, reached
	e.fake.kmu.Unlock()
	desk := dialControlPage(t, e.srv)
	ch := askChallenge(t, desk, "d1")
	sendCmd(t, desk, command{Cmd: "setDeviceApprove", ID: "d1", On: true, Nonce: ch.Nonce, V: ch.V})
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("the enable never reached the relay")
	}
	out := make(chan noticeMsg, 1)
	go func() {
		var n noticeMsg
		defer func() { out <- n }()
		defer func() { _ = recover() }()
		readUntil(t, desk, "notice", &n)
	}()
	var once bool
	return func() {
		if !once {
			once = true
			close(gate)
		}
	}, out
}

func TestAnOffThatLandsWhileAnEnableIsInFlightWins(t *testing.T) {
	e := newApproveEnv(t)
	release, notes := slowEnable(t, e)
	defer release()
	other := dialControlPage(t, e.srv)
	sendCmd(t, other, command{Cmd: "setDeviceApprove", ID: "d1", On: false})
	time.Sleep(300 * time.Millisecond)
	release()
	select {
	case n := <-notes:
		if !n.Error || !strings.Contains(n.Text, "switched off") {
			t.Errorf("the enable was answered %+v, want it refused as superseded", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the enable was never answered")
	}
	stillOff(t, e.srv, "d1", "off landed during the enable")
}

func TestTheKillSwitchThatLandsWhileAnEnableIsInFlightWins(t *testing.T) {
	e := newApproveEnv(t)
	release, notes := slowEnable(t, e)
	defer release()
	other := dialControlPage(t, e.srv)
	sendCmd(t, other, command{Cmd: "setApprovalsOff"})
	time.Sleep(300 * time.Millisecond)
	release()
	if n := <-notes; !n.Error || !strings.Contains(n.Text, "switched off") {
		t.Errorf("the enable was answered %+v, want it refused as superseded", n)
	}
	stillOff(t, e.srv, "d1", "kill switch landed during the enable")
}

func TestAnUnpairThatLandsWhileAnEnableIsInFlightWins(t *testing.T) {
	e := newApproveEnv(t)
	release, notes := slowEnable(t, e)
	defer release()
	e.srv.forgetRole("d1")
	time.Sleep(300 * time.Millisecond)
	release()
	if n := <-notes; !n.Error || !strings.Contains(n.Text, "switched off") {
		t.Errorf("the enable was answered %+v, want it refused as superseded", n)
	}
	stillOff(t, e.srv, "d1", "unpair landed during the enable")
}

// ---------------------------------------------------------------------------
// The grant belongs to a key.
// ---------------------------------------------------------------------------

func TestTheGrantIsTiedToTheKeyItWasMadeFor(t *testing.T) {
	e := newApproveEnv(t)
	e.allow(t, "d1")
	key := e.fake.keys["d1"]
	if a := e.srv.deviceAccess("d1"); a.ApproveKey != store.KeyHash(key) || a.ApproveKey == "" {
		t.Fatalf("record = %+v, want the hash of the key", a)
	}
	if !e.srv.mayApproveWithKey("d1", key) {
		t.Fatal("the device may not approve with its own key")
	}
	for name, k := range map[string][]byte{"another key": {9, 9, 9}, "no key": nil, "empty key": {}} {
		if e.srv.mayApproveWithKey("d1", k) {
			t.Errorf("a device may approve with %s", name)
		}
	}
	if e.srv.mayApproveWithKey("d2", key) {
		t.Error("another device may approve with this device's key")
	}
	// The switch alone is not the gate.
	if !e.srv.approveSwitchOn("d1") {
		t.Error("the switch is not on")
	}
}

func TestARecordWithAStaleOrNoKeyHashIsNotAllowed(t *testing.T) {
	e := newApproveEnv(t)
	now := time.Now().UTC()
	key := e.fake.keys["d1"]
	// A restored preferences file: a grant for a key the device no longer has.
	putAccess(t, e.srv, "d1", store.DeviceAccess{Role: store.RoleFull, Approve: true, ApproveV: approveAckVersion, ApproveAt: now, ApproveKey: store.KeyHash([]byte("an older key"))})
	if e.srv.mayApproveWithKey("d1", key) {
		t.Error("a grant with a stale key hash allows the device")
	}
	// A record made before the grant was tied to a key.
	putAccess(t, e.srv, "d1", store.DeviceAccess{Role: store.RoleFull, Approve: true, ApproveV: approveAckVersion, ApproveAt: now})
	if e.srv.mayApproveWithKey("d1", key) {
		t.Error("a grant with no key hash allows the device")
	}
	putAccess(t, e.srv, "d1", store.DeviceAccess{Role: store.RoleFull, Approve: true, ApproveV: approveAckVersion, ApproveAt: now, ApproveKey: store.KeyHash(key)})
	if !e.srv.mayApproveWithKey("d1", key) {
		t.Error("a grant for this key does not allow the device")
	}
}

// pairedAgain is a server whose relay lists d1 with key A, with approval turned
// on for A, and the same relay with key B once swap is called.
type pairedAgain struct {
	srv   *Server
	fake  *approveFake
	swap  func(state remote.VerifyState)
	keyA  []byte
	hashA string
}

func newPairedAgain(t *testing.T) *pairedAgain {
	t.Helper()
	a, _ := e2e.GenerateStaticKey()
	b, _ := e2e.GenerateStaticKey()
	mk := func(k interface{ Bytes() []byte }, pub string) *approveFake {
		return &approveFake{
			verifyRemote: newVerifyRemote(t, fmt.Sprintf(`[{"id":"d1","name":"phone","publicKey":%q}]`, pub), ""),
			keys:         map[string][]byte{"d1": k.Bytes()},
		}
	}
	fakeA := mk(a.PublicKey(), e2e.EncodePublicKey(a.PublicKey()))
	fakeB := mk(b.PublicKey(), e2e.EncodePublicKey(b.PublicKey()))
	fakeA.setVerified("d1", remote.KeyOriginUsual, remote.Verified)
	srv, _ := newTestServer(t)
	srv.SetRemote(fakeA)
	listDevice(srv, "d1")
	p := &pairedAgain{srv: srv, fake: fakeA, keyA: a.PublicKey().Bytes(), hashA: store.KeyHash(a.PublicKey().Bytes())}
	e := &approveEnv{srv: srv, fake: fakeA}
	e.allow(t, "d1")
	p.swap = func(state remote.VerifyState) {
		fakeB.setVerified("d1", remote.KeyOriginUsual, state)
		srv.SetRemote(fakeB)
	}
	return p
}

func (p *pairedAgain) listRoster(t *testing.T) {
	t.Helper()
	conn := dialControl(t, p.srv)
	sendCmd(t, conn, command{Cmd: "remoteDevices"})
	var msg approveDevicesMsg
	readUntil(t, conn, "remoteDevices", &msg)
}

func TestAPairedAgainDeviceUnderTheSameIdNeedsANewEnable(t *testing.T) {
	p := newPairedAgain(t)
	if !p.srv.mayApproveWithKey("d1", p.keyA) {
		t.Fatal("the grant does not allow the device before it is paired again")
	}
	p.swap(remote.Verified) // the same id, another key, and the person verified it too
	p.listRoster(t)
	waitUntil(t, "the grant to be cleared", func() bool { return !p.srv.deviceAccess("d1").Approve })
	if rec, _ := store.PeekDeviceAccess(); rec["d1"].Approve || rec["d1"].ApproveKey != "" {
		t.Errorf("the saved record still holds the grant: %+v", rec["d1"])
	}
	if p.srv.mayApproveWithKey("d1", p.keyA) {
		t.Error("the old key is still allowed")
	}
}

func TestAKeyThatChangedClearsTheGrant(t *testing.T) {
	p := newPairedAgain(t)
	// The key the relay lists is the same one, but the verifier says it changed
	// since it was verified: swap lists key B, so use a verify state on key A.
	p.fake.setVerified("d1", remote.KeyOriginUsual, remote.KeyChanged)
	p.listRoster(t)
	waitUntil(t, "the grant to be cleared", func() bool { return !p.srv.deviceAccess("d1").Approve })
}

func TestUnverifyingTheKeyClearsTheGrant(t *testing.T) {
	p := newPairedAgain(t)
	desk := dialControlPage(t, p.srv)
	sendCmd(t, desk, command{Cmd: "remoteUnverify", ID: "d1", Kind: "usual"})
	waitUntil(t, "the grant to be cleared", func() bool { return !p.srv.deviceAccess("d1").Approve })
	if p.srv.mayApproveWithKey("d1", p.keyA) {
		t.Error("the device may still approve")
	}
	// Taking away the desk-origin verification is another key and changes nothing.
	p2 := newPairedAgain(t)
	desk = dialControlPage(t, p2.srv)
	sendCmd(t, desk, command{Cmd: "remoteUnverify", ID: "d1", Kind: "desk"})
	time.Sleep(300 * time.Millisecond)
	if !p2.srv.approveSwitchOn("d1") {
		t.Error("unverifying the desk key took the grant away")
	}
}

func TestVerifyingADifferentKeyClearsTheGrant(t *testing.T) {
	p := newPairedAgain(t)
	p.swap(remote.NotVerified)
	desk := dialControlPage(t, p.srv)
	sendCmd(t, desk, command{Cmd: "remoteVerify", ID: "d1", Kind: "usual", Text: "1234"})
	waitUntil(t, "the grant to be cleared", func() bool { return !p.srv.deviceAccess("d1").Approve })
}

func TestAnUnpairedDeviceMissingFromTheRosterBumpsTheGeneration(t *testing.T) {
	p := newPairedAgain(t)
	before := p.srv.approveGen.Load()
	p.srv.forgetRolesExcept([]remote.Device{{ID: "someone-else"}})
	if p.srv.approveGen.Load() == before {
		t.Error("an approved device left the roster and an enable in flight would still apply")
	}
}

// ---------------------------------------------------------------------------
// The running program cannot write a grant back that the file took away.
// ---------------------------------------------------------------------------

func TestAnUnrelatedSaveDoesNotWriteBackAGrantTheFileTookAway(t *testing.T) {
	e := newApproveEnv(t)
	e.allow(t, "d1")
	// `flockdeck remote approvals off` with the running program not told.
	p, err := store.ReadPrefs()
	if err != nil {
		t.Fatal(err)
	}
	p, _ = p.WithoutApprovals()
	if err := store.SavePrefs(p); err != nil {
		t.Fatal(err)
	}
	// The running program saves something else.
	desk := dialControlPage(t, e.srv)
	sendCmd(t, desk, command{Cmd: "helpSeen"})
	waitUntil(t, "the unrelated save", func() bool {
		q, _ := store.ReadPrefs()
		return string(q.UI[store.UIHelpSeen]) == "true"
	})
	if rec, _ := store.PeekDeviceAccess(); rec["d1"].Approve {
		t.Errorf("the save wrote the grant back: %+v", rec["d1"])
	}
	waitUntil(t, "the record in force to follow", func() bool { return !e.srv.deviceAccess("d1").Approve })
}

// ---------------------------------------------------------------------------
// The gate reads the file cheaply and never quarantines it.
// ---------------------------------------------------------------------------

func TestTheGateDoesNotQuarantineADamagedFileAndCachesTheRead(t *testing.T) {
	e := newApproveEnv(t)
	e.allow(t, "d1")
	path := prefsPath(t)
	dir := filepath.Dir(path)
	damaged := []byte("{ not json")
	if err := os.WriteFile(path, damaged, 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if e.srv.approveSwitchOn("d1") || e.srv.mayApproveWithKey("d1", e.fake.keys["d1"]) {
			t.Fatal("a device may approve while the file is damaged")
		}
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(damaged) {
		t.Errorf("the file was moved or changed by the gate: %q, %v", got, err)
	}
	ents, _ := os.ReadDir(dir)
	for _, ent := range ents {
		if strings.HasPrefix(ent.Name(), "prefs.json.") || strings.Contains(ent.Name(), "damaged") {
			t.Errorf("the gate left %s beside the file", ent.Name())
		}
	}

	// Repaired, it is trusted again, and a second call reuses the read.
	if err := store.SavePrefs(store.LoadPrefs()); err != nil {
		t.Fatal(err)
	}
	removeFile(t, path)
	good, _ := store.LoadPrefs().WithAccess("d1", e.srv.deviceAccess("d1"))
	if err := store.SavePrefs(good); err != nil {
		t.Fatal(err)
	}
	if !e.srv.approveSwitchOn("d1") {
		t.Fatal("the repaired file is not trusted")
	}
	e.srv.approveFile.mu.Lock()
	at := e.srv.approveFile.at
	e.srv.approveFile.mu.Unlock()
	for i := 0; i < 5; i++ {
		e.srv.approveSwitchOn("d1")
	}
	e.srv.approveFile.mu.Lock()
	again := e.srv.approveFile.at
	e.srv.approveFile.mu.Unlock()
	if !again.Equal(at) {
		t.Error("the file was read again though it had not changed")
	}
}

// ---------------------------------------------------------------------------
// The wording.
// ---------------------------------------------------------------------------

func TestTheWordingDoesNotPromiseTheRelayCannotReadCards(t *testing.T) {
	if strings.Contains(approveAckText, "The relay cannot read them") {
		t.Error("the wording still promises that the relay cannot read the cards")
	}
	for _, want := range []string{"only passes traffic along", "tampered relay", "tampered page"} {
		if !strings.Contains(approveAckText, want) {
			t.Errorf("the wording lacks %q", want)
		}
	}
	if approveAckVersion < 2 {
		t.Errorf("the wording changed but its version is %d", approveAckVersion)
	}
	// A grant made at the older wording no longer counts.
	if (store.DeviceAccess{Role: store.RoleFull, Approve: true, ApproveV: 1, ApproveKey: "x"}).ApproveFor(approveAckVersion, "x") {
		t.Error("a grant at wording version 1 still counts")
	}
}

// An unpair that arrives as a roster refresh (not through forgetRole) while an
// enable is being checked: the device has no record yet, so only the change in
// the roster shows it went.
func TestARosterRefreshThatDropsADeviceWinsOverAnEnableInFlight(t *testing.T) {
	e := newApproveEnv(t)
	e.srv.forgetRolesExcept([]remote.Device{{ID: "d1"}, {ID: "d2"}})
	release, notes := slowEnable(t, e)
	defer release()
	e.srv.forgetRolesExcept([]remote.Device{{ID: "d2"}})
	time.Sleep(300 * time.Millisecond)
	release()
	if n := <-notes; !n.Error || !strings.Contains(n.Text, "switched off") {
		t.Errorf("the enable was answered %+v, want it refused as superseded", n)
	}
	stillOff(t, e.srv, "d1", "the roster dropped the device during the enable")
}

func TestARosterRefreshThatKeepsEveryDeviceDoesNotBump(t *testing.T) {
	e := newApproveEnv(t)
	e.srv.forgetRolesExcept([]remote.Device{{ID: "d1"}, {ID: "d2"}})
	before := e.srv.approveGen.Load()
	e.srv.forgetRolesExcept([]remote.Device{{ID: "d1"}, {ID: "d2"}, {ID: "d3"}})
	if e.srv.approveGen.Load() != before {
		t.Error("a refresh that only added a device bumped the generation")
	}
}

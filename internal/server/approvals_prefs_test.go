package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/e2e"
	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/store"
)

// approveFake is a remote access that keeps verifications in memory (as
// verifyRemote does) and has an end-to-end key for the devices a test gives one,
// so the desk can check a device before it lets it approve.
type approveFake struct {
	*verifyRemote
	kmu     sync.Mutex
	keys    map[string][]byte
	origins []remote.KeyOrigin
	// gate, when set, holds a key lookup until it is closed; reached is told
	// that one is waiting. It stands in for a slow relay.
	gate    chan struct{}
	reached chan struct{}
}

func (f *approveFake) E2EDeviceKey(_ context.Context, device string, origin remote.KeyOrigin) ([]byte, bool) {
	f.kmu.Lock()
	gate, reached := f.gate, f.reached
	f.kmu.Unlock()
	if gate != nil {
		select {
		case reached <- struct{}{}:
		default:
		}
		<-gate
	}
	f.kmu.Lock()
	defer f.kmu.Unlock()
	f.origins = append(f.origins, origin)
	k, ok := f.keys[device]
	return k, ok
}

func (f *approveFake) setVerified(device string, origin remote.KeyOrigin, s remote.VerifyState) {
	f.verifyRemote.mu.Lock()
	defer f.verifyRemote.mu.Unlock()
	f.verifyRemote.states[fmt.Sprintf("%s/%d", device, origin)] = s
}

type approveEnv struct {
	srv  *Server
	fake *approveFake
	ts   *httptest.Server
}

// newApproveEnv is a server whose relay lists d1 and d2, both with a key, d1's
// usual key verified. Nothing is switched on.
func newApproveEnv(t *testing.T) *approveEnv {
	t.Helper()
	srv, _ := newTestServer(t)
	fake := &approveFake{
		verifyRemote: newVerifyRemote(t, `[{"id":"d1","name":"phone"},{"id":"d2","name":"tablet"}]`, ""),
		keys:         map[string][]byte{"d1": {1, 2, 3}, "d2": {4, 5, 6}},
	}
	fake.setVerified("d1", remote.KeyOriginUsual, remote.Verified)
	srv.SetRemote(fake)
	listDevice(srv, "d1", "d2")
	return &approveEnv{srv: srv, fake: fake, ts: remoteServer(t, srv)}
}

// allow has the desk allow a device to approve, the way the dialog does, and
// waits until the host has applied it.
func (e *approveEnv) allow(t *testing.T, device string) {
	t.Helper()
	e.fake.setVerified(device, remote.KeyOriginUsual, remote.Verified)
	desk := dialControlPage(t, e.srv)
	if note := turnOn(t, desk, device); note.Error {
		t.Fatalf("refused: %+v", note)
	}
	waitUntil(t, "approval to be switched on for "+device, func() bool { return e.srv.approveSwitchOn(device) })
}

// askChallenge opens the question for a device, as the dialog does, and returns
// the host's answer.
func askChallenge(t *testing.T, conn *websocket.Conn, device string) approveChallengeMsg {
	t.Helper()
	sendCmd(t, conn, command{Cmd: "approveChallenge", ID: device})
	var m approveChallengeMsg
	readUntil(t, conn, "approveChallenge", &m)
	return m
}

// turnOn is what the dialog sends when the person says yes: the question first,
// then the answer with the nonce and the wording version it was given.
func turnOn(t *testing.T, conn *websocket.Conn, device string) noticeMsg {
	t.Helper()
	ch := askChallenge(t, conn, device)
	return noticeAfter(t, conn, command{Cmd: "setDeviceApprove", ID: device, On: true, Nonce: ch.Nonce, V: ch.V})
}

// noticeAfter sends a command and returns the first notice that comes back.
func noticeAfter(t *testing.T, conn *websocket.Conn, cmd command) noticeMsg {
	t.Helper()
	sendCmd(t, conn, cmd)
	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	return note
}

// putAccess sets a device's record in memory and in the file, for the states the
// commands cannot produce.
func putAccess(t *testing.T, srv *Server, device string, a store.DeviceAccess) {
	t.Helper()
	ask(srv, func() bool {
		next, ok := srv.prefs.WithAccess(device, a)
		if !ok {
			t.Error("could not record the access")
		}
		srv.prefs = next
		srv.publishAccess()
		if err := store.SavePrefs(next); err != nil {
			t.Error(err)
		}
		return true
	})
}

// stillOff waits a moment, since the command under test runs on another
// goroutine, and fails if device may approve.
func stillOff(t *testing.T, srv *Server, device, why string) {
	t.Helper()
	time.Sleep(200 * time.Millisecond)
	if srv.approveSwitchOn(device) {
		t.Errorf("%s: %s may approve", why, device)
	}
	if srv.deviceAccess(device).Approve {
		t.Errorf("%s: the record for %s says approve", why, device)
	}
	if p, _ := store.ReadPrefs(); p.AccessFor(device).Approve {
		t.Errorf("%s: the saved record for %s says approve", why, device)
	}
}

func TestApprovalIsOffForEveryDeviceByDefault(t *testing.T) {
	e := newApproveEnv(t)
	for _, id := range []string{"d1", "d2", "never-heard-of", ""} {
		if e.srv.approveSwitchOn(id) {
			t.Errorf("%q may approve with nothing switched on", id)
		}
	}
	// A full device with a record for another reason is still off.
	putAccess(t, e.srv, "d1", store.DeviceAccess{Role: store.RoleViewer, WatchPanes: true})
	setAccess(t, e.srv, "d2", "full", false)
	if e.srv.approveSwitchOn("d1") || e.srv.approveSwitchOn("d2") {
		t.Error("a device with a role record may approve")
	}
	if got := e.srv.deviceRole("never-heard-of"); got != store.RoleFull {
		t.Fatalf("an unknown device is %q; the test means a full one", got)
	}
}

func TestAPhoneIsAllowedToApproveWhenEverythingIsInPlace(t *testing.T) {
	e := newApproveEnv(t)
	desk := dialControlPage(t, e.srv)
	if note := turnOn(t, desk, "d1"); note.Error {
		t.Fatalf("refused: %+v", note)
	}
	if !e.srv.approveSwitchOn("d1") {
		t.Fatal("d1 may not approve after the desk allowed it")
	}
	if a := e.srv.deviceAccess("d1"); !a.Approve || a.ApproveV != approveAckVersion || a.ApproveAt.IsZero() {
		t.Errorf("record = %+v, want approve, the wording version and a time", a)
	}
	if e.srv.approveSwitchOn("d2") {
		t.Error("another device may approve")
	}
	p, err := store.ReadPrefs()
	if err != nil || !p.AccessFor("d1").ApproveOn(approveAckVersion) {
		t.Errorf("saved record = %+v, %v", p.Devices["d1"], err)
	}
	// The key looked at is the device's usual one, which the phone app uses.
	e.fake.kmu.Lock()
	defer e.fake.kmu.Unlock()
	if len(e.fake.origins) == 0 {
		t.Error("the key was never looked up")
	}
	for _, o := range e.fake.origins {
		if o != remote.KeyOriginUsual {
			t.Errorf("looked up the %v key, want the usual one", o)
		}
	}
}

func TestAPhoneIsNotAllowedToApproveWithoutAVerifiedKey(t *testing.T) {
	cases := map[string]func(*approveEnv){
		"key not verified": func(e *approveEnv) { e.fake.setVerified("d1", remote.KeyOriginUsual, remote.NotVerified) },
		"key changed":      func(e *approveEnv) { e.fake.setVerified("d1", remote.KeyOriginUsual, remote.KeyChanged) },
		"only the desk key is verified": func(e *approveEnv) {
			e.fake.setVerified("d1", remote.KeyOriginUsual, remote.NotVerified)
			e.fake.setVerified("d1", remote.KeyOriginDesk, remote.Verified)
		},
		"no key": func(e *approveEnv) { e.fake.keys = map[string][]byte{} },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			e := newApproveEnv(t)
			setup(e)
			desk := dialControlPage(t, e.srv)
			note := turnOn(t, desk, "d1")
			if !note.Error || !strings.Contains(note.Text, "not been verified") {
				t.Errorf("answered %+v, want it refused for the key", note)
			}
			stillOff(t, e.srv, "d1", name)
		})
	}
}

func TestAPhoneIsNotAllowedToApproveWhenTheRelayDoesNotListIt(t *testing.T) {
	e := newApproveEnv(t)
	e.fake.keys["gone"] = []byte{9}
	e.fake.setVerified("gone", remote.KeyOriginUsual, remote.Verified)
	desk := dialControlPage(t, e.srv)
	note := turnOn(t, desk, "gone")
	if !note.Error || !strings.Contains(note.Text, "not paired") {
		t.Errorf("answered %+v, want it refused as not paired", note)
	}
	stillOff(t, e.srv, "gone", "unlisted")
}

func TestOnlyADeviceIdIsAccepted(t *testing.T) {
	e := newApproveEnv(t)
	desk := dialControlPage(t, e.srv)
	for _, id := range []string{"", "../../etc", strings.Repeat("a", 129)} {
		note := noticeAfter(t, desk, command{Cmd: "setDeviceApprove", ID: id, On: true, Nonce: "x", V: approveAckVersion})
		if !note.Error {
			t.Errorf("id %q was answered %+v, want an error", id, note)
		}
	}
	if p, _ := store.ReadPrefs(); len(p.Devices) != 0 {
		t.Errorf("saved records = %+v, want none", p.Devices)
	}
}

// Neither turning it on nor turning it off is accepted from a window reached
// through the relay, even by a full device, and the desk is told of the try.
func TestApprovalIsChangedOnlyAtTheDesk(t *testing.T) {
	e := newApproveEnv(t)
	e.allow(t, "d1")
	desk := dialControlPage(t, e.srv)
	phone, err := dialRemoteControlDevice(e.ts, e.ts.URL, "d2")
	if err != nil {
		t.Fatal(err)
	}
	defer phone.CloseNow()
	for _, cmd := range []command{
		{Cmd: "setDeviceApprove", ID: "d2", On: true, Confirmed: true},
		{Cmd: "setDeviceApprove", ID: "d1", On: false},
		{Cmd: "setApprovalsOff"},
	} {
		note := noticeAfter(t, phone, cmd)
		if !note.Error || note.Text != deskOnlyApprove {
			t.Errorf("%s through the relay was answered %+v", cmd.Cmd, note)
		}
		var seen noticeMsg
		readUntil(t, desk, "notice", &seen)
		if !seen.Error || !strings.Contains(seen.Text, cmd.Cmd) {
			t.Errorf("%s: the desk was told %+v, want a notice naming the command", cmd.Cmd, seen)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if !e.srv.approveSwitchOn("d1") {
		t.Error("a window reached through the relay switched approval off")
	}
	stillOff(t, e.srv, "d2", "through the relay")
}

func TestApprovalIsSwitchedOffFromAnyWindowAtTheDesk(t *testing.T) {
	e := newApproveEnv(t)
	e.allow(t, "d1")
	script := dialControl(t, e.srv)
	note := noticeAfter(t, script, command{Cmd: "setDeviceApprove", ID: "d1", On: false})
	if note.Error {
		t.Fatalf("refused: %+v", note)
	}
	stillOff(t, e.srv, "d1", "after off")
}

func TestSwitchingApprovalOffEndsTheDevicesSockets(t *testing.T) {
	e := newApproveEnv(t)
	e.allow(t, "d1")
	phone, err := dialRemoteControlDevice(e.ts, e.ts.URL, "d1")
	if err != nil {
		t.Fatal(err)
	}
	defer phone.CloseNow()
	readRemoteMsg(t, phone, "hello")
	desk := dialControlPage(t, e.srv)
	sendCmd(t, desk, command{Cmd: "setDeviceApprove", ID: "d1", On: false})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		if _, _, err := phone.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatal("the device's socket stayed open after approval was switched off")
			}
			return
		}
	}
}

// An agreement to older wording does not count.
func TestAnOldAgreementDoesNotCount(t *testing.T) {
	e := newApproveEnv(t)
	putAccess(t, e.srv, "d1", store.DeviceAccess{Role: store.RoleFull, Approve: true, ApproveV: approveAckVersion - 1, ApproveAt: time.Now().UTC()})
	if e.srv.approveSwitchOn("d1") {
		t.Error("a device may approve on an agreement to older wording")
	}
	putAccess(t, e.srv, "d1", store.DeviceAccess{Role: store.RoleFull, Approve: true})
	if e.srv.approveSwitchOn("d1") {
		t.Error("a device may approve with no agreement recorded")
	}
	putAccess(t, e.srv, "d1", store.DeviceAccess{Role: store.RoleFull, Approve: true, ApproveV: approveAckVersion, ApproveAt: time.Now().UTC()})
	if !e.srv.approveSwitchOn("d1") {
		t.Error("a device may not approve on a current agreement")
	}
}

// The running program's record is the one in force, and the file can only take
// approval away: deleted, damaged or edited while Flockdeck runs, it says no.
func TestTheFileCanTakeApprovalAwayButNotGiveIt(t *testing.T) {
	e := newApproveEnv(t)
	e.allow(t, "d1")
	path := prefsPath(t)

	if err := os.WriteFile(path, []byte(`{"devices":{"d1":{"role":"full"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if e.srv.approveSwitchOn("d1") {
		t.Error("d1 may approve although the file now says it may not")
	}
	if err := os.WriteFile(path, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if e.srv.approveSwitchOn("d1") {
		t.Error("d1 may approve although the file is damaged")
	}
	removeFile(t, path)
	if e.srv.approveSwitchOn("d1") {
		t.Error("d1 may approve although the file is gone")
	}

	// And a file that grants it does not, with the program's own record off.
	e2 := newApproveEnv(t)
	good := `{"devices":{"d2":{"role":"full","approve":true,"approveV":` + fmt.Sprint(approveAckVersion) + `}}}`
	if err := os.WriteFile(path, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	if e2.srv.approveSwitchOn("d2") {
		t.Error("d2 may approve because the file was edited, not because the desk said so")
	}
}

func TestTheKillSwitchSwitchesEveryDeviceOffAndKeepsRoles(t *testing.T) {
	e := newApproveEnv(t)
	e.allow(t, "d1")
	e.allow(t, "d2")
	setAccess(t, e.srv, "d2", "viewer", true)
	waitUntil(t, "d2 to keep approval after its role changed", func() bool { return e.srv.approveSwitchOn("d2") })
	d1sock, err := dialRemoteControlDevice(e.ts, e.ts.URL, "d1")
	if err != nil {
		t.Fatal(err)
	}
	defer d1sock.CloseNow()
	readRemoteMsg(t, d1sock, "hello")

	desk := dialControlPage(t, e.srv)
	note := noticeAfter(t, desk, command{Cmd: "setApprovalsOff"})
	if note.Error || !strings.Contains(note.Text, "2 devices") {
		t.Errorf("answered %+v, want it to say two devices", note)
	}
	stillOff(t, e.srv, "d1", "after the kill switch")
	stillOff(t, e.srv, "d2", "after the kill switch")
	if a := e.srv.deviceAccess("d2"); a.EffectiveRole() != store.RoleViewer || !a.WatchPanes {
		t.Errorf("d2 = %+v, want its role and watch setting kept", a)
	}
	if a := e.srv.deviceAccess("d2"); a.ApproveV != 0 || !a.ApproveAt.IsZero() {
		t.Errorf("d2 keeps an agreement: %+v", a)
	}
	p, _ := store.ReadPrefs()
	if _, has := p.Devices["d1"]; has {
		t.Errorf("d1 has a record of nothing: %+v", p.Devices["d1"])
	}
	// d1's socket was cut so that it reconnects without the capability.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		if _, _, err := d1sock.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatal("d1's socket stayed open")
			}
			break
		}
	}
	// Again, with nothing on, says so and changes nothing.
	note = noticeAfter(t, desk, command{Cmd: "setApprovalsOff"})
	if note.Error || !strings.Contains(note.Text, "No device") {
		t.Errorf("a second switch-off answered %+v", note)
	}
}

func TestChangingARoleKeepsApprovalAndUnpairingDropsIt(t *testing.T) {
	e := newApproveEnv(t)
	e.allow(t, "d1")
	setAccess(t, e.srv, "d1", "viewer", false)
	if !e.srv.approveSwitchOn("d1") {
		t.Error("making a device a viewer took its approval away")
	}
	setAccess(t, e.srv, "d1", "full", false)
	if !e.srv.approveSwitchOn("d1") {
		t.Error("making a device full again took its approval away")
	}
	if a := e.srv.deviceAccess("d1"); a.ApproveV != approveAckVersion || a.ApproveKey == "" {
		t.Errorf("record = %+v, want the agreement and its key kept", a)
	}
	e.srv.forgetRole("d1")
	waitUntil(t, "the record to go", func() bool { return !e.srv.deviceAccess("d1").Approve })
	if e.srv.approveSwitchOn("d1") {
		t.Error("an unpaired device may approve")
	}
	if p, _ := store.ReadPrefs(); len(p.Devices) != 0 {
		t.Errorf("saved records = %+v, want none", p.Devices)
	}
}

// A setting that cannot be saved is not given, and one that narrows is applied
// whether or not it can be saved, as a restriction is.
func TestApprovalIsGivenOnlyIfItCanBeSaved(t *testing.T) {
	e := newApproveEnv(t)
	breakPrefsFile(t)
	desk := dialControlPage(t, e.srv)
	note := turnOn(t, desk, "d1")
	if !note.Error || !strings.Contains(note.Text, "not allowed to approve") {
		t.Errorf("answered %+v, want it refused for the save", note)
	}
	time.Sleep(200 * time.Millisecond)
	if e.srv.approveSwitchOn("d1") || e.srv.deviceAccess("d1").Approve {
		t.Error("approval was given although it could not be saved")
	}
}

func TestSwitchingOffIsAppliedEvenWhenItCannotBeSaved(t *testing.T) {
	e := newApproveEnv(t)
	e.allow(t, "d1")
	path := prefsPath(t)
	removeFile(t, path)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	desk := dialControlPage(t, e.srv)
	note := noticeAfter(t, desk, command{Cmd: "setDeviceApprove", ID: "d1", On: false})
	if !note.Error || !strings.Contains(note.Text, "applied now, but could not be saved") {
		t.Errorf("answered %+v, want it applied but not saved", note)
	}
	if e.srv.deviceAccess("d1").Approve {
		t.Error("the record still says approve")
	}
}

func TestHelloSaysCanApproveToADeviceThatMay(t *testing.T) {
	e := newApproveEnv(t)
	hello := func(device string) map[string]any {
		t.Helper()
		c, err := dialRemoteControlDevice(e.ts, e.ts.URL, device)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.CloseNow() })
		return readRemoteMsg(t, c, "hello")
	}
	if _, has := hello("d1")["canApprove"]; has {
		t.Error("canApprove is in the hello while approval is off")
	}
	e.allow(t, "d1")
	if hello("d1")["canApprove"] != true {
		t.Error("canApprove is missing from a device that may approve")
	}
	if _, has := hello("d2")["canApprove"]; has {
		t.Error("canApprove is in the hello of a device that may not")
	}
	// A viewer is told too: approval works for either role.
	setAccess(t, e.srv, "d1", "viewer", false)
	if hello("d1")["canApprove"] != true {
		t.Error("a viewer that may approve is not told so")
	}
	// A window on this machine is not told anything about it.
	desk := dialControl(t, e.srv)
	h := readRemoteMsg(t, desk, "hello")
	if _, has := h["canApprove"]; has {
		t.Error("a window on this machine was sent canApprove")
	}
	e.srv.stopApprovals(func(int, error) {})
	waitUntil(t, "approval to be off", func() bool { return !e.srv.approveSwitchOn("d1") })
	if _, has := hello("d1")["canApprove"]; has {
		t.Error("canApprove is still in the hello after the kill switch")
	}
}

type approveDevicesMsg struct {
	Devices []struct {
		ID         string `json:"id"`
		Approve    bool   `json:"approve"`
		ApproveKey string `json:"approveKey"`
		Verify     string `json:"verify"`
	} `json:"devices"`
}

func TestRemoteDevicesSaysWhoMayApproveAndWhetherTheKeyIsVerified(t *testing.T) {
	usual, _ := e2e.GenerateStaticKey()
	enc := e2e.EncodePublicKey(usual.PublicKey())
	hash := store.KeyHash(usual.PublicKey().Bytes())
	srv, _ := newTestServer(t)
	fake := &approveFake{
		verifyRemote: newVerifyRemote(t, fmt.Sprintf(`[{"id":"d1","name":"a","publicKey":%q},{"id":"d2","name":"b","publicKey":%q},{"id":"d3","name":"c"},{"id":"d4","name":"d","publicKey":%q}]`, enc, enc, enc), ""),
		keys:         map[string][]byte{"d1": usual.PublicKey().Bytes()},
	}
	fake.setVerified("d1", remote.KeyOriginUsual, remote.Verified)
	fake.setVerified("d2", remote.KeyOriginDesk, remote.Verified)
	fake.setVerified("d4", remote.KeyOriginUsual, remote.KeyChanged)
	srv.SetRemote(fake)
	now := time.Now().UTC()
	putAccess(t, srv, "d1", store.DeviceAccess{Role: store.RoleFull, Approve: true, ApproveV: approveAckVersion, ApproveAt: now, ApproveKey: hash})
	putAccess(t, srv, "d2", store.DeviceAccess{Role: store.RoleFull, Approve: true, ApproveV: approveAckVersion - 1})
	putAccess(t, srv, "d4", store.DeviceAccess{Role: store.RoleFull, Approve: true, ApproveV: approveAckVersion, ApproveAt: now, ApproveKey: hash})
	conn := dialControl(t, srv)
	sendCmd(t, conn, command{Cmd: "remoteDevices"})
	var msg approveDevicesMsg
	readUntil(t, conn, "remoteDevices", &msg)
	if len(msg.Devices) != 4 {
		t.Fatalf("%d devices", len(msg.Devices))
	}
	d1, d2, d3, d4 := msg.Devices[0], msg.Devices[1], msg.Devices[2], msg.Devices[3]
	if !d1.Approve || d1.ApproveKey != "verified" {
		t.Errorf("d1 = %+v, want approval on with a verified key", d1)
	}
	if d2.Approve || d2.ApproveKey != "" {
		t.Errorf("d2 = %+v, want a grant that is not for this key off (only the desk key is verified)", d2)
	}
	if d3.Approve || d3.ApproveKey != "" {
		t.Errorf("d3 = %+v, want nothing", d3)
	}
	if d4.Approve || d4.ApproveKey != "changed" {
		t.Errorf("d4 = %+v, want approval off and its key changed", d4)
	}
	// And the stale grants are switched off for good, not just drawn as off.
	waitUntil(t, "the stale grants to be cleared", func() bool {
		return !srv.deviceAccess("d2").Approve && !srv.deviceAccess("d4").Approve
	})
	if !srv.deviceAccess("d1").Approve {
		t.Error("a grant that holds was cleared")
	}

	// A window reached through the relay is not told how another device is set.
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControlDevice(ts, ts.URL, "d3")
	if err != nil {
		t.Fatal(err)
	}
	defer phone.CloseNow()
	sendCmd(t, phone, command{Cmd: "remoteDevices"})
	m := readRemoteMsg(t, phone, "remoteDevices")
	for _, d := range m["devices"].([]any) {
		if _, has := d.(map[string]any)["approve"]; has {
			t.Errorf("a remote window was told who may approve: %v", d)
		}
	}
}

func TestViewersCannotChangeApproval(t *testing.T) {
	e := newApproveEnv(t)
	e.allow(t, "d1")
	setAccess(t, e.srv, "d2", "viewer", true)
	for _, name := range []string{"setDeviceApprove", "setApprovalsOff"} {
		if commandAccess[name] != accessFull {
			t.Errorf("commandAccess[%q] = %v, want full", name, commandAccess[name])
		}
		if e.srv.allowCommand(&controlClient{remote: true, device: "d2"}, name) {
			t.Errorf("a viewer may send %s", name)
		}
	}
	viewer, err := dialRemoteControlDevice(e.ts, e.ts.URL, "d2")
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.CloseNow()
	sendCmd(t, viewer, command{Cmd: "setApprovalsOff"})
	time.Sleep(200 * time.Millisecond)
	if !e.srv.approveSwitchOn("d1") {
		t.Error("a viewer switched approval off")
	}
}

func TestTheStopEndpointSwitchesApprovalOffInMemory(t *testing.T) {
	e := newApproveEnv(t)
	e.allow(t, "d1")
	base := "http://" + e.srv.Addr()

	// Not through the relay, not by GET, not without the token.
	resp, err := http.Post(e.ts.URL+"/remote/approvals/stop?t="+e.srv.Token(), "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode < 300 {
		t.Errorf("the relay's side answered %d", resp.StatusCode)
	}
	if resp, err = http.Post(base+"/remote/approvals/stop", "text/plain", nil); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode < 300 {
		t.Errorf("no token answered %d", resp.StatusCode)
	}
	if resp, err = http.Get(base + "/remote/approvals/stop?t=" + e.srv.Token()); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode < 300 {
		t.Errorf("GET answered %d", resp.StatusCode)
	}
	if !e.srv.approveSwitchOn("d1") {
		t.Fatal("a refused request switched approval off")
	}

	// The command line writes the file first. The memory still says on, and the
	// call is what changes it.
	p, _ := store.ReadPrefs()
	p, _ = p.WithoutApprovals()
	if err := store.SavePrefs(p); err != nil {
		t.Fatal(err)
	}
	if err := RequestApprovalsStop(base, e.srv.Token()); err != nil {
		t.Fatal(err)
	}
	if e.srv.deviceAccess("d1").Approve {
		t.Error("the running program still has approval on")
	}
	// With nothing on it still answers.
	if err := RequestApprovalsStop(base, e.srv.Token()); err != nil {
		t.Errorf("a second stop failed: %v", err)
	}
}

// When what each device may do is not known, no device is allowed to approve.
func TestNoDeviceIsAllowedToApproveWhileAccessIsUnknown(t *testing.T) {
	e := newApproveEnv(t)
	ask(e.srv, func() bool { e.srv.prefs.KeepAccessUnknown(); e.srv.publishAccess(); return true })
	desk := dialControlPage(t, e.srv)
	note := turnOn(t, desk, "d1")
	if !note.Error || !strings.Contains(note.Text, "could not be read") {
		t.Errorf("answered %+v, want it refused because access is not known", note)
	}
	stillOff(t, e.srv, "d1", "access unknown")
}

// The words the desk's dialog shows are the host's own, and a client that skips
// the question is answered with them. A page that drifted from the host's
// wording would have the person agree to something else.
func TestTheDialogShowsTheHostsWordsForApproval(t *testing.T) {
	src, err := os.ReadFile("../../ui/src/lib/remote.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), strconv.Quote(approveAckText)) {
		t.Errorf("ui/src/lib/remote.js does not hold the wording %q", approveAckText)
	}
	if !strings.Contains(approveAckText, "tampered relay") || approveAckVersion != 2 {
		t.Errorf("the wording or its version changed (%d): raise approveAckVersion and read the plan before editing", approveAckVersion)
	}
}

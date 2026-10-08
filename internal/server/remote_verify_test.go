package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jmwri/flockdeck/internal/e2e"
	"github.com/jmwri/flockdeck/internal/remote"
)

// verifyRemote is a remote access that keeps verifications in memory and
// records what it was asked.
type verifyRemote struct {
	relayedRemote
	mu        sync.Mutex
	states    map[string]remote.VerifyState // "device/origin"
	verifies  []string                      // "device/origin/code"
	unverifys []string
	forgotten []string
	keptOnly  [][]string
	verifyErr error
}

func (v *verifyRemote) DeviceVerifyState(id string, o remote.KeyOrigin, _ []byte) remote.VerifyState {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.states[fmt.Sprintf("%s/%d", id, o)]
}

func (v *verifyRemote) VerifyDevice(_ context.Context, id string, o remote.KeyOrigin, claimed string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.verifies = append(v.verifies, fmt.Sprintf("%s/%d/%s", id, o, claimed))
	return v.verifyErr
}

func (v *verifyRemote) UnverifyDevice(id string, o remote.KeyOrigin) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.unverifys = append(v.unverifys, fmt.Sprintf("%s/%d", id, o))
	return nil
}

func (v *verifyRemote) ForgetDevice(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.forgotten = append(v.forgotten, id)
	return nil
}

func (v *verifyRemote) calls() (verifies, unverifys int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.verifies), len(v.unverifys)
}

func newVerifyRemote(t *testing.T, devicesJSON, hostKey string) *verifyRemote {
	t.Helper()
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"devices":`+devicesJSON+`,"hosts":[{"id":"h1","name":"desk","self":true}]}`)
	}))
	t.Cleanup(relay.Close)
	return &verifyRemote{
		relayedRemote: relayedRemote{
			client:  remote.NewClient(&remote.Config{Relay: relay.URL, Token: "fdh_test"}, "v"),
			hostKey: hostKey,
		},
		states: map[string]remote.VerifyState{},
	}
}

type verifyDevicesMsg struct {
	Devices []struct {
		ID              string `json:"id"`
		Fingerprint     string `json:"fingerprint"`
		DeskFingerprint string `json:"deskFingerprint"`
		Verify          string `json:"verify"`
		DeskVerify      string `json:"deskVerify"`
	} `json:"devices"`
}

// The roster carries a code for each of a device's two keys, and whether a
// person verified each.
func TestRemoteDevicesCarriesTheDeskFingerprintAndVerifyState(t *testing.T) {
	hostPriv, _ := e2e.GenerateStaticKey()
	usualPriv, _ := e2e.GenerateStaticKey()
	deskPriv, _ := e2e.GenerateStaticKey()
	hostKey := e2e.EncodePublicKey(hostPriv.PublicKey())
	fake := newVerifyRemote(t, fmt.Sprintf(
		`[{"id":"d1","name":"phone","publicKey":%q,"deskPublicKey":%q},{"id":"d2","name":"old","publicKey":%q}]`,
		e2e.EncodePublicKey(usualPriv.PublicKey()), e2e.EncodePublicKey(deskPriv.PublicKey()),
		e2e.EncodePublicKey(usualPriv.PublicKey())), hostKey)
	fake.states[fmt.Sprintf("d1/%d", remote.KeyOriginDesk)] = remote.Verified
	fake.states[fmt.Sprintf("d1/%d", remote.KeyOriginUsual)] = remote.KeyChanged
	srv, _ := newTestServer(t)
	srv.SetRemote(fake)
	conn := dialControl(t, srv)
	sendCmd(t, conn, command{Cmd: "remoteDevices"})
	var msg verifyDevicesMsg
	readUntil(t, conn, "remoteDevices", &msg)
	if len(msg.Devices) != 2 {
		t.Fatalf("got %d devices", len(msg.Devices))
	}
	d1, d2 := msg.Devices[0], msg.Devices[1]
	if want := e2e.Fingerprint(deskPriv.PublicKey(), hostPriv.PublicKey()); d1.DeskFingerprint != want {
		t.Errorf("desk fingerprint = %q, want %q", d1.DeskFingerprint, want)
	}
	if want := e2e.Fingerprint(usualPriv.PublicKey(), hostPriv.PublicKey()); d1.Fingerprint != want {
		t.Errorf("fingerprint = %q, want %q", d1.Fingerprint, want)
	}
	if d1.DeskVerify != "verified" || d1.Verify != "changed" {
		t.Errorf("states = verify %q, desk %q; want changed and verified", d1.Verify, d1.DeskVerify)
	}
	if d2.DeskFingerprint != "" || d2.DeskVerify != "" || d2.Verify != "" {
		t.Errorf("a device with no desk key got desk data: %+v", d2)
	}
}

// The host half of the code is this machine's own key. A roster that lists
// another key for this machine (a relay that swapped it) does not change it.
func TestFingerprintUsesThisMachinesOwnKeyNotTheRosters(t *testing.T) {
	hostPriv, _ := e2e.GenerateStaticKey()
	swapped, _ := e2e.GenerateStaticKey()
	devPriv, _ := e2e.GenerateStaticKey()
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(
			`{"devices":[{"id":"d1","name":"p","deskPublicKey":%q}],"hosts":[{"id":"h1","self":true,"publicKey":%q}]}`,
			e2e.EncodePublicKey(devPriv.PublicKey()), e2e.EncodePublicKey(swapped.PublicKey())))
	}))
	defer relay.Close()
	srv, _ := newTestServer(t)
	srv.SetRemote(&relayedRemote{
		client:  remote.NewClient(&remote.Config{Relay: relay.URL, Token: "fdh_test"}, "v"),
		hostKey: e2e.EncodePublicKey(hostPriv.PublicKey()),
	})
	conn := dialControl(t, srv)
	sendCmd(t, conn, command{Cmd: "remoteDevices"})
	var msg verifyDevicesMsg
	readUntil(t, conn, "remoteDevices", &msg)
	if got, want := msg.Devices[0].DeskFingerprint, e2e.Fingerprint(devPriv.PublicKey(), hostPriv.PublicKey()); got != want {
		t.Errorf("fingerprint = %q, want the one against this machine's own key %q", got, want)
	}
}

// Marking a device verified is the desk's alone, and an attempt from a window
// reached through the relay is refused, written to error.log and shown at the
// desk.
func TestVerifyingADeviceIsDoneOnlyAtTheDesk(t *testing.T) {
	var logMu sync.Mutex
	var logged []string
	old := logf
	logf = func(format string, args ...any) {
		logMu.Lock()
		defer logMu.Unlock()
		logged = append(logged, fmt.Sprintf(format, args...))
	}
	t.Cleanup(func() { logf = old })

	fake := newVerifyRemote(t, `[]`, "")
	srv, _ := newTestServer(t)
	srv.SetRemote(fake)
	desk := dialControl(t, srv)
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatalf("dial through the tunnel: %v", err)
	}
	defer phone.CloseNow()

	const code = "11111 22222 33333 44444 55555 66666"
	for _, cmd := range []command{
		{Cmd: "remoteVerify", ID: "d1", Kind: "desk", Text: code},
		{Cmd: "remoteVerify", ID: "d1", Kind: "usual", Text: code},
		{Cmd: "remoteUnverify", ID: "d1", Kind: "desk"},
	} {
		sendCmd(t, phone, cmd)
		var note noticeMsg
		readUntil(t, phone, "notice", &note)
		if !note.Error || note.Text != deskOnlyVerify {
			t.Errorf("%s through the relay was answered %+v, want it refused", cmd.Cmd, note)
		}
		var deskNote noticeMsg
		readUntil(t, desk, "notice", &deskNote)
		if !deskNote.Error || !strings.Contains(deskNote.Text, cmd.Cmd) || !strings.Contains(deskNote.Text, "refused") {
			t.Errorf("the desk was told %+v of %s through the relay, want a notice naming it", deskNote, cmd.Cmd)
		}
	}
	if v, u := fake.calls(); v != 0 || u != 0 {
		t.Errorf("a window through the relay reached the verified record: %d verifies, %d unverifies", v, u)
	}
	logMu.Lock()
	if len(logged) != 3 || !strings.Contains(logged[0], "remoteVerify") || !strings.Contains(logged[2], "remoteUnverify") {
		t.Errorf("error.log lines = %q, want one per refused command", logged)
	}
	logMu.Unlock()

	sendCmd(t, desk, command{Cmd: "remoteVerify", ID: "d1", Kind: "desk", Text: code})
	var note noticeMsg
	readUntil(t, desk, "notice", &note)
	if note.Error {
		t.Errorf("the desk was refused: %+v", note)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if want := fmt.Sprintf("d1/%d/%s", remote.KeyOriginDesk, code); len(fake.verifies) != 1 || fake.verifies[0] != want {
		t.Errorf("verify calls = %v, want [%s]", fake.verifies, want)
	}
}

// A roster without a device drops what was recorded for it, so a device
// unpaired from its own page cannot come back verified.
func TestARosterWithoutADeviceForgetsItsVerification(t *testing.T) {
	fake := newVerifyRemote(t, `[{"id":"d2","name":"tablet"}]`, "")
	srv, _ := newTestServer(t)
	srv.SetRemote(fake)
	desk := dialControl(t, srv)
	sendCmd(t, desk, command{Cmd: "remoteDevices"})
	var msg verifyDevicesMsg
	readUntil(t, desk, "remoteDevices", &msg)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.keptOnly) != 1 || len(fake.keptOnly[0]) != 1 || fake.keptOnly[0][0] != "d2" {
		t.Errorf("records kept for %v, want only [d2]", fake.keptOnly)
	}
}

func TestVerifyCommandsRefuseAnUnknownOriginOrNoDevice(t *testing.T) {
	fake := newVerifyRemote(t, `[]`, "")
	srv, _ := newTestServer(t)
	srv.SetRemote(fake)
	desk := dialControl(t, srv)
	for _, cmd := range []command{
		{Cmd: "remoteVerify", ID: "d1", Kind: "", Text: "1"},
		{Cmd: "remoteVerify", ID: "d1", Kind: "host", Text: "1"},
		{Cmd: "remoteVerify", ID: "", Kind: "desk", Text: "1"},
		{Cmd: "remoteUnverify", ID: "d1", Kind: "../desk"},
		{Cmd: "remoteUnverify", ID: "", Kind: "usual"},
	} {
		sendCmd(t, desk, cmd)
		var note noticeMsg
		readUntil(t, desk, "notice", &note)
		if !note.Error {
			t.Errorf("%+v was accepted", cmd)
		}
	}
	if v, u := fake.calls(); v != 0 || u != 0 {
		t.Errorf("bad commands reached the record: %d, %d", v, u)
	}
}

// A mismatch is reported as an error, and the dialog is sent the list again.
func TestAMismatchedCodeIsReportedAtTheDesk(t *testing.T) {
	fake := newVerifyRemote(t, `[]`, "")
	fake.verifyErr = remote.ErrFingerprintMismatch
	srv, _ := newTestServer(t)
	srv.SetRemote(fake)
	desk := dialControl(t, srv)
	sendCmd(t, desk, command{Cmd: "remoteVerify", ID: "d1", Kind: "desk", Text: "1"})
	var note noticeMsg
	readUntil(t, desk, "notice", &note)
	if !note.Error {
		t.Errorf("a mismatch was reported as success: %+v", note)
	}
	var msg verifyDevicesMsg
	readUntil(t, desk, "remoteDevices", &msg)
}

// With nothing that can record a verification, the command is refused
// rather than appearing to work.
func TestVerifyIsRefusedWhereNothingCanRecordIt(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetRemote(&fakeRemote{})
	desk := dialControl(t, srv)
	sendCmd(t, desk, command{Cmd: "remoteVerify", ID: "d1", Kind: "desk", Text: "1"})
	var note noticeMsg
	readUntil(t, desk, "notice", &note)
	if !note.Error {
		t.Errorf("accepted with nothing to record it: %+v", note)
	}
}

// Unpairing a device drops what was recorded for it.
func TestUnpairingADeviceForgetsItsVerification(t *testing.T) {
	fake := newVerifyRemote(t, `[]`, "")
	srv, _ := newTestServer(t)
	srv.SetRemote(fake)
	desk := dialControl(t, srv)
	sendCmd(t, desk, command{Cmd: "remoteRevoke", ID: "d1"})
	var msg verifyDevicesMsg
	readUntil(t, desk, "remoteDevices", &msg)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.forgotten) != 1 || fake.forgotten[0] != "d1" {
		t.Errorf("forgotten = %v, want [d1]", fake.forgotten)
	}
}

func (v *verifyRemote) ForgetDevicesNotIn(ids []string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.keptOnly = append(v.keptOnly, ids)
	return nil
}

// A roster with no devices is not a reason to forget every verification: a
// proxy, a captive portal or a failing relay can send one.
func TestAnEmptyRosterForgetsNoVerification(t *testing.T) {
	fake := newVerifyRemote(t, `[]`, "")
	srv, _ := newTestServer(t)
	srv.SetRemote(fake)
	desk := dialControl(t, srv)
	sendCmd(t, desk, command{Cmd: "remoteDevices"})
	var msg verifyDevicesMsg
	readUntil(t, desk, "remoteDevices", &msg)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.keptOnly) != 0 {
		t.Errorf("an empty roster asked for records to be kept only for %v", fake.keptOnly)
	}
}

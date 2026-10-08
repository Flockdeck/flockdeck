package remote

import (
	"context"
	"crypto/ecdh"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/e2e"
	"github.com/jmwri/flockdeck/internal/store"
)

// verifyFixture is a manager with a real host identity and a fake relay
// listing one device, d1, that has registered both of its keys.
type verifyFixture struct {
	m       *Manager
	relay   *e2eFakeRelay
	usual   []byte // d1's usual key, raw
	desk    []byte // d1's desk key, raw
	usualFP string
	deskFP  string
}

func mustKey(t *testing.T) *ecdh.PrivateKey {
	t.Helper()
	k, err := e2e.GenerateStaticKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func mustPub(t *testing.T, raw []byte) *ecdh.PublicKey {
	t.Helper()
	p, err := ecdh.P256().NewPublicKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func mustVerifiedPath(t *testing.T) string {
	t.Helper()
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, verifiedFile)
}

func setDevices(f *e2eFakeRelay, d ...Device) {
	f.mu.Lock()
	f.devices = d
	f.mu.Unlock()
}

func newVerifyFixture(t *testing.T) *verifyFixture {
	t.Helper()
	isolate(t)
	usualPriv, deskPriv := mustKey(t), mustKey(t)
	relay := newE2EFakeRelay(t, Device{
		ID: "d1", Name: "phone",
		PublicKey:     e2e.EncodePublicKey(usualPriv.PublicKey()),
		DeskPublicKey: e2e.EncodePublicKey(deskPriv.PublicKey()),
	})
	m := testManager(relay.URL)
	_, hostPub, err := m.hostKeyBytes()
	if err != nil {
		t.Fatal(err)
	}
	return &verifyFixture{
		m: m, relay: relay,
		usual:   usualPriv.PublicKey().Bytes(),
		desk:    deskPriv.PublicKey().Bytes(),
		usualFP: e2e.Fingerprint(usualPriv.PublicKey(), hostPub),
		deskFP:  e2e.Fingerprint(deskPriv.PublicKey(), hostPub),
	}
}

func TestNothingIsVerifiedByDefault(t *testing.T) {
	f := newVerifyFixture(t)
	for _, o := range []KeyOrigin{KeyOriginUsual, KeyOriginDesk} {
		if f.m.DeviceVerified("d1", o, f.usual) || f.m.DeviceVerified("d1", o, f.desk) {
			t.Errorf("origin %d verified with no record", o)
		}
	}
}

func TestVerifyDeviceBindsDeviceOriginAndKey(t *testing.T) {
	f := newVerifyFixture(t)
	if err := f.m.VerifyDevice(context.Background(), "d1", KeyOriginDesk, f.deskFP); err != nil {
		t.Fatal(err)
	}
	if !f.m.DeviceVerified("d1", KeyOriginDesk, f.desk) {
		t.Fatal("not verified after the right code")
	}
	if f.m.DeviceVerified("d1", KeyOriginUsual, f.desk) {
		t.Error("verifying the desk origin verified the usual origin")
	}
	if f.m.DeviceVerified("d1", KeyOriginUsual, f.usual) {
		t.Error("verifying the desk key verified the usual key")
	}
	if f.m.DeviceVerified("d2", KeyOriginDesk, f.desk) {
		t.Error("verified for another device id")
	}
	if f.m.DeviceVerified("d1", KeyOriginDesk, f.usual) {
		t.Error("verified for another key")
	}
	if f.m.DeviceVerified("d1", KeyOriginDesk, nil) || f.m.DeviceVerified("d1", KeyOriginDesk, []byte{}) {
		t.Error("verified an empty key")
	}
	if f.m.DeviceVerified("", KeyOriginDesk, f.desk) {
		t.Error("verified an empty device id")
	}
	if f.m.DeviceVerified("d1", KeyOrigin(7), f.desk) {
		t.Error("verified an origin that does not exist")
	}
	if f.m.DeviceVerified("d1", KeyOriginDesk, f.desk[:len(f.desk)-1]) ||
		f.m.DeviceVerified("d1", KeyOriginDesk, append(append([]byte{}, f.desk...), 0)) {
		t.Error("verified a key that is not the recorded one")
	}
}

func TestVerifyDeviceRefusesAWrongOrEmptyCode(t *testing.T) {
	f := newVerifyFixture(t)
	for _, code := range []string{"", "   ", "00000 00000 00000 00000 00000 00000", f.usualFP, "abc", f.deskFP + " 1"} {
		err := f.m.VerifyDevice(context.Background(), "d1", KeyOriginDesk, code)
		if !errors.Is(err, ErrFingerprintMismatch) {
			t.Errorf("code %q: err = %v, want a mismatch", code, err)
		}
	}
	if f.m.DeviceVerified("d1", KeyOriginDesk, f.desk) {
		t.Error("a refused code left the device verified")
	}
	if _, err := os.Stat(mustVerifiedPath(t)); err == nil {
		t.Error("a refused code wrote the record file")
	}
}

func TestVerifyDeviceAcceptsTheCodeAsTyped(t *testing.T) {
	f := newVerifyFixture(t)
	loose := strings.ReplaceAll(f.deskFP, " ", "-")
	if err := f.m.VerifyDevice(context.Background(), "d1", KeyOriginDesk, "  "+loose+"\n"); err != nil {
		t.Fatalf("code with other separators refused: %v", err)
	}
}

func TestVerifyDeviceFailsForAnUnknownDeviceOrMissingKey(t *testing.T) {
	f := newVerifyFixture(t)
	ctx := context.Background()
	if err := f.m.VerifyDevice(ctx, "nope", KeyOriginDesk, f.deskFP); err == nil {
		t.Error("verified a device the relay does not list")
	}
	setDevices(f.relay, Device{ID: "d1", PublicKey: e2e.EncodePublicKey(mustKey(t).PublicKey())})
	if err := f.m.VerifyDevice(ctx, "d1", KeyOriginDesk, f.deskFP); err == nil {
		t.Error("verified an origin the device has no key for")
	}
	setDevices(f.relay, Device{ID: "d1", DeskPublicKey: "not-a-key"})
	if err := f.m.VerifyDevice(ctx, "d1", KeyOriginDesk, f.deskFP); err == nil {
		t.Error("verified a malformed key")
	}
}

// The window shows a code, the key changes, then the button is pressed: the
// code the person compared is for a key that is gone.
func TestVerifyDeviceRefusesACodeForAKeyThatMovedSinceItWasShown(t *testing.T) {
	f := newVerifyFixture(t)
	other := mustKey(t)
	setDevices(f.relay, Device{ID: "d1", DeskPublicKey: e2e.EncodePublicKey(other.PublicKey())})
	if err := f.m.VerifyDevice(context.Background(), "d1", KeyOriginDesk, f.deskFP); !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("err = %v, want a mismatch", err)
	}
	if f.m.DeviceVerified("d1", KeyOriginDesk, other.PublicKey().Bytes()) ||
		f.m.DeviceVerified("d1", KeyOriginDesk, f.desk) {
		t.Error("a stale code verified a key")
	}
}

func TestAChangedKeyUnverifies(t *testing.T) {
	f := newVerifyFixture(t)
	ctx := context.Background()
	if err := f.m.VerifyDevice(ctx, "d1", KeyOriginDesk, f.deskFP); err != nil {
		t.Fatal(err)
	}
	swapped := mustKey(t).PublicKey()
	if f.m.DeviceVerified("d1", KeyOriginDesk, swapped.Bytes()) {
		t.Fatal("a swapped key is verified")
	}
	if got := f.m.DeviceVerifyState("d1", KeyOriginDesk, swapped.Bytes()); got != KeyChanged {
		t.Errorf("state = %v, want KeyChanged", got)
	}
	// Verifying again is a new comparison, and replaces the record.
	setDevices(f.relay, Device{ID: "d1", DeskPublicKey: e2e.EncodePublicKey(swapped)})
	_, hostPub, _ := f.m.hostKeyBytes()
	if err := f.m.VerifyDevice(ctx, "d1", KeyOriginDesk, e2e.Fingerprint(swapped, hostPub)); err != nil {
		t.Fatal(err)
	}
	if !f.m.DeviceVerified("d1", KeyOriginDesk, swapped.Bytes()) || f.m.DeviceVerified("d1", KeyOriginDesk, f.desk) {
		t.Error("the second verification did not replace the first")
	}
}

func TestAChangedHostKeyUnverifies(t *testing.T) {
	f := newVerifyFixture(t)
	if err := f.m.VerifyDevice(context.Background(), "d1", KeyOriginUsual, f.usualFP); err != nil {
		t.Fatal(err)
	}
	// A new machine identity (the key file was lost or replaced).
	if err := saveE2EIdentity(mustKey(t)); err != nil {
		t.Fatal(err)
	}
	m2 := testManager(f.relay.URL)
	if m2.DeviceVerified("d1", KeyOriginUsual, f.usual) {
		t.Fatal("still verified after this machine's own key changed")
	}
	if got := m2.DeviceVerifyState("d1", KeyOriginUsual, f.usual); got != KeyChanged {
		t.Errorf("state = %v, want KeyChanged", got)
	}
}

func TestVerificationSurvivesARestart(t *testing.T) {
	f := newVerifyFixture(t)
	if err := f.m.VerifyDevice(context.Background(), "d1", KeyOriginDesk, f.deskFP); err != nil {
		t.Fatal(err)
	}
	m2 := testManager(f.relay.URL)
	if !m2.DeviceVerified("d1", KeyOriginDesk, f.desk) {
		t.Error("a fresh manager does not see the record")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(mustVerifiedPath(t))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("record file mode = %v, want 0600", info.Mode().Perm())
		}
	}
}

func TestUnreadableRecordsMeanNothingIsVerified(t *testing.T) {
	for name, content := range map[string]string{
		"garbage":       "not json",
		"empty":         "",
		"wrong version": `{"v":2,"records":[{"device":"d1","origin":"desk","deviceKey":"AA","hostKey":"AA"}]}`,
		"bad base64":    `{"v":1,"records":[{"device":"d1","origin":"desk","deviceKey":"!!","hostKey":"!!"}]}`,
		"empty keys":    `{"v":1,"records":[{"device":"d1","origin":"desk","deviceKey":"","hostKey":""}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newVerifyFixture(t)
			p := mustVerifiedPath(t)
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if f.m.DeviceVerified("d1", KeyOriginDesk, f.desk) || f.m.DeviceVerified("d1", KeyOriginDesk, []byte{0}) {
				t.Error("a damaged record verified a device")
			}
			// The next real verification still works over it.
			if err := f.m.VerifyDevice(context.Background(), "d1", KeyOriginDesk, f.deskFP); err != nil {
				t.Fatal(err)
			}
			if !f.m.DeviceVerified("d1", KeyOriginDesk, f.desk) {
				t.Error("could not verify after a damaged file")
			}
		})
	}
}

func TestUnverifyAndForget(t *testing.T) {
	f := newVerifyFixture(t)
	ctx := context.Background()
	if err := f.m.VerifyDevice(ctx, "d1", KeyOriginDesk, f.deskFP); err != nil {
		t.Fatal(err)
	}
	if err := f.m.VerifyDevice(ctx, "d1", KeyOriginUsual, f.usualFP); err != nil {
		t.Fatal(err)
	}
	if err := f.m.UnverifyDevice("d1", KeyOriginDesk); err != nil {
		t.Fatal(err)
	}
	if f.m.DeviceVerified("d1", KeyOriginDesk, f.desk) || !f.m.DeviceVerified("d1", KeyOriginUsual, f.usual) {
		t.Error("unverify removed the wrong record")
	}
	if err := f.m.ForgetDevice("d1"); err != nil {
		t.Fatal(err)
	}
	if f.m.DeviceVerified("d1", KeyOriginUsual, f.usual) {
		t.Error("forget left a record behind")
	}
	if testManager(f.relay.URL).DeviceVerified("d1", KeyOriginUsual, f.usual) {
		t.Error("the removal was not saved")
	}
}

// E2EVerifiedRespond checks the key it then handshakes against.
func TestE2EVerifiedRespond(t *testing.T) {
	isolate(t)
	devicePriv := mustKey(t)
	relay := newE2EFakeRelay(t, Device{ID: "d1", DeskPublicKey: e2e.EncodePublicKey(devicePriv.PublicKey())})
	m := testManager(relay.URL)
	hostPriv, err := m.hostE2EIdentity()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, hello, err := e2e.StartDeviceHandshake(devicePriv, hostPriv.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if sess, resp, err := m.E2EVerifiedRespond(ctx, "d1", KeyOriginDesk, hello); !errors.Is(err, ErrNotVerified) || sess != nil || resp != nil {
		t.Fatalf("unverified device: %v %v %v, want ErrNotVerified and no response", sess, resp, err)
	}
	fp := e2e.Fingerprint(devicePriv.PublicKey(), hostPriv.PublicKey())
	if err := m.VerifyDevice(ctx, "d1", KeyOriginDesk, fp); err != nil {
		t.Fatal(err)
	}
	dh, hello, err := e2e.StartDeviceHandshake(devicePriv, hostPriv.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	hostSess, resp, err := m.E2EVerifiedRespond(ctx, "d1", KeyOriginDesk, hello)
	if err != nil {
		t.Fatalf("verified device refused: %v", err)
	}
	devSess, err := dh.Finish(resp)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := hostSess.Open(devSess.Seal([]byte("x"))); err != nil || string(got) != "x" {
		t.Fatalf("session does not work: %q %v", got, err)
	}
	// The usual origin has no key at all.
	if _, _, err := m.E2EVerifiedRespond(ctx, "d1", KeyOriginUsual, hello); err == nil {
		t.Error("answered a handshake for an origin with no key")
	}

	// The relay swaps the key; the cache is expired so it is read again.
	old := e2eRosterTTL
	e2eRosterTTL = 0
	defer func() { e2eRosterTTL = old }()
	swapped := mustKey(t)
	setDevices(relay, Device{ID: "d1", DeskPublicKey: e2e.EncodePublicKey(swapped.PublicKey())})
	_, hello2, err := e2e.StartDeviceHandshake(swapped, hostPriv.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if sess, _, err := m.E2EVerifiedRespond(ctx, "d1", KeyOriginDesk, hello2); !errors.Is(err, ErrNotVerified) || sess != nil {
		t.Fatalf("a swapped key was served: %v %v", sess, err)
	}
	// The plain responder is unchanged: only the verified one gates.
	if _, _, err := m.E2ERespond(ctx, "d1", KeyOriginDesk, hello2); err != nil {
		t.Errorf("E2ERespond changed behaviour: %v", err)
	}
}

func TestParseKeyOrigin(t *testing.T) {
	for in, want := range map[string]struct {
		o  KeyOrigin
		ok bool
	}{"usual": {KeyOriginUsual, true}, "desk": {KeyOriginDesk, true}, "": {0, false}, "Desk": {0, false}, "other": {0, false}} {
		if o, ok := ParseKeyOrigin(in); o != want.o || ok != want.ok {
			t.Errorf("ParseKeyOrigin(%q) = %v, %v", in, o, ok)
		}
	}
}

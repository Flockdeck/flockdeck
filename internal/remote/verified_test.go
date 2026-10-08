package remote

import (
	"context"
	"crypto/ecdh"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

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

// A file that does not hold the records in the form this code writes
// verifies nothing. Each case starts from a real record, written by a
// verification and known to verify, and breaks one thing, so a guard that
// stops working shows as a device verifying.
func TestUnreadableRecordsMeanNothingIsVerified(t *testing.T) {
	type doc = map[string]any
	// edit changes the decoded file; raw replaces the file's bytes outright.
	for name, c := range map[string]struct {
		edit func(d doc)
		raw  func(good []byte) []byte
	}{
		"garbage":     {raw: func(good []byte) []byte { return append(append([]byte{}, good...), "}{ garbage"...) }},
		"truncated":   {raw: func(good []byte) []byte { return good[:len(good)/2] }},
		"empty":       {raw: func([]byte) []byte { return nil }},
		"not json":    {raw: func([]byte) []byte { return []byte("not json") }},
		"version two": {edit: func(d doc) { d["v"] = 2 }},
		"no version":  {edit: func(d doc) { delete(d, "v") }},
		"too many records": {edit: func(d doc) {
			recs := d["records"].([]any)
			for i := 0; i < maxVerifiedRecords; i++ {
				recs = append(recs, doc{"device": "other" + strconv.Itoa(i), "origin": "desk", "deviceKey": "AA", "hostKey": "AA"})
			}
			d["records"] = recs
		}},
		"bad base64 device key": {edit: func(d doc) { d["records"].([]any)[0].(doc)["deviceKey"] = "!!" }},
		"bad base64 host key":   {edit: func(d doc) { d["records"].([]any)[0].(doc)["hostKey"] = "!!" }},
		"empty keys": {edit: func(d doc) {
			r := d["records"].([]any)[0].(doc)
			r["deviceKey"], r["hostKey"] = "", ""
		}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newVerifyFixture(t)
			if err := f.m.VerifyDevice(context.Background(), "d1", KeyOriginDesk, f.deskFP); err != nil {
				t.Fatal(err)
			}
			p := mustVerifiedPath(t)
			good, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			// The unbroken file verifies, so the cases below fail for the
			// reason they say.
			if !testManager(f.relay.URL).DeviceVerified("d1", KeyOriginDesk, f.desk) {
				t.Fatal("the record as written does not verify")
			}
			data := good
			if c.edit != nil {
				var d doc
				if err := json.Unmarshal(good, &d); err != nil {
					t.Fatal(err)
				}
				// The decoded records are map[string]any; give the edits the
				// shape they index.
				recs := d["records"].([]any)
				for i, r := range recs {
					recs[i] = doc(r.(map[string]any))
				}
				c.edit(d)
				if data, err = json.Marshal(d); err != nil {
					t.Fatal(err)
				}
			} else {
				data = c.raw(good)
			}
			if err := os.WriteFile(p, data, 0o600); err != nil {
				t.Fatal(err)
			}
			m2 := testManager(f.relay.URL)
			if m2.DeviceVerified("d1", KeyOriginDesk, f.desk) {
				t.Error("a damaged record verified a device")
			}
			// The next real verification still works over it.
			if err := m2.VerifyDevice(context.Background(), "d1", KeyOriginDesk, f.deskFP); err != nil {
				t.Fatal(err)
			}
			if !m2.DeviceVerified("d1", KeyOriginDesk, f.desk) {
				t.Error("could not verify after a damaged file")
			}
		})
	}
}

// Two Flockdeck processes can share a state directory. What one records the
// other sees without restarting, and removing it is seen too.
func TestAnotherInstanceSeesAChangeToTheRecord(t *testing.T) {
	f := newVerifyFixture(t)
	m2 := testManager(f.relay.URL)
	ctx := context.Background()
	// m2 reads the (absent) file first, so it holds a stale copy.
	if m2.DeviceVerified("d1", KeyOriginDesk, f.desk) {
		t.Fatal("verified with no record")
	}
	if err := f.m.VerifyDevice(ctx, "d1", KeyOriginDesk, f.deskFP); err != nil {
		t.Fatal(err)
	}
	if !m2.DeviceVerified("d1", KeyOriginDesk, f.desk) {
		t.Error("a second instance does not see a verification made by the first")
	}
	if err := f.m.UnverifyDevice("d1", KeyOriginDesk); err != nil {
		t.Fatal(err)
	}
	if m2.DeviceVerified("d1", KeyOriginDesk, f.desk) {
		t.Error("a second instance still trusts a verification the first removed")
	}
	// The file deleted outright is seen too.
	if err := f.m.VerifyDevice(ctx, "d1", KeyOriginDesk, f.deskFP); err != nil {
		t.Fatal(err)
	}
	if !m2.DeviceVerified("d1", KeyOriginDesk, f.desk) {
		t.Fatal("not seen")
	}
	if err := os.Remove(mustVerifiedPath(t)); err != nil {
		t.Fatal(err)
	}
	if m2.DeviceVerified("d1", KeyOriginDesk, f.desk) {
		t.Error("a second instance still trusts a record file that is gone")
	}
}

// A rewrite that leaves the file the same size is still noticed when the
// modification time moves.
func TestARewrittenRecordOfTheSameSizeIsReread(t *testing.T) {
	f := newVerifyFixture(t)
	if err := f.m.VerifyDevice(context.Background(), "d1", KeyOriginDesk, f.deskFP); err != nil {
		t.Fatal(err)
	}
	if !f.m.DeviceVerified("d1", KeyOriginDesk, f.desk) {
		t.Fatal("not verified")
	}
	p := mustVerifiedPath(t)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// Same length: the device id d1 becomes d2.
	changed := strings.Replace(string(data), `"d1"`, `"d2"`, 1)
	if len(changed) != len(data) || changed == string(data) {
		t.Fatal("test file edit is wrong")
	}
	if err := os.WriteFile(p, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	if f.m.DeviceVerified("d1", KeyOriginDesk, f.desk) {
		t.Error("a rewritten record file was not read again")
	}
}

// A change made by one instance does not undo one made by another: each
// reads the file before it changes it.
func TestTwoInstancesDoNotOverwriteEachOther(t *testing.T) {
	f := newVerifyFixture(t)
	m2 := testManager(f.relay.URL)
	ctx := context.Background()
	// Both have read the empty file.
	f.m.DeviceVerified("d1", KeyOriginDesk, f.desk)
	m2.DeviceVerified("d1", KeyOriginDesk, f.desk)
	if err := f.m.VerifyDevice(ctx, "d1", KeyOriginDesk, f.deskFP); err != nil {
		t.Fatal(err)
	}
	if err := m2.VerifyDevice(ctx, "d1", KeyOriginUsual, f.usualFP); err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]*Manager{"first": f.m, "second": m2, "fresh": testManager(f.relay.URL)} {
		if !m.DeviceVerified("d1", KeyOriginDesk, f.desk) || !m.DeviceVerified("d1", KeyOriginUsual, f.usual) {
			t.Errorf("the %s instance lost a verification: desk %v, usual %v", name,
				m.DeviceVerified("d1", KeyOriginDesk, f.desk), m.DeviceVerified("d1", KeyOriginUsual, f.usual))
		}
	}
	// A stale instance that forgets a device does not bring back what
	// another removed, nor drop what another added.
	m3 := testManager(f.relay.URL)
	m3.DeviceVerified("d1", KeyOriginDesk, f.desk)
	if err := f.m.UnverifyDevice("d1", KeyOriginDesk); err != nil {
		t.Fatal(err)
	}
	if err := m3.UnverifyDevice("d1", KeyOriginUsual); err != nil {
		t.Fatal(err)
	}
	if f.m.DeviceVerified("d1", KeyOriginDesk, f.desk) || f.m.DeviceVerified("d1", KeyOriginUsual, f.usual) {
		t.Error("an instance's removal was undone by another's stale copy")
	}
}

// A device that leaves the roster loses its verification, and one that
// returns with the same id and key is not verified until compared again.
func TestADeviceThatLeavesTheRosterIsNoLongerVerified(t *testing.T) {
	f := newVerifyFixture(t)
	ctx := context.Background()
	d1 := Device{
		ID: "d1", PublicKey: e2e.EncodePublicKey(mustPub(t, f.usual)),
		DeskPublicKey: e2e.EncodePublicKey(mustPub(t, f.desk)),
	}
	otherPriv := mustKey(t)
	d2 := Device{ID: "d2", DeskPublicKey: e2e.EncodePublicKey(otherPriv.PublicKey())}
	setDevices(f.relay, d1, d2)
	_, hostPub, _ := f.m.hostKeyBytes()
	if err := f.m.VerifyDevice(ctx, "d1", KeyOriginDesk, f.deskFP); err != nil {
		t.Fatal(err)
	}
	if err := f.m.VerifyDevice(ctx, "d2", KeyOriginDesk, e2e.Fingerprint(otherPriv.PublicKey(), hostPub)); err != nil {
		t.Fatal(err)
	}

	// d1 is unpaired from its own page: the next roster lacks it.
	setDevices(f.relay, d2)
	if got := f.m.DeviceFingerprint(ctx, "d2", KeyOriginDesk); got == "" {
		t.Fatal("no fingerprint for the device that is still paired")
	}
	if f.m.DeviceVerified("d1", KeyOriginDesk, f.desk) {
		t.Error("a device missing from the roster is still verified")
	}
	if !f.m.DeviceVerified("d2", KeyOriginDesk, otherPriv.PublicKey().Bytes()) {
		t.Error("a device still on the roster lost its verification")
	}

	// The same id and key pair again.
	setDevices(f.relay, d1, d2)
	if f.m.DeviceVerified("d1", KeyOriginDesk, f.desk) {
		t.Error("a re-paired device came back verified without a new comparison")
	}
	if got := f.m.DeviceVerifyState("d1", KeyOriginDesk, f.desk); got != NotVerified {
		t.Errorf("state = %v, want NotVerified", got)
	}
	if err := f.m.VerifyDevice(ctx, "d1", KeyOriginDesk, f.deskFP); err != nil {
		t.Fatal(err)
	}
	if !f.m.DeviceVerified("d1", KeyOriginDesk, f.desk) {
		t.Error("could not verify the re-paired device")
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

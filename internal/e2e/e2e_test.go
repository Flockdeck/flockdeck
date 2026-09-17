package e2e_test

// Ported from flockdeck-relay's internal/e2e/e2e_test.go, which exercises
// the reference implementation this package is a copy of (see e2e.go's
// package doc, "Why this is a copy, not an import"). Kept in step by hand,
// the same as the code it tests.

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"testing"

	"github.com/jmwri/flockdeck/internal/e2e"
)

// handshake runs a full device/host handshake between two independently
// generated key pairs, as a browser and a desktop each run their own half
// of, and returns the two sessions it leaves them with.
func handshake(t *testing.T) (device, host *e2e.Session) {
	t.Helper()
	devicePriv, err := e2e.GenerateStaticKey()
	if err != nil {
		t.Fatal(err)
	}
	hostPriv, err := e2e.GenerateStaticKey()
	if err != nil {
		t.Fatal(err)
	}
	dh, hello, err := e2e.StartDeviceHandshake(devicePriv, hostPriv.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	hostSession, response, err := e2e.RespondHostHandshake(hostPriv, devicePriv.PublicKey(), hello)
	if err != nil {
		t.Fatal(err)
	}
	deviceSession, err := dh.Finish(response)
	if err != nil {
		t.Fatal(err)
	}
	return deviceSession, hostSession
}

// A device and a host that ran the same handshake reach the same session,
// each able to read what the other sealed, in both directions.
func TestHandshakeAgreesOnASession(t *testing.T) {
	device, host := handshake(t)

	sealed := device.Seal([]byte("ls -la\n"))
	got, err := host.Open(sealed)
	if err != nil {
		t.Fatalf("host opening what the device sealed: %v", err)
	}
	if string(got) != "ls -la\n" {
		t.Errorf("host read %q, want %q", got, "ls -la\n")
	}

	sealed = host.Seal([]byte("total 12\ndrwxr-xr-x ...\n"))
	got, err = device.Open(sealed)
	if err != nil {
		t.Fatalf("device opening what the host sealed: %v", err)
	}
	if string(got) != "total 12\ndrwxr-xr-x ...\n" {
		t.Errorf("device read %q", got)
	}
}

// A relay carrying the handshake and every frame after it -- which is all
// it is ever handed -- is given nothing it can decrypt: the plaintext
// itself never appears in what crosses the wire.
func TestSealedFramesDoNotContainThePlaintext(t *testing.T) {
	device, _ := handshake(t)
	plain := []byte("super-secret-api-key=sk-abcdef123456")
	sealed := device.Seal(plain)
	if bytes.Contains(sealed, plain) {
		t.Fatal("the sealed frame contains the plaintext")
	}
}

// Many frames in a row, both directions interleaved, each read back exactly
// once and in order -- the shape a real terminal socket's traffic actually
// has.
func TestManyFramesInOrder(t *testing.T) {
	device, host := handshake(t)
	for i := 0; i < 500; i++ {
		msg := []byte{byte(i), byte(i >> 8), 'x'}
		if got, err := host.Open(device.Seal(msg)); err != nil || !bytes.Equal(got, msg) {
			t.Fatalf("frame %d device->host: %q, %v", i, got, err)
		}
		if got, err := device.Open(host.Seal(msg)); err != nil || !bytes.Equal(got, msg) {
			t.Fatalf("frame %d host->device: %q, %v", i, got, err)
		}
	}
}

// Two independent handshakes -- two different terminal sockets -- never
// produce sessions that can read each other's frames, even though both ran
// between the very same two static keys: each session's ephemeral keys make
// it its own.
func TestTwoSessionsBetweenTheSameKeysDoNotCrossRead(t *testing.T) {
	devicePriv, _ := e2e.GenerateStaticKey()
	hostPriv, _ := e2e.GenerateStaticKey()

	dh1, hello1, _ := e2e.StartDeviceHandshake(devicePriv, hostPriv.PublicKey())
	host1, resp1, _ := e2e.RespondHostHandshake(hostPriv, devicePriv.PublicKey(), hello1)
	device1, _ := dh1.Finish(resp1)

	dh2, hello2, _ := e2e.StartDeviceHandshake(devicePriv, hostPriv.PublicKey())
	host2, resp2, _ := e2e.RespondHostHandshake(hostPriv, devicePriv.PublicKey(), hello2)
	device2, _ := dh2.Finish(resp2)

	// Each session still works on its own.
	if _, err := host1.Open(device1.Seal([]byte("session one"))); err != nil {
		t.Errorf("session one, on its own: %v", err)
	}
	if _, err := host2.Open(device2.Seal([]byte("session two"))); err != nil {
		t.Errorf("session two, on its own: %v", err)
	}
	// But not crossed: the first session's frame means nothing to the
	// second, and the second's means nothing to the first.
	if _, err := host2.Open(device1.Seal([]byte("session one, frame two"))); err == nil {
		t.Error("the second session opened the first session's frame")
	}
	if _, err := host1.Open(device2.Seal([]byte("session two, frame two"))); err == nil {
		t.Error("the first session opened the second session's frame")
	}
}

// A relay that flips a bit anywhere in a frame -- the version, the counter,
// the ciphertext or the tag -- is refused, never silently misread.
func TestATamperedFrameIsRefused(t *testing.T) {
	device, host := handshake(t)
	base := device.Seal([]byte("rm -rf /"))
	for i := range base {
		frame := append([]byte(nil), base...)
		frame[i] ^= 0x01
		if _, err := host.Open(frame); err == nil {
			t.Fatalf("byte %d flipped: opened without error", i)
		}
	}
}

// A frame replayed -- the exact bytes the relay saw once already -- is
// refused the second time: Open's counter check is strict, not a sliding
// window, since the socket it runs over never legitimately reorders or
// duplicates a frame.
func TestAReplayedFrameIsRefused(t *testing.T) {
	device, host := handshake(t)
	sealed := device.Seal([]byte("whoami"))
	if _, err := host.Open(sealed); err != nil {
		t.Fatalf("first read: %v", err)
	}
	if _, err := host.Open(sealed); err == nil {
		t.Fatal("the same frame opened twice")
	}
}

// A frame out of order -- the second one delivered before the first -- is
// refused for the same reason a replay is: on the ordered socket this
// scheme assumes, it can only mean something has gone wrong between the two
// static keys' owners and whatever is now on the wire.
func TestAnOutOfOrderFrameIsRefused(t *testing.T) {
	device, host := handshake(t)
	first := device.Seal([]byte("one"))
	second := device.Seal([]byte("two"))
	if _, err := host.Open(second); err == nil {
		t.Fatal("the second frame opened before the first")
	}
	if _, err := host.Open(first); err != nil {
		t.Fatalf("the first frame, still next in line: %v", err)
	}
}

// A device that hands the host's response to the wrong host's handshake --
// impersonation, or simply the wrong pairing -- ends up unable to read what
// that host seals: the sessions the two independently derive do not match.
func TestAWrongHostDoesNotShareTheSession(t *testing.T) {
	devicePriv, _ := e2e.GenerateStaticKey()
	realHostPriv, _ := e2e.GenerateStaticKey()
	impostorHostPriv, _ := e2e.GenerateStaticKey()

	dh, hello, err := e2e.StartDeviceHandshake(devicePriv, realHostPriv.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	// The impostor answers as though it were the real host, but the device
	// started its handshake naming the real host's static key, not the
	// impostor's: the two will derive different T1 and T2 terms.
	impostorSession, response, err := e2e.RespondHostHandshake(impostorHostPriv, devicePriv.PublicKey(), hello)
	if err != nil {
		t.Fatal(err)
	}
	deviceSession, err := dh.Finish(response)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := impostorSession.Open(deviceSession.Seal([]byte("hello"))); err == nil {
		t.Fatal("an impostor host shared the device's session")
	}
}

// A device whose static key the host does not actually have on file -- an
// unrecognised or revoked device presenting someone else's hello -- likewise
// ends up with no shared session, which is what stops a relay-forwarded
// hello from some other device pairing as this one.
func TestAWrongDeviceDoesNotShareTheSession(t *testing.T) {
	realDevicePriv, _ := e2e.GenerateStaticKey()
	impostorDevicePriv, _ := e2e.GenerateStaticKey()
	hostPriv, _ := e2e.GenerateStaticKey()

	dh, hello, err := e2e.StartDeviceHandshake(impostorDevicePriv, hostPriv.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	// The host looks the sender up as the real device (its own book-keeping
	// named this socket that device's, say by a stale or spoofed id) and
	// uses that device's static key, which does not match the impostor's.
	hostSession, response, err := e2e.RespondHostHandshake(hostPriv, realDevicePriv.PublicKey(), hello)
	if err != nil {
		t.Fatal(err)
	}
	deviceSession, err := dh.Finish(response)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hostSession.Open(deviceSession.Seal([]byte("hello"))); err == nil {
		t.Fatal("a host using the wrong device key shared a session with an impostor")
	}
}

// A public key round-trips through the same encoding the relay stores it
// and hands it back in.
func TestPublicKeyRoundTrip(t *testing.T) {
	priv, err := e2e.GenerateStaticKey()
	if err != nil {
		t.Fatal(err)
	}
	encoded := e2e.EncodePublicKey(priv.PublicKey())
	back, err := e2e.DecodePublicKey(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back.Bytes(), priv.PublicKey().Bytes()) {
		t.Error("the key did not round-trip")
	}
}

// What the relay itself does with a key it is asked to store: refuse
// anything that is not base64url, or that is but does not land on the curve.
func TestDecodePublicKeyRefusesNonsense(t *testing.T) {
	cases := []string{
		"not-base64url!!",
		"AAAA", // valid base64url, far too short to be a point
	}
	for _, c := range cases {
		if _, err := e2e.DecodePublicKey(c); err == nil {
			t.Errorf("DecodePublicKey(%q): no error", c)
		}
	}
	// Random bytes of the right length are still not a point on the curve
	// (astronomically unlikely to be one by chance).
	junk := make([]byte, 65)
	_, _ = rand.Read(junk)
	junk[0] = 4 // the uncompressed-point tag, so the length check alone does not reject it
	if _, err := e2e.DecodePublicKey(base64.RawURLEncoding.EncodeToString(junk)); err == nil {
		t.Error("65 random bytes decoded as a public key")
	}
}

// The two static keys a real handshake produced these vectors from: the
// same keys, and the same expected code, flockdeck-relay's own e2e_test.go
// and flockdeck-remote's e2e.js check their own fingerprints against. A
// mismatch here means this copy has drifted from the reference
// implementation, not that either is wrong on its own.
const (
	vecDeviceStaticPub = "0433d812e1276886a0d442f3b338620ed578a661f1f355c4d8206fdedca0743dadbf861f2d5cba92ba85bdf778ef8d829dd8c575a2647a92efb944fbd000c414ae"
	vecHostStaticPub   = "04a0e057bf1fc9ca714f0e25546cad4007c1044598b975e09c0727c629a3d746e1b74d0840b24dbedbe78b06fd6b42a6b62e93b9d6ce0b22901f262a1ea9854789"
	vecFingerprint     = "99506 91610 13506 43956 60391 19129"
)

func vecKey(t *testing.T, h string) *ecdh.PublicKey {
	t.Helper()
	raw, err := hex.DecodeString(h)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ecdh.P256().NewPublicKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

// Fingerprint reproduces the exact code flockdeck-relay's own e2e_test.go
// and flockdeck-remote's e2e.js compute from the same two keys -- proof
// this copy has not drifted from either.
func TestFingerprintMatchesTheReferenceVectorByteForByte(t *testing.T) {
	got := e2e.Fingerprint(vecKey(t, vecDeviceStaticPub), vecKey(t, vecHostStaticPub))
	if got != vecFingerprint {
		t.Errorf("Fingerprint() = %q, want %q", got, vecFingerprint)
	}
}

// Six groups of five digits, space-separated -- what a person is asked to
// read off one screen and compare against another, not any other encoding
// of the same bytes.
func TestFingerprintFormat(t *testing.T) {
	got := e2e.Fingerprint(vecKey(t, vecDeviceStaticPub), vecKey(t, vecHostStaticPub))
	if !regexp.MustCompile(`^\d{5}( \d{5}){5}$`).MatchString(got) {
		t.Errorf("Fingerprint() = %q, not six 5-digit groups", got)
	}
}

// Fingerprint depends on nothing but the two static keys: two independent
// handshakes between the same pair produce different sessions but the same
// fingerprint, which is what lets a person verify it once rather than
// before every terminal they open.
func TestFingerprintDoesNotDependOnTheHandshakeOrSession(t *testing.T) {
	devicePriv, _ := e2e.GenerateStaticKey()
	hostPriv, _ := e2e.GenerateStaticKey()
	want := e2e.Fingerprint(devicePriv.PublicKey(), hostPriv.PublicKey())

	for i := 0; i < 3; i++ {
		dh, hello, err := e2e.StartDeviceHandshake(devicePriv, hostPriv.PublicKey())
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := e2e.RespondHostHandshake(hostPriv, devicePriv.PublicKey(), hello); err != nil {
			t.Fatal(err)
		}
		_ = dh
		if got := e2e.Fingerprint(devicePriv.PublicKey(), hostPriv.PublicKey()); got != want {
			t.Fatalf("run %d: Fingerprint() = %q, want %q", i, got, want)
		}
	}
}

// A relay that swapped either static key -- the device's or the host's --
// leaves the two sides holding different keys for one of them, and that
// shows up as a different fingerprint: this is the check that catches it.
func TestFingerprintChangesWhenEitherKeyChanges(t *testing.T) {
	devicePriv, _ := e2e.GenerateStaticKey()
	hostPriv, _ := e2e.GenerateStaticKey()
	impostorPriv, _ := e2e.GenerateStaticKey()
	base := e2e.Fingerprint(devicePriv.PublicKey(), hostPriv.PublicKey())

	if got := e2e.Fingerprint(impostorPriv.PublicKey(), hostPriv.PublicKey()); got == base {
		t.Error("a substituted device key produced the same fingerprint")
	}
	if got := e2e.Fingerprint(devicePriv.PublicKey(), impostorPriv.PublicKey()); got == base {
		t.Error("a substituted host key produced the same fingerprint")
	}
}

// devicePub and hostPub are not interchangeable: swapping them is a
// different pair of arguments, and Fingerprint has no way to tell that
// apart from a genuinely different host, so it must not agree by accident.
// Both sides always call it the same way round (device first, host
// second), which is what makes this safe rather than a source of the very
// mismatches this feature exists to catch.
func TestFingerprintIsNotSymmetricInItsArguments(t *testing.T) {
	device := vecKey(t, vecDeviceStaticPub)
	host := vecKey(t, vecHostStaticPub)
	if e2e.Fingerprint(device, host) == e2e.Fingerprint(host, device) {
		t.Error("swapping devicePub and hostPub produced the same fingerprint")
	}
}

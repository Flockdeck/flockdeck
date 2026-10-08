package remote

import (
	"context"
	"crypto/ecdh"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/e2e"
	"github.com/jmwri/flockdeck/internal/store"
)

// This file records which devices the person at this machine has checked.
// A fingerprint (internal/e2e.Fingerprint) is only worth anything once
// someone has compared it by eye with what the device shows, and until now
// nothing remembered that they had. The record here is that memory: "the
// person at the desk compared the code for this device, from this origin,
// with this key".
//
// It is bound to all of: the device id, the origin (a device has a separate
// key for each, see KeyOrigin), the device's key and this machine's own key.
// Change any of them and the record no longer matches, so a relay that
// swaps a key after the check silently un-verifies the device. The record
// is never edited into agreement with a new key; verifying again is a new
// comparison by a person.
//
// The record lives in this machine's state directory, beside remote.json,
// and is only written by Manager.VerifyDevice, which the server only calls
// for a window on this machine (never one reached through the relay).
//
// What the record does not defend against: it is protected the way
// remote.json and e2e_key.json are, by a 0600 file in the state directory
// and nothing stronger. Flockdeck keeps no secret anywhere that a program
// running as the same user cannot read (keys.json is a plain file too), so a
// MAC over the records would be keyed with a key stored beside them and
// would add nothing. A program running as you can therefore write a record
// for a key it chose (both keys are public values it can read), or ask the
// local server to record one. The record stops a relay from swapping a key
// after a person checked it. It does not stop software on this machine that
// already acts as you.
//
// Two Flockdeck processes can share a state directory, so the file is
// re-read whenever it has changed (its modification time or size), before a
// check and before a change, rather than trusted from the first read.

// verifiedFile is where the records are kept, 0600 like its neighbours.
const verifiedFile = "remote_verified.json"

// maxVerifiedRecords bounds the file. A person compares codes by hand, so
// a real account never gets near it; the bound is for a damaged or
// hostile file.
const maxVerifiedRecords = 1000

// VerifyState is what the record says about one (device, origin, key).
type VerifyState int

const (
	// NotVerified: no one has verified this device from this origin, or the
	// key on offer cannot be compared (empty).
	NotVerified VerifyState = iota
	// Verified: a person compared the code for exactly this key.
	Verified
	// KeyChanged: a person verified this device from this origin before,
	// but for another key, or against another key of this machine's. It
	// counts as not verified; it is separate so the dialog can say a key
	// moved rather than that nothing was ever checked.
	KeyChanged
)

// ErrFingerprintMismatch is what VerifyDevice fails with when the code the
// person compared is not the code for the device's key as it is now.
var ErrFingerprintMismatch = errors.New("remote: the code no longer matches this device's key; look at the code again")

// verifiedRecord is one verification.
type verifiedRecord struct {
	Device string `json:"device"`
	Origin string `json:"origin"`
	// DeviceKey and HostKey are the raw P-256 points, base64url, as
	// internal/e2e.EncodePublicKey writes them.
	DeviceKey string    `json:"deviceKey"`
	HostKey   string    `json:"hostKey"`
	At        time.Time `json:"at"`
}

type verifiedDoc struct {
	V       int              `json:"v"`
	Records []verifiedRecord `json:"records"`
}

// verifiedStore is the records, as last read from the file. Unreadable
// records are treated as none: failing closed means every device shows as
// not verified, which costs a re-check and nothing else.
type verifiedStore struct {
	mu     sync.Mutex
	loaded bool
	sig    fileSig
	recs   []verifiedRecord
}

// fileSig is what a stat of the record file says, enough to notice that
// another Flockdeck process changed it.
type fileSig struct {
	exists bool
	mod    time.Time
	size   int64
}

func verifiedPath() (string, error) {
	dir, err := store.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, verifiedFile), nil
}

func statSig(p string) fileSig {
	fi, err := os.Stat(p)
	if err != nil {
		return fileSig{}
	}
	return fileSig{exists: true, mod: fi.ModTime(), size: fi.Size()}
}

// load makes v.recs what the file holds now, reading it again only if it is
// not loaded yet or its modification time or size moved since the last
// read. A stat is all a check that finds nothing changed costs. Callers hold
// v.mu.
func (v *verifiedStore) load() {
	p, err := verifiedPath()
	if err != nil {
		v.loaded, v.recs = false, nil
		return
	}
	// The stat comes before the read, so a write landing between the two
	// leaves the signature older than the data and is read again next time.
	sig := statSig(p)
	if v.loaded && sig == v.sig {
		return
	}
	v.loaded, v.sig, v.recs = true, sig, nil
	data, err := store.ReadState(p)
	if err != nil {
		// Absent or unreadable: nothing is verified.
		return
	}
	var doc verifiedDoc
	if json.Unmarshal(data, &doc) != nil || doc.V != 1 {
		return
	}
	if len(doc.Records) > maxVerifiedRecords {
		return
	}
	v.recs = doc.Records
}

// save writes recs, and forgets what was read so that the next load reads
// the file back. Callers hold v.mu.
func (v *verifiedStore) save(recs []verifiedRecord) error {
	p, err := verifiedPath()
	if err != nil {
		return err
	}
	if recs == nil {
		recs = []verifiedRecord{}
	}
	data, err := json.MarshalIndent(verifiedDoc{V: 1, Records: recs}, "", "  ")
	if err != nil {
		return err
	}
	v.loaded = false
	return store.WriteAtomic(p, data)
}

func originName(o KeyOrigin) string {
	switch o {
	case KeyOriginUsual:
		return "usual"
	case KeyOriginDesk:
		return "desk"
	}
	return ""
}

// ParseKeyOrigin reads the name a window sends for an origin: "usual" or
// "desk". Anything else is not an origin.
func ParseKeyOrigin(s string) (KeyOrigin, bool) {
	switch s {
	case "usual":
		return KeyOriginUsual, true
	case "desk":
		return KeyOriginDesk, true
	}
	return 0, false
}

func b64(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }

// hostKeyBytes is this machine's own long-term public key. It is the truth
// for the host half of every fingerprint: the relay's roster also lists a
// key for this machine, but a relay that swapped it would make the code on
// both screens agree on the swapped value.
func (m *Manager) hostKeyBytes() ([]byte, *ecdh.PublicKey, error) {
	priv, err := m.hostE2EIdentity()
	if err != nil {
		return nil, nil, err
	}
	pub := priv.PublicKey()
	return pub.Bytes(), pub, nil
}

// DeviceVerifyState is what the record says of deviceID's key for origin.
// key is the raw P-256 point (ecdh.PublicKey.Bytes()).
func (m *Manager) DeviceVerifyState(deviceID string, origin KeyOrigin, key []byte) VerifyState {
	name := originName(origin)
	if deviceID == "" || name == "" || len(key) == 0 {
		return NotVerified
	}
	host, _, err := m.hostKeyBytes()
	if err != nil {
		return NotVerified
	}
	m.verified.mu.Lock()
	defer m.verified.mu.Unlock()
	m.verified.load()
	for _, r := range m.verified.recs {
		if r.Device != deviceID || r.Origin != name {
			continue
		}
		dk, err1 := base64.RawURLEncoding.DecodeString(r.DeviceKey)
		hk, err2 := base64.RawURLEncoding.DecodeString(r.HostKey)
		if err1 != nil || err2 != nil || len(dk) == 0 || len(hk) == 0 {
			return KeyChanged
		}
		// Both comparisons always run, so a mismatch on the first does not
		// shorten the second.
		a := subtle.ConstantTimeCompare(dk, key)
		b := subtle.ConstantTimeCompare(hk, host)
		if a&b == 1 {
			return Verified
		}
		return KeyChanged
	}
	return NotVerified
}

// DeviceVerified reports whether the person at this machine verified exactly
// this device, origin and key. A key that has changed since, or that this
// machine's own key changed under, is not verified. key is the raw P-256
// point of the device's key for origin (ecdh.PublicKey.Bytes()).
//
// Anything that serves a device on the strength of a verification must ask
// this about the key it is about to use, and refuse when it is false.
func (m *Manager) DeviceVerified(deviceID string, origin KeyOrigin, key []byte) bool {
	return m.DeviceVerifyState(deviceID, origin, key) == Verified
}

// normaliseCode keeps the digits of a fingerprint, so spacing and case in
// what a window sends do not matter.
func normaliseCode(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// DeviceFingerprint is the code for deviceID's key for origin as the relay
// reports it now, against this machine's own key; "" if either is missing
// or malformed. It asks the relay afresh (never the cache), since the point
// is to show what a person is about to vouch for.
func (m *Manager) DeviceFingerprint(ctx context.Context, deviceID string, origin KeyOrigin) string {
	_, _, fp, _ := m.currentDeviceKey(ctx, deviceID, origin)
	return fp
}

// currentDeviceKey fetches the roster and returns deviceID's key for origin
// (raw), its encoded form and the fingerprint against this machine's key.
func (m *Manager) currentDeviceKey(ctx context.Context, deviceID string, origin KeyOrigin) (raw []byte, hostRaw []byte, fp string, err error) {
	if deviceID == "" || originName(origin) == "" {
		return nil, nil, "", errors.New("no such device or origin")
	}
	hostRaw, hostPub, err := m.hostKeyBytes()
	if err != nil {
		return nil, nil, "", err
	}
	cl, err := m.Client()
	if err != nil {
		return nil, nil, "", err
	}
	roster, err := cl.Devices(ctx)
	if err != nil {
		return nil, nil, "", err
	}
	ids := make([]string, len(roster.Devices))
	for i, d := range roster.Devices {
		ids[i] = d.ID
	}
	_ = m.ForgetDevicesNotIn(ids)
	for _, d := range roster.Devices {
		if d.ID != deviceID {
			continue
		}
		enc := origin.key(d)
		if enc == "" {
			return nil, nil, "", ErrNoE2EKey
		}
		pub, err := e2e.DecodePublicKey(enc)
		if err != nil {
			return nil, nil, "", err
		}
		return pub.Bytes(), hostRaw, e2e.Fingerprint(pub, hostPub), nil
	}
	return nil, nil, "", errors.New("no such device")
}

// VerifyDevice records that the person compared the code for deviceID's key
// for origin. claimed is the code they were shown. It is checked against
// the key the relay reports at this moment, so a key that changed between
// the code being shown and the button being pressed is refused with
// ErrFingerprintMismatch rather than verified by mistake.
//
// It must only be reached from a window on this machine: whether a person
// compared anything is the one fact a remote window cannot be trusted to
// say. internal/server refuses the command when it came through the relay.
func (m *Manager) VerifyDevice(ctx context.Context, deviceID string, origin KeyOrigin, claimed string) error {
	want := normaliseCode(claimed)
	if want == "" {
		return ErrFingerprintMismatch
	}
	raw, hostRaw, fp, err := m.currentDeviceKey(ctx, deviceID, origin)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(normaliseCode(fp)), []byte(want)) != 1 {
		return ErrFingerprintMismatch
	}
	rec := verifiedRecord{
		Device: deviceID, Origin: originName(origin),
		DeviceKey: b64(raw), HostKey: b64(hostRaw), At: time.Now().UTC(),
	}
	m.verified.mu.Lock()
	defer m.verified.mu.Unlock()
	m.verified.load()
	next := make([]verifiedRecord, 0, len(m.verified.recs)+1)
	for _, r := range m.verified.recs {
		if r.Device == rec.Device && r.Origin == rec.Origin {
			continue
		}
		next = append(next, r)
	}
	if len(next) >= maxVerifiedRecords {
		return errors.New("remote: too many verified devices on record")
	}
	next = append(next, rec)
	if err := m.verified.save(next); err != nil {
		return fmt.Errorf("remote: could not save the verification: %w", err)
	}
	return nil
}

// UnverifyDevice removes the record for deviceID and origin.
func (m *Manager) UnverifyDevice(deviceID string, origin KeyOrigin) error {
	name := originName(origin)
	if name == "" {
		return nil
	}
	return m.dropVerified(func(r verifiedRecord) bool { return r.Device == deviceID && r.Origin == name })
}

// ForgetDevice removes every record for deviceID, both origins: what
// unpairing a device does.
func (m *Manager) ForgetDevice(deviceID string) error {
	return m.dropVerified(func(r verifiedRecord) bool { return r.Device == deviceID })
}

// ForgetDevicesNotIn removes the records of every device not in ids, given
// the device ids of a roster the relay has just returned. A device unpaired
// from its own page leaves nothing on this machine to say so, and one that
// pairs again with the same id and key must not come back verified without
// someone comparing its code again. Call it only with a roster that was read
// successfully.
func (m *Manager) ForgetDevicesNotIn(ids []string) error {
	keep := make(map[string]bool, len(ids))
	for _, id := range ids {
		keep[id] = true
	}
	return m.dropVerified(func(r verifiedRecord) bool { return !keep[r.Device] })
}

func (m *Manager) dropVerified(drop func(verifiedRecord) bool) error {
	m.verified.mu.Lock()
	defer m.verified.mu.Unlock()
	m.verified.load()
	next := make([]verifiedRecord, 0, len(m.verified.recs))
	for _, r := range m.verified.recs {
		if !drop(r) {
			next = append(next, r)
		}
	}
	if len(next) == len(m.verified.recs) {
		return nil
	}
	if err := m.verified.save(next); err != nil {
		return err
	}
	return nil
}

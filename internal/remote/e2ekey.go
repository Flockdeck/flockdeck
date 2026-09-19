package remote

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/jmwri/flockdeck/internal/e2e"
	"github.com/jmwri/flockdeck/internal/store"
)

// This file is this machine's half of end-to-end encrypting the terminals it
// serves through the relay (see internal/e2e for the scheme, and
// internal/server/pty.go for where a terminal socket actually uses it): a
// long-term identity kept on disk, and registering its public half with the
// relay so a paired browser can find it.
//
// Everything here treats the relay as it already is elsewhere in this
// package -- reachable but not to be blocked on. A key that cannot be
// generated because the disk is unavailable, or registered because the relay
// is unreachable, is not a reason to refuse a terminal: it is a reason to
// serve that terminal unencrypted, which is exactly what pty.go does when
// E2ECapable says no.

// e2eKeyFile is where this machine's long-term end-to-end identity is kept,
// in the state directory beside remote.json and the local server's own
// token. Like those, it holds a secret -- the private half never leaves this
// machine -- so it is written 0600 the same way, through the same
// store.WriteAtomic and store.ReadState remote.json itself uses.
const e2eKeyFile = "e2e_key.json"

// e2eKeyDoc is e2eKeyFile's contents. Only the private key is kept: the
// public half is cheap to recompute from it, and keeping the encoding this
// package already has for it (internal/e2e.EncodePublicKey) rather than a
// second copy on disk is one less place for the two to disagree.
type e2eKeyDoc struct {
	// PrivateKey is the P-256 scalar (ecdh.PrivateKey.Bytes()), base64url.
	PrivateKey string `json:"privateKey"`
}

func e2eKeyPath() (string, error) {
	dir, err := store.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, e2eKeyFile), nil
}

// loadE2EIdentity returns this machine's saved identity, or nil if it has
// never made one.
func loadE2EIdentity() (*ecdh.PrivateKey, error) {
	p, err := e2eKeyPath()
	if err != nil {
		return nil, err
	}
	data, err := store.ReadState(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", e2eKeyFile, err)
	}
	var doc e2eKeyDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON (%v)", p, err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(doc.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("%s does not hold a valid key (%v)", p, err)
	}
	priv, err := ecdh.P256().NewPrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("%s does not hold a valid key (%v)", p, err)
	}
	return priv, nil
}

// saveE2EIdentity writes this machine's identity, readable by this user
// only.
func saveE2EIdentity(priv *ecdh.PrivateKey) error {
	p, err := e2eKeyPath()
	if err != nil {
		return err
	}
	doc := e2eKeyDoc{PrivateKey: base64.RawURLEncoding.EncodeToString(priv.Bytes())}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", e2eKeyFile, err)
	}
	if err := store.WriteAtomic(p, data); err != nil {
		return fmt.Errorf("write %s: %w", e2eKeyFile, err)
	}
	return nil
}

// ensureE2EIdentity loads this machine's identity, making and saving one the
// first time this is called on a machine that predates end-to-end
// encryption, or that has never had a terminal reached through the relay.
func ensureE2EIdentity() (*ecdh.PrivateKey, error) {
	priv, err := loadE2EIdentity()
	if err != nil {
		return nil, err
	}
	if priv != nil {
		return priv, nil
	}
	priv, err = e2e.GenerateStaticKey()
	if err != nil {
		return nil, err
	}
	if err := saveE2EIdentity(priv); err != nil {
		return nil, err
	}
	return priv, nil
}

// ErrNoE2EKey is what E2ERespond fails with, and what makes E2ECapable
// answer false, when either this host or the device asking for a terminal
// has no long-term end-to-end key registered with the relay: an older
// client that predates this feature, or a key rotation still in flight. The
// caller's answer is to serve the terminal unencrypted rather than refuse
// it -- see internal/server/pty.go's use of both.
var ErrNoE2EKey = errors.New("remote: no end-to-end key is registered for this host and device")

// e2eRosterTTL bounds how long a device's public key, once fetched, is
// trusted before asking the relay again. A terminal handshake needs the
// device's current key, but a window can open several panes at once, each
// its own tunnel stream and its own call to E2ECapable and E2ERespond, and
// asking the relay once for all of them is both faster and lighter on it
// than asking once per pane. It is a variable so a test does not have to
// wait it out.
var e2eRosterTTL = 10 * time.Second

// e2eRegisterTimeout bounds one attempt to register this host's key with the
// relay. It is a variable so a test does not have to sit through it.
var e2eRegisterTimeout = 20 * time.Second

// hostE2EIdentity is this machine's identity, made the first time it is
// asked for and cached after that: it never changes while this process
// runs, so there is nothing to gain by reading it from disk again.
func (m *Manager) hostE2EIdentity() (*ecdh.PrivateKey, error) {
	m.e2eMu.Lock()
	defer m.e2eMu.Unlock()
	if m.e2ePriv != nil {
		return m.e2ePriv, nil
	}
	priv, err := ensureE2EIdentity()
	if err != nil {
		return nil, err
	}
	m.e2ePriv = priv
	return priv, nil
}

// E2EPublicKey is this machine's own long-term end-to-end public key,
// base64url (internal/e2e.EncodePublicKey) -- what a window reached through
// the relay's own full interface needs as the "host" side of
// StartDeviceHandshake, the same way /api/v1/me's Hosts[].PublicKey already
// gives it to flockdeck-remote. "" where there is none yet to give -- the
// disk is unavailable, say -- which is the same as any other machine this
// package treats as end-to-end incapable: a terminal served unencrypted,
// not a fault.
func (m *Manager) E2EPublicKey() string {
	priv, err := m.hostE2EIdentity()
	if err != nil {
		return ""
	}
	return e2e.EncodePublicKey(priv.PublicKey())
}

// KeyOrigin says which of a device's two end-to-end keys (see client.go's
// own doc on Device.DeskPublicKey) a terminal's handshake answers with: the
// one it registered from its usual origin, or the one it registered from
// this host's own full interface. pty.go decides which, from the relay's
// own Flockdeck-Remote-Origin header on the socket -- never anything a
// browser could set for itself, the same trust this package already gives
// Flockdeck-Remote-Device.
type KeyOrigin int

const (
	KeyOriginUsual KeyOrigin = iota
	KeyOriginDesk
)

// key is deviceID's key for this origin, out of the roster e2eRoster reads.
func (o KeyOrigin) key(d Device) string {
	if o == KeyOriginDesk {
		return d.DeskPublicKey
	}
	return d.PublicKey
}

// e2eRoster is the account's devices, by id, exactly as the relay reports
// them -- both end-to-end keys included, one for each origin a device might
// open a terminal from. It is fetched from the relay through Client().Devices,
// cached for e2eRosterTTL.
func (m *Manager) e2eRoster(ctx context.Context) (map[string]Device, error) {
	m.e2eMu.Lock()
	if m.e2eRosterCache != nil && time.Since(m.e2eRosterAt) < e2eRosterTTL {
		devices := m.e2eRosterCache
		m.e2eMu.Unlock()
		return devices, nil
	}
	m.e2eMu.Unlock()

	cl, err := m.Client()
	if err != nil {
		return nil, err
	}
	roster, err := cl.Devices(ctx)
	if err != nil {
		return nil, err
	}
	devices := make(map[string]Device, len(roster.Devices))
	for _, d := range roster.Devices {
		devices[d.ID] = d
	}
	m.e2eMu.Lock()
	m.e2eRosterCache, m.e2eRosterAt = devices, time.Now()
	m.e2eMu.Unlock()
	return devices, nil
}

// E2ECapable reports whether a terminal opened for deviceID right now, from
// origin, could be end-to-end encrypted: this machine has a long-term key,
// and the relay's roster says deviceID has registered origin's key too. It
// answers false, never an error, for anything that stops it finding out --
// the relay unreachable, no such device, an unenrolled machine -- since
// every one of those is a plain "serve this terminal unencrypted", not a
// fault to report.
func (m *Manager) E2ECapable(ctx context.Context, deviceID string, origin KeyOrigin) bool {
	if deviceID == "" {
		return false
	}
	if _, err := m.hostE2EIdentity(); err != nil {
		return false
	}
	devices, err := m.e2eRoster(ctx)
	if err != nil {
		return false
	}
	return origin.key(devices[deviceID]) != ""
}

// E2ERespond runs this host's whole side of a fresh terminal handshake
// (internal/e2e.RespondHostHandshake): hello is the browser's ephemeral
// public key, the terminal socket's first frame. It returns the session and
// the response to send back as the socket's second frame.
//
// Callers are expected to have checked E2ECapable first, with the same
// origin, so that hello is only ever read from a socket both sides are
// expected to encrypt; called otherwise, a missing key on either side comes
// back as ErrNoE2EKey rather than attempting a handshake that cannot
// complete.
func (m *Manager) E2ERespond(ctx context.Context, deviceID string, origin KeyOrigin, hello []byte) (*e2e.Session, []byte, error) {
	hostPriv, err := m.hostE2EIdentity()
	if err != nil {
		return nil, nil, err
	}
	devices, err := m.e2eRoster(ctx)
	if err != nil {
		return nil, nil, err
	}
	enc := origin.key(devices[deviceID])
	if enc == "" {
		return nil, nil, ErrNoE2EKey
	}
	devicePub, err := e2e.DecodePublicKey(enc)
	if err != nil {
		return nil, nil, fmt.Errorf("remote: the relay gave a bad end-to-end key for this device: %w", err)
	}
	return e2e.RespondHostHandshake(hostPriv, devicePub, hello)
}

// EnsureE2EKey makes sure this machine has a long-term end-to-end identity
// and that the relay has its public half on file, generating and
// registering one if not. Both halves are idempotent, so it is safe to call
// often: Reload does, every time the enrolment is read, which includes every
// application start and every explicit reconnect.
//
// A machine enrolled before this feature existed has no identity on its
// first call after upgrading, and gets one then; a machine that already has
// one, whose relay has simply forgotten it (a restore from backup, a
// migration), is registered again rather than left looking unencrypted to
// every device that asks.
func (m *Manager) EnsureE2EKey(ctx context.Context) error {
	priv, err := m.hostE2EIdentity()
	if err != nil {
		m.setE2ERegErr(err)
		return err
	}
	cl, err := m.Client()
	if err != nil {
		m.setE2ERegErr(err)
		return err
	}
	err = cl.SetE2EKey(ctx, e2e.EncodePublicKey(priv.PublicKey()))
	m.setE2ERegErr(err)
	return err
}

func (m *Manager) setE2ERegErr(err error) {
	m.e2eMu.Lock()
	m.e2eRegErr, m.e2eRegAt = err, time.Now()
	m.e2eMu.Unlock()
}

// E2EKeyStatus reports the outcome of the last attempt to register this
// machine's end-to-end key with the relay -- ok if it has never been tried,
// since that only means no enrolment has asked for it yet. It is what a
// diagnostics view queries; nothing here decides anything by it.
func (m *Manager) E2EKeyStatus() (at time.Time, err error) {
	m.e2eMu.Lock()
	defer m.e2eMu.Unlock()
	return m.e2eRegAt, m.e2eRegErr
}

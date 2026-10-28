package server

import (
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// The gate for phone approval: what decides, on each decision, whether a device
// may answer a permission request, and the one-time question the desk's dialog
// has to carry to turn a device on. approvals_prefs.go has the switch itself.

// approveSwitchOn reports whether the desk has switched approval on for a device
// at the current wording: in the running program's record and in the preferences
// file. It is for telling the device (hello.canApprove) and the desk's lists. It
// is not the gate: a decision is allowed only by mayApproveWithKey, which also
// holds the grant to the device's key.
func (s *Server) approveSwitchOn(deviceID string) bool {
	return s.approveAllowed(deviceID, func(a store.DeviceAccess) bool { return a.ApproveOn(approveAckVersion) })
}

// mayApproveWithKey is the gate. A device may answer a permission request only
// when approval is on at the current wording and the grant was made for the key
// it is using now: key is the device's usual end-to-end key as the handshake
// proved it. A device paired again under the same id, a key that changed, a
// restored preferences file with an old grant, and a record from before the grant
// was tied to a key all fail here and need the desk to turn approval on again.
//
// Both the running program's record and the preferences file must say yes. The
// record in memory is the one in force (a file edited while Flockdeck runs must
// not widen anything); the file can take approval away, so `flockdeck remote
// approvals off`, or deleting or damaging the file, takes effect on the next call
// even if the running program was not told. A file that cannot be read says no.
func (s *Server) mayApproveWithKey(deviceID string, key []byte) bool {
	h := store.KeyHash(key)
	return h != "" && s.approveAllowed(deviceID, func(a store.DeviceAccess) bool { return a.ApproveFor(approveAckVersion, h) })
}

func (s *Server) approveAllowed(deviceID string, ok func(store.DeviceAccess) bool) bool {
	if deviceID == "" || !ok(s.deviceAccess(deviceID)) {
		return false
	}
	devs, readable := s.approveFileDevices()
	return readable && ok(devs[deviceID])
}

// approveFileTTL is the longest a read of the preferences file is trusted when
// its modification time and size have not changed.
const approveFileTTL = 5 * time.Second

// approveFileDevices is the device records in the preferences file. A decision
// asks on every call, so the file is read again only when its modification time
// or size changed, and at least every approveFileTTL. It never moves a damaged
// file aside (store.PeekDeviceAccess); a file that cannot be read is reported as
// not readable, and nothing is then allowed.
func (s *Server) approveFileDevices() (map[string]store.DeviceAccess, bool) {
	c := &s.approveFile
	c.mu.Lock()
	defer c.mu.Unlock()
	mod, size, ok := store.PrefsStamp()
	if c.have && ok && mod.Equal(c.mod) && size == c.size && time.Since(c.at) < approveFileTTL {
		return c.devs, c.err == nil
	}
	devs, err := store.PeekDeviceAccess()
	c.have, c.mod, c.size, c.at, c.devs, c.err = true, mod, size, time.Now(), devs, err
	return devs, err == nil
}

// approveFileCache is the last read of the preferences file's device records.
type approveFileCache struct {
	mu   sync.Mutex
	have bool
	mod  time.Time
	size int64
	at   time.Time
	devs map[string]store.DeviceAccess
	err  error
}

// An approveChallenge is the host's one-time question to a window at the desk:
// the dialog asks for one when it opens, shows the wording, and sends the nonce
// back with the answer. It is bound to that window's connection, the device and
// the wording version, is used once, and expires.
type approveChallenge struct {
	client  *controlClient
	device  string
	version int
	expires time.Time
}

const (
	approveChallengeLife = 2 * time.Minute
	approveChallengeMax  = 16
)

// approveAsk holds the challenges handed out and not yet used.
type approveAsk struct {
	mu sync.Mutex
	by map[string]approveChallenge
}

func (a *approveAsk) prune(now time.Time) {
	for n, ch := range a.by {
		if now.After(ch.expires) {
			delete(a.by, n)
		}
	}
}

// give makes a challenge for c about device. One window has at most one
// challenge at a time; a new one replaces its old one.
func (a *approveAsk) give(c *controlClient, device string) (nonce string, ok bool) {
	n, err := randomHex(16)
	if err != nil {
		return "", false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	a.prune(now)
	for old, ch := range a.by {
		if ch.client == c {
			delete(a.by, old)
		}
	}
	if len(a.by) >= approveChallengeMax {
		return "", false
	}
	if a.by == nil {
		a.by = map[string]approveChallenge{}
	}
	a.by[n] = approveChallenge{client: c, device: device, version: approveAckVersion, expires: now.Add(approveChallengeLife)}
	return n, true
}

// take uses a challenge. It is spent whether or not it matches, so a guess costs
// the guesser the nonce; it matches only for the window it was given to, the
// device it was about, the current wording version, and before it expired.
func (a *approveAsk) take(nonce string, c *controlClient, device string, version int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	ch, found := a.by[nonce]
	delete(a.by, nonce)
	return found && ch.client == c && ch.device == device && ch.version == version && version == approveAckVersion && time.Now().Before(ch.expires)
}

// dropWindow forgets a window's challenge when it goes.
func (a *approveAsk) dropWindow(c *controlClient) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for n, ch := range a.by {
		if ch.client == c {
			delete(a.by, n)
		}
	}
}

// approveChallengeMsg is the host's answer to approveChallenge: the wording to
// show and the nonce that goes back with the person's yes.
type approveChallengeMsg struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Nonce     string `json:"nonce,omitempty"`
	V         int    `json:"v"`
	Text      string `json:"text"`
	ExpiresMS int    `json:"expiresMs"`
	Error     string `json:"error,omitempty"`
}

// askApprove answers a page at the desk that opened the question "Let this device
// approve?" with the wording and a nonce. It is refused to a window reached
// through the relay and to a connection that did not open as a page.
//
// This is not proof that a person is looking: a program that holds the window
// token can open a page connection of its own and ask. What it does is tie the
// yes to a connection that asked for it, to the device and to the wording, once,
// so a command sent blind, replayed, or sent from another connection does nothing.
func (s *Server) askApprove(c *controlClient, cmd command) {
	if s.refusedApproveThroughRelay(c, cmd) {
		return
	}
	if !validDeviceID(cmd.ID) {
		c.notify("No device was named", true)
		return
	}
	if !c.page {
		c.notify(pageOnlyApprove, true)
		return
	}
	msg := approveChallengeMsg{Type: "approveChallenge", ID: cmd.ID, V: approveAckVersion, Text: approveAckText, ExpiresMS: int(approveChallengeLife / time.Millisecond)}
	n, ok := s.approveAsk.give(c, cmd.ID)
	if !ok {
		msg.Error = "Too many questions are open. Try again in a moment"
	}
	msg.Nonce = n
	c.sendJSON(msg)
}

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/store"
)

// Remote artifacts: what a paired device may view of this machine through the
// /ws/artifacts socket, and the switches that govern it. See
// artifacts_socket.go for the socket itself.
//
// Everything is off until the desk turns it on, and only the desk can: every
// command here is refused when it arrives through the relay. Three things
// must all hold before a device is served anything: the kind is switched on
// and its acknowledgement is current, the device is on the allowlist, and the
// device's end-to-end key is the one the user verified (ArtifactVerifier).
// They are checked when the socket opens and again on every request, from the
// preferences file itself, so a switch turned off at the desk, or by
// `flockdeck remote artifacts off` with no window open, takes effect on the
// next frame.

// artifactAckVersion is the wording version of the acknowledgement text. Raise
// it when what viewing exposes changes, and everyone is asked again.
const artifactAckVersion = 1

// artifactKinds are the kinds the preferences know, in the order they are
// reported. Only the ones in artifactOffered can be switched on: links and
// files wait for devices that can be limited to viewing, which is not built.
var artifactKinds = []string{store.ArtifactKindRecordings, store.ArtifactKindLinks, store.ArtifactKindFiles}

var artifactOffered = map[string]bool{store.ArtifactKindRecordings: true}

// ArtifactVerifier says whether the desk has recorded a device's end-to-end
// key as verified: the user compared the fingerprint and marked it. It is true
// only for exactly that device, key origin and key, so a changed key is not
// verified. internal/remote's Manager provides it.
type ArtifactVerifier interface {
	DeviceVerified(deviceID string, origin remote.KeyOrigin, key []byte) bool
}

// artifactKeyer is the other half of the check: the key a device would
// handshake with, which a RemoteAccess may provide. One that does not can
// never have a device verified.
type artifactKeyer interface {
	E2EDeviceKey(ctx context.Context, deviceID string, origin remote.KeyOrigin) ([]byte, bool)
}

// SetArtifactVerifier hands the server its verified-device record. Until it is
// set, no device is verified and the artifacts socket serves nobody.
func (s *Server) SetArtifactVerifier(v ArtifactVerifier) {
	s.artifacts.mu.Lock()
	s.artifacts.verifier = v
	s.artifacts.mu.Unlock()
}

func (s *Server) artifactVerifier() ArtifactVerifier {
	s.artifacts.mu.Lock()
	defer s.artifacts.mu.Unlock()
	return s.artifacts.verifier
}

// The reasons the socket gives for closing on a device, which the window turns
// into words. They are the only detail a refused device is told.
const (
	artifactReasonDisabled = "disabled"
	artifactReasonDevice   = "device"
	artifactReasonVerify   = "verify"
	artifactReasonKey      = "key"
)

// artifactEnabledKinds is the kinds that are offered, switched on and
// acknowledged at the current wording.
func artifactEnabledKinds(p store.RemoteArtifactsPrefs) []string {
	var out []string
	for _, k := range artifactKinds {
		if artifactOffered[k] && p.KindOn(k) && p.Acked(k, artifactAckVersion) {
			out = append(out, k)
		}
	}
	return out
}

// artifactCheck decides whether device may use artifacts now, from the
// preferences as they are on disk. key is the device's end-to-end key for
// origin. It fails closed: preferences that cannot be read are all off.
func (s *Server) artifactCheck(device string, origin remote.KeyOrigin, key []byte) (kinds []string, reason string) {
	p := store.LoadPrefs().RemoteArtifacts
	kinds = artifactEnabledKinds(p)
	switch {
	case len(kinds) == 0:
		return nil, artifactReasonDisabled
	case !p.HasDevice(device):
		return nil, artifactReasonDevice
	}
	v := s.artifactVerifier()
	if v == nil || len(key) == 0 || !v.DeviceVerified(device, origin, key) {
		return nil, artifactReasonVerify
	}
	return kinds, ""
}

// artifactsOffered is whether a window reached through the relay is told
// artifacts exist: a kind is enabled for its device. It does not look at the
// device's key, which would mean asking the relay, so a device whose key has
// changed since it was verified is shown the tab and told to verify.
func artifactsOffered(p store.RemoteArtifactsPrefs, device string) bool {
	return len(artifactEnabledKinds(p)) > 0 && p.HasDevice(device)
}

// artifactsCap is the control socket's hello field naming the capability.
type artifactsCap struct {
	V int `json:"v"`
}

// artifactsCapMsg tells a window reached through the relay that artifacts have
// become available or gone, since hello is sent only once.
type artifactsCapMsg struct {
	Type      string `json:"type"`
	Available bool   `json:"available"`
	V         int    `json:"v,omitempty"`
}

// prefsFor is the preferences as window c may be told them. A window reached
// through the relay is not told the artifact settings: the device list is the
// desk's, and the control socket is readable by the relay.
func prefsFor(c *controlClient, p store.Prefs) store.Prefs {
	if c.remote {
		p.RemoteArtifacts = store.RemoteArtifactsPrefs{}
	}
	return p
}

// announceArtifacts tells each window reached through the relay whether
// artifacts are available to its device, where that has changed since it was
// last told. It runs on the workspace goroutine.
func (s *Server) announceArtifacts() {
	for _, cl := range s.clientList() {
		if !cl.remote {
			continue
		}
		on := artifactsOffered(s.prefs.RemoteArtifacts, cl.device)
		want := int32(artifactsTellNo)
		if on {
			want = artifactsTellYes
		}
		if cl.artifactsTold.Swap(want) == want {
			continue
		}
		msg := artifactsCapMsg{Type: "artifacts", Available: on}
		if on {
			msg.V = 1
		}
		cl.sendJSON(msg)
	}
}

// What a window has been told about artifacts: nothing yet, yes, or no.
const (
	artifactsTellYes = 1
	artifactsTellNo  = 2
)

// deskOnlyArtifacts is why a window reached through the relay may not change
// what can be viewed. A device that could widen it could give itself, or any
// other, a way to read this machine.
const deskOnlyArtifacts = "What paired devices may view is changed on the machine Flockdeck runs on, not from a window reached through the relay"

// refusedArtifactsThroughRelay refuses a window reached through the relay and
// reports whether it did. The attempt is recorded and shown at the desk: a
// legitimate window never sends one.
func (s *Server) refusedArtifactsThroughRelay(c *controlClient, cmd string) bool {
	if !c.remote {
		return false
	}
	c.notify(deskOnlyArtifacts, true)
	s.artifactDenied(c.device, "", "desk-only:"+cmd)
	return true
}

// artifactAckText is what the desk is shown before a kind is first switched
// on, and what a client that skips the question is answered with.
func artifactAckText(kind string) string {
	var what string
	switch kind {
	case "recordings":
		what = "Viewing recordings from another device shows what you and the agents said, including anything secret that redaction missed."
	default:
		what = "Viewing " + kind + " from another device shows them on that device."
	}
	return what + " They are redacted again before they are sent, but that is pattern matching and it misses things. " +
		"Nothing is sent unencrypted, and a relay that only passes traffic cannot read it. A relay that alters the page or the keys it serves to your device could, so the check that a device's key is the one you verified matters. " +
		"The device can keep what it displays, and nothing stops it copying or photographing the screen. " +
		"A paired device that is allowed here can already open a shell on this machine until devices can be limited to viewing, so this is not a boundary against that device. " +
		"Turn it off in Settings, with Stop all remote viewing now, or with `flockdeck remote artifacts off`."
}

// setRemoteArtifacts switches a kind on or off, or puts a device on the
// allowlist or takes it off, at the desk. Kind is "on" or "off". Exactly one
// of Text (the kind of artifact) and ID (the device) names what.
//
// Switching a kind on the first time is put to the user and arrives Confirmed;
// it is checked here as well, so a client that skips the question cannot skip
// the answer, as with recordPane. A device is put on the allowlist only if its
// key is verified now.
func (s *Server) setRemoteArtifacts(c *controlClient, cmd command) {
	if s.refusedArtifactsThroughRelay(c, cmd.Cmd) {
		return
	}
	on := cmd.Kind == "on"
	if !on && cmd.Kind != "off" {
		c.notify("Remote artifacts: say on or off", true)
		return
	}
	switch {
	case cmd.Text != "" && cmd.ID == "":
		s.setArtifactKind(c, cmd.Text, on, cmd.Confirmed)
	case cmd.ID != "" && cmd.Text == "":
		if on {
			s.allowArtifactDevice(c, cmd.ID)
		} else {
			s.revokeArtifactDevice(c, cmd)
		}
	default:
		c.notify("Remote artifacts: name a kind or a device, not both", true)
	}
}

func (s *Server) setArtifactKind(c *controlClient, kind string, on, confirmed bool) {
	known := false
	for _, k := range artifactKinds {
		known = known || k == kind
	}
	switch {
	case !known:
		c.notify("Remote artifacts: there is no kind called "+tidy(kind), true)
		return
	case on && !artifactOffered[kind]:
		c.notify("Viewing "+kind+" remotely is not available yet: it waits for devices that can be limited to viewing", true)
		return
	}
	if on && !store.LoadPrefs().RemoteArtifacts.Acked(kind, artifactAckVersion) && !confirmed {
		c.notify(artifactAckText(kind), true)
		return
	}
	s.updatePrefs(c, func(p *store.Prefs) bool {
		changed := false
		if on && !p.RemoteArtifacts.Acked(kind, artifactAckVersion) {
			p.RemoteArtifacts.SetAck(kind, store.ArtifactAck{Version: artifactAckVersion, At: time.Now().UTC()})
			changed = true
		}
		if p.RemoteArtifacts.SetKind(kind, on) {
			changed = true
		}
		if changed && !on {
			// Cut what is open now rather than at the next frame.
			s.artifacts.closeAll(artifactReasonDisabled)
		}
		return changed
	})
	event := "kind-off"
	if on {
		event = "kind-on"
	}
	_ = s.artifacts.audit.write(auditEvent{Event: event, Device: "desk", Kind: kind})
	if on {
		c.notify("Paired devices on the allowlist can now view "+kind+". Allow a device once you have verified its key", false)
	} else {
		c.notify("Paired devices can no longer view "+kind, false)
	}
}

// allowArtifactDevice puts a device on the allowlist, if its key is verified.
// Finding the key can mean asking the relay, so it is done off the window's
// read loop, and the preferences are changed only afterwards.
func (s *Server) allowArtifactDevice(c *controlClient, device string) {
	go func() {
		defer s.surviveFor(c, "allowing a device to view artifacts")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// The full interface is the only client of this socket for now, and it
		// uses the device's desk-origin key.
		key, ok := s.deviceKey(ctx, device, remote.KeyOriginDesk)
		if v := s.artifactVerifier(); !ok || v == nil || !v.DeviceVerified(device, remote.KeyOriginDesk, key) {
			c.notify("That device's key has not been verified here. Compare its code with the one on the device, mark it verified, then allow it", true)
			return
		}
		s.updatePrefs(c, func(p *store.Prefs) bool { return p.RemoteArtifacts.AddDevice(device) })
		_ = s.artifacts.audit.write(auditEvent{Event: "device-allow", Device: device})
		c.notify("That device may now use remote artifacts, for the kinds that are switched on", false)
	}()
}

// deviceKey is the key device would handshake with for origin.
func (s *Server) deviceKey(ctx context.Context, device string, origin remote.KeyOrigin) ([]byte, bool) {
	ra := s.remoteAccess()
	if ra == nil {
		return nil, false
	}
	k, ok := ra.(artifactKeyer)
	if !ok {
		return nil, false
	}
	return k.E2EDeviceKey(ctx, device, origin)
}

// revokeArtifactDevice takes a device off the allowlist and closes its sockets
// at once.
func (s *Server) revokeArtifactDevice(c *controlClient, cmd command) {
	if s.refusedArtifactsThroughRelay(c, cmd.Cmd) {
		return
	}
	if cmd.ID == "" {
		c.notify("Remote artifacts: name a device", true)
		return
	}
	s.artifacts.closeDevice(cmd.ID, artifactReasonDevice)
	s.updatePrefs(c, func(p *store.Prefs) bool { return p.RemoteArtifacts.RemoveDevice(cmd.ID) })
	_ = s.artifacts.audit.write(auditEvent{Event: "device-revoke", Device: cmd.ID})
	c.notify("That device can no longer view artifacts", false)
}

// stopRemoteArtifacts closes every artifacts socket now and forgets every id
// handed out. It changes no setting: a device that is still allowed can
// connect again.
func (s *Server) stopRemoteArtifacts(c *controlClient, cmd command) {
	if s.refusedArtifactsThroughRelay(c, cmd.Cmd) {
		return
	}
	n := s.artifacts.closeAll(artifactReasonDisabled)
	_ = s.artifacts.audit.write(auditEvent{Event: "stop", Device: "desk", Bytes: int64(n)})
	c.notify("Stopped all remote viewing", false)
}

// handleArtifactsStop is how `flockdeck remote artifacts off` reaches a running
// instance: the preferences file has already been written, so it rereads them
// and closes the sockets. Like /remote/reload it acts, so it wants a POST and
// the token in the URL, which no remote window has.
func (s *Server) handleArtifactsStop(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) || !s.requireURLToken(w, r) {
		return
	}
	s.artifacts.closeAll(artifactReasonDisabled)
	s.do(func() {
		if p, err := store.ReadPrefs(); err == nil {
			s.prefs = p
			s.broadcastPrefs()
		}
	})
	_ = s.artifacts.audit.write(auditEvent{Event: "off", Device: "desk"})
	w.WriteHeader(http.StatusNoContent)
}

// RequestArtifactsStop asks a running instance to reread the preferences and
// close every artifacts socket.
func RequestArtifactsStop(baseURL, token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/remote/artifacts/stop?t="+token, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return refused("stop remote artifacts", resp)
	}
	return nil
}

// noticeDesk shows a notice in every window at the desk, not those reached
// through the relay.
func (s *Server) noticeDesk(text string, isErr bool) {
	for _, cl := range s.clientList() {
		if !cl.remote {
			cl.notify(text, isErr)
		}
	}
}

// artifactDenied records and shows a refusal. Refusals are rare from a
// legitimate client and a sign of a modified or hostile one, so the desk is
// told; a device refused in a loop is recorded and shown at a limited rate so
// it cannot flood the log or the desk with it.
func (s *Server) artifactDenied(device, name, reason string) {
	record, notify, suppressed := s.artifacts.denial(device + "\x00" + reason)
	if record {
		_ = s.artifacts.audit.write(auditEvent{Event: "denied", Device: device, DeviceName: name, Reason: reason, Suppressed: suppressed})
	}
	if notify {
		who := tidy(name)
		if who == "" {
			who = "A paired device"
		}
		s.noticeDesk(who+" was refused remote artifacts ("+tidy(reason)+"). That is not something Flockdeck's own window does", true)
	}
}

// artifactKindsJSON is a kind list as the hello reply sends it, never null.
func artifactKindsJSON(kinds []string) json.RawMessage {
	if len(kinds) == 0 {
		return json.RawMessage("[]")
	}
	b, _ := json.Marshal(kinds)
	return b
}

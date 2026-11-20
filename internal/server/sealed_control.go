package server

import (
	"context"
	"slices"
	"time"

	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/store"
)

// "Control agents" (docs/remote-parity: stage 3): whether a paired device may act
// on agents through the sealed socket, which is tier B. It is the switch and its
// gate. It is off for every device, full ones included, until the desk turns it
// on for that one device after the device's usual key is verified, and it is tied
// to that key as phone approval is (store.DeviceAccess.ControlFor). Only a full
// device can hold it: a viewer, whatever the desk let it watch, never acts.

// deskOnlyControl is why a window reached through the relay may not change who
// can control agents. A device that could would grant itself the right.
const deskOnlyControl = "Whether a paired device may control agents is set on the machine Flockdeck runs on, not from a window reached through the relay"

// pageOnlyControl is why a connection that is not a Flockdeck window may not
// allow a device to control agents.
const pageOnlyControl = "A device can only be allowed to control agents from a Flockdeck window on this machine"

// controlNeedsChallenge is what a turn-on without the host's question is answered.
const controlNeedsChallenge = "Open the question for that device first. This request does not carry the confirmation the host gave this window"

// controlAckText is the wording the desk shows before a device is allowed to
// control agents; ui/src/lib/remote.js has the same words (CONTROL_ACK_WORDS).
// Control agents is equivalent to remote code execution on this machine: a
// device with it can run shell commands, start agents and type into any pane
// without a confirm at the desk.
const controlAckText = "This device will be able to send messages and pictures to your agents, stop them, answer their questions, approve their plans once, run one-line shell commands in a structured pane, start new agents (on a new branch if it asks), pick up a past conversation and add worktrees to projects Flockdeck knows. It can ask to commit, push or pull, which you allow here one request at a time. It cannot allow always, approve tool requests or merge. Turn it off here at any time."

// mayControlWithKey is the gate for tier B. A device may act only when the switch
// is on, it is a full device, and the grant was made for the key it is using now
// (key is the usual key as the handshake proved it). Both the running program's
// record and the preferences file must say yes, as for approval: the file can
// take control away, and a file that cannot be read says no.
func (s *Server) mayControlWithKey(device string, key []byte) bool {
	h := store.KeyHash(key)
	return h != "" && s.approveAllowed(device, func(a store.DeviceAccess) bool {
		return a.EffectiveRole() == store.RoleFull && a.ControlFor(h)
	})
}

// controlStale says a roster entry's grant no longer holds: the device's usual
// key is not the one it was made for, or is no longer marked verified.
func (s *Server) controlStale(d remote.Device, a store.DeviceAccess) bool {
	if !a.Control {
		return false
	}
	if h := usualKeyHash(d); h != "" && h != a.ControlKey {
		return true
	}
	return s.deviceVerifier() != nil && d.PublicKey != "" && s.approveKeyState(d) != verifyStateVerified
}

// setDeviceControl switches a device's tier B right on or off. ID is the device
// and On the new state. It is the desk's alone, refused to a window reached
// through the relay, and takes effect at once: the device's sealed sockets are
// ended and it reconnects to be told the new tier. Off needs nothing more and
// always wins over a turn-on still being checked (controlGen). On needs a window
// that opened as a page, Confirmed (the dialog has just shown the wording), a
// device the relay lists and its usual key marked verified now. It is applied
// only if the preferences could be saved.
func (s *Server) setDeviceControl(c *controlClient, cmd command) {
	if c.remote {
		c.notify(deskOnlyControl, true)
		s.noticeDesk("A window reached through the relay tried to change which devices may control agents ("+tidy(cmd.Cmd)+"). It was refused", true)
		return
	}
	if !validDeviceID(cmd.ID) {
		c.notify("No device was named", true)
		return
	}
	device := cmd.ID
	if !cmd.On {
		s.controlGen.Add(1)
		s.applyDeviceControl(c, device, false, 0, "")
		return
	}
	if !c.page || !cmd.Confirmed {
		c.notify(pageOnlyControl, true)
		return
	}
	// A one-use question the host gave this window for this device (approveChallenge
	// with kind "control"), as turning on phone approval needs. A command sent
	// blind, replayed or sent from another connection carries none.
	if !s.controlAsk.take(cmd.Nonce, c, device, approveAckVersion) {
		c.notify(controlNeedsChallenge, true)
		return
	}
	gen := s.controlGen.Load()
	go func() {
		defer s.surviveFor(c, "allowing a device to control agents")
		ctx, cancel := context.WithTimeout(context.Background(), rosterTimeout)
		defer cancel()
		ids, err := s.deviceRoster(ctx)
		if err != nil {
			c.notify("Could not check that device with the relay, so it was not allowed to control agents: "+err.Error(), true)
			return
		}
		if !slices.Contains(ids, device) {
			c.notify("That device is not paired any more", true)
			return
		}
		key, ok := s.deviceKey(ctx, device, approveKeyOrigin)
		v := s.deviceVerifier()
		if !ok || v == nil || len(key) == 0 || v.DeviceVerifyState(device, approveKeyOrigin, key) != remote.Verified {
			c.notify("That device's key has not been verified here. Compare its code with the one on the device, mark it verified, then allow it to control agents", true)
			return
		}
		s.applyDeviceControl(c, device, true, gen, store.KeyHash(key))
	}()
}

// applyDeviceControl makes the change setDeviceControl was asked for. Turning it
// off is applied in memory whether or not it can be saved; turning it on only
// when the file was written, only for a full device, and only if nothing has
// switched control off since gen was read.
func (s *Server) applyDeviceControl(c *controlClient, device string, on bool, gen uint64, keyHash string) {
	tooMany, unknown, superseded, notFull := false, false, false, false
	change := func(p *store.Prefs) bool {
		if on && s.controlGen.Load() != gen {
			superseded = true
			return false
		}
		if on && p.AccessUnknown() {
			unknown = true
			return false
		}
		old := p.AccessFor(device)
		access := old
		access.Control = on
		switch {
		case on && old.EffectiveRole() != store.RoleFull:
			notFull = true
			return false
		case on && old.ControlFor(keyHash):
			return false
		case !on && !old.Control:
			return false
		case on:
			access.ControlAt, access.ControlKey = time.Now().UTC(), keyHash
		}
		next, ok := p.WithAccess(device, access)
		if !ok {
			tooMany = true
			return false
		}
		*p = next
		return true
	}
	s.updateAccessReport(!on, change, func(applied bool, saveErr error) {
		switch {
		case superseded:
			c.notify("Control was switched off, or the device was unpaired, while it was being checked, so that device was not allowed to control agents", true)
			return
		case unknown:
			c.notify("What each device may do could not be read from the preferences, so no device was allowed to control agents", true)
			return
		case notFull:
			c.notify("Only a full device can be allowed to control agents. Make it full first", true)
			return
		case tooMany:
			c.notify("Too many devices have their own setting; make some of them full again first", true)
			return
		case !applied && saveErr != nil:
			c.notify("Could not save the preferences, so that device was not allowed to control agents: "+saveErr.Error(), true)
			return
		case !applied && on:
			c.notify("That device was already allowed to control agents", false)
			return
		case !applied:
			c.notify("That device was not allowed to control agents", false)
			return
		}
		s.sockets.cut(device)
		event := "device-control-off"
		if on {
			event = "device-control-on"
		}
		s.approvals.auditAsync(auditEvent{Event: event, Device: device, Reason: saveReason(saveErr)})
		switch {
		case saveErr != nil:
			c.notify("That device can no longer control agents. This is applied now, but could not be saved, so it will be lost when Flockdeck restarts: "+saveErr.Error(), true)
		case on:
			c.notify("That device can now send to agents, stop them, answer their questions and start new ones", false)
		default:
			c.notify("That device can no longer control agents", false)
		}
		s.remoteDevices(c)
	})
}

// reconcileControl switches control off for every device in a roster the relay
// returned whose grant no longer holds.
func (s *Server) reconcileControl(devices []remote.Device) {
	for _, d := range devices {
		if s.controlStale(d, s.deviceAccess(d.ID)) {
			s.clearControl(d.ID)
		}
	}
}

// clearControl switches one device's control off, ends its sockets, and makes any
// turn-on still being checked give up.
func (s *Server) clearControl(device string) {
	s.controlGen.Add(1)
	changed := false
	s.updateAccessReport(true, func(p *store.Prefs) bool {
		old := p.AccessFor(device)
		if !old.Control && old.ControlKey == "" {
			return false
		}
		old.Control = false
		next, ok := p.WithAccess(device, old)
		if !ok {
			return false
		}
		*p = next
		changed = true
		return true
	}, func(applied bool, _ error) {
		if applied && changed {
			s.sockets.cut(device)
			s.approvals.auditAsync(auditEvent{Event: "device-control-off", Device: device, Reason: "key-changed-or-unpaired"})
			s.noticeDesk("A device's right to control agents was switched off because its key is no longer the one it was allowed with. Allow it again once you have verified the key", false)
		}
	})
}

package server

import (
	"context"
	"fmt"
	"github.com/jmwri/flockdeck/internal/e2e"
	"net/http"
	"slices"
	"time"

	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/store"
)

// Phone approval: whether a paired device may answer a structured pane's
// permission request from its phone. See docs/phone-approval-plan.md. This file
// is the switch only. The sealed socket that carries a decision (/ws/approvals)
// and the push for a structured wait come later and ask mayApprove.
//
// Approval is off for every device, full devices included, until the desk turns
// it on for that one device. Turning it on needs three things at the desk:
// the device's end-to-end key marked verified, the person agreeing to
// approveAckText, and a window that opened as a page, which is the one place
// the person is asked. Turning it off needs none of them, and every way of
// turning it on or off is refused to a window reached through the relay.

// approveAckVersion is the wording version of approveAckText. Raise it when
// what an approving device is shown changes: every device then stops counting
// as on until the desk turns it on again and agrees to the new text.
const approveAckVersion = 2

// ApproveAckVersion is approveAckVersion for the command line, which lists only
// the devices that are allowed at the wording in force.
const ApproveAckVersion = approveAckVersion

// approveAckText is what the desk is shown before a device is allowed to approve,
// and is carried to the dialog in the host's challenge. It does not say the relay
// cannot read the cards: a relay that serves altered page code to the phone could.
const approveAckText = "This device will be shown the commands and file paths your agents ask to run or change, and can allow or deny them. A relay that only passes traffic along cannot read them; a tampered relay, or a tampered page on the phone, could."

// approveWordingChanged and approveNeedsChallenge are what a turn-on is answered
// with when it does not carry the wording the person saw, or a challenge the
// host gave this window for this device.
const (
	approveWordingChanged = "The wording of this question has changed. Open it again and read it before you allow the device"
	approveNeedsChallenge = "Open the question for that device first. This request does not carry the confirmation the host gave this window"
)

// approveKeyOrigin is the key an approving device handshakes with. The phone app
// is the only client, and it uses the device's usual key, not the one the full
// interface at the desk uses (remote.KeyOriginDesk).
const approveKeyOrigin = remote.KeyOriginUsual

// deskOnlyApprove is why a window reached through the relay may not change who
// can approve. A device that could would approve its own way in.
const deskOnlyApprove = "Whether a paired device may approve agent requests is set on the machine Flockdeck runs on, not from a window reached through the relay"

// pageOnlyApprove is why a connection that is not a Flockdeck window may not
// allow a device to approve.
const pageOnlyApprove = "A device can only be allowed to approve from a Flockdeck window on this machine"

// approveKeyState is the word for the state of a device's usual key, as
// verifyStateFor gives it, which a device list carries beside the switch.
func (s *Server) approveKeyState(d remote.Device) string {
	return s.verifyStateFor(d.ID, approveKeyOrigin, d.PublicKey)
}

// refusedApproveThroughRelay refuses a command that came from a window reached
// through the relay, and reports whether it did. Nothing in the app sends one
// from there, so one is a modified page or a relay trying it: it is logged and
// shown at the desk.
func (s *Server) refusedApproveThroughRelay(c *controlClient, cmd command) bool {
	if !c.remote {
		return false
	}
	c.notify(deskOnlyApprove, true)
	logf("refused %s for device %q from a window reached through the relay (paired device %q)", cmd.Cmd, cmd.ID, c.device)
	s.noticeDesk(fmt.Sprintf("A window reached through the relay tried to change which devices may approve agent requests (%s). It was refused", tidy(cmd.Cmd)), true)
	return true
}

// setDeviceApprove switches a device's approval on or off. ID is the device
// and On the new state. Like setDeviceRole it is the desk's alone and takes
// effect at once: the device's open sockets are ended, so its window reconnects
// and is told the new state in a fresh hello.
//
// Off needs nothing more, and always wins over an enable still being checked
// (approveGen). On is accepted only from a page, only with the wording version
// the person saw and the nonce the host gave this window for this device
// (askApprove), and only when the device's key is marked verified now. The grant
// is tied to that key. It is applied only if it can be saved: unlike a
// restriction, a permission that would be lost at the next restart, or that the
// file does not show, is not given.
func (s *Server) setDeviceApprove(c *controlClient, cmd command) {
	if s.refusedApproveThroughRelay(c, cmd) {
		return
	}
	if !validDeviceID(cmd.ID) {
		c.notify("No device was named", true)
		return
	}
	device := cmd.ID
	if !cmd.On {
		s.approveGen.Add(1)
		s.applyDeviceApprove(c, device, false, 0, "")
		return
	}
	switch {
	case !c.page:
		c.notify(pageOnlyApprove, true)
		return
	case cmd.V != approveAckVersion:
		c.notify(approveWordingChanged, true)
		return
	case !s.approveAsk.take(cmd.Nonce, c, device, cmd.V):
		c.notify(approveNeedsChallenge, true)
		return
	}
	gen := s.approveGen.Load()
	go func() {
		defer s.surviveFor(c, "allowing a device to approve")
		ctx, cancel := context.WithTimeout(context.Background(), rosterTimeout)
		defer cancel()
		ids, err := s.deviceRoster(ctx)
		if err != nil {
			c.notify("Could not check that device with the relay, so it was not allowed to approve: "+err.Error(), true)
			return
		}
		if !slices.Contains(ids, device) {
			c.notify("That device is not paired any more", true)
			return
		}
		key, ok := s.deviceKey(ctx, device, approveKeyOrigin)
		v := s.deviceVerifier()
		if !ok || v == nil || len(key) == 0 || v.DeviceVerifyState(device, approveKeyOrigin, key) != remote.Verified {
			c.notify("That device's key has not been verified here. Compare its code with the one on the device, mark it verified, then allow it to approve", true)
			return
		}
		s.applyDeviceApprove(c, device, true, gen, store.KeyHash(key))
	}()
}

// applyDeviceApprove makes the change setDeviceApprove was asked for. Turning
// approval off is applied in memory whether or not it can be saved, as a
// restriction is; turning it on only when the file was written, and only if
// nothing has switched approval off since gen was read (approveGen): the check is
// made on the workspace goroutine, in the same step as the change, so a disable
// that landed while the key and roster were being checked always wins. keyHash is
// the device's usual key, which the grant is tied to.
func (s *Server) applyDeviceApprove(c *controlClient, device string, on bool, gen uint64, keyHash string) {
	tooMany, unknown, superseded := false, false, false
	change := func(p *store.Prefs) bool {
		if on && s.approveGen.Load() != gen {
			superseded = true
			return false
		}
		if on && p.AccessUnknown() {
			unknown = true
			return false
		}
		old := p.AccessFor(device)
		access := old
		access.Approve = on
		switch {
		case on && old.ApproveFor(approveAckVersion, keyHash):
			return false
		case !on && !old.Approve:
			return false
		case on:
			access.ApproveV, access.ApproveAt, access.ApproveKey = approveAckVersion, time.Now().UTC(), keyHash
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
			c.notify("Approval was switched off, or the device was unpaired, while it was being checked, so that device was not allowed to approve", true)
			return
		case unknown:
			c.notify("What each device may do could not be read from the preferences, so no device was allowed to approve", true)
			return
		case tooMany:
			c.notify("Too many devices have their own setting; make some of them full again first", true)
			return
		case !applied && saveErr != nil:
			c.notify("Could not save the preferences, so that device was not allowed to approve: "+saveErr.Error(), true)
			return
		case !applied && on:
			c.notify("That device was already allowed to approve", false)
			return
		case !applied:
			c.notify("That device was not allowed to approve", false)
			return
		}
		// After the new record is published, so that a socket opened between
		// the two already sees it.
		s.sockets.cut(device)
		switch {
		case saveErr != nil:
			c.notify("That device can no longer approve agent requests. This is applied now, but could not be saved, so it will be lost when Flockdeck restarts: "+saveErr.Error(), true)
		case on:
			c.notify("That device can now allow or deny the commands and file changes your agents ask for", false)
		default:
			c.notify("That device can no longer approve agent requests", false)
		}
		s.remoteDevices(c)
	})
}

// approvalsOff switches approval off for every device and reports who it was
// on for. It is the change behind the kill switch, whichever way it is reached.
func approvalsOff(p *store.Prefs) (changed bool, was []string) {
	next, was := p.WithoutApprovals()
	if len(was) == 0 && len(next.Devices) == len(p.Devices) && !hasApproveResidue(p.Devices) {
		return false, nil
	}
	*p = next
	slices.Sort(was)
	return true, was
}

// hasApproveResidue is whether any record holds an agreement without the
// switch, which the kill switch also clears.
func hasApproveResidue(m map[string]store.DeviceAccess) bool {
	for _, a := range m {
		if a.ApproveV != 0 || !a.ApproveAt.IsZero() || a.ApproveKey != "" {
			return true
		}
	}
	return false
}

// setApprovalsOff is the kill switch: no device may approve until the desk
// allows it again, one device at a time. It acts from any window at the desk,
// since it only narrows, and is refused to a window reached through the relay.
func (s *Server) setApprovalsOff(c *controlClient, cmd command) {
	if s.refusedApproveThroughRelay(c, cmd) {
		return
	}
	s.stopApprovals(func(n int, saveErr error) {
		switch {
		case n == 0 && saveErr == nil:
			c.notify("No device was allowed to approve", false)
		case saveErr != nil:
			c.notify("Approval is off for every device. This is applied now, but could not be saved, so it will be lost when Flockdeck restarts: "+saveErr.Error(), true)
		default:
			c.notify("Approval is off for "+plural(n, "device"), false)
		}
		s.remoteDevices(c)
	})
}

// stopApprovals switches approval off for every device, in memory and in the
// file, ends their sockets, and then calls done with how many devices it was on
// for and the save's error. done is called once, on the workspace goroutine,
// whether or not anything changed.
func (s *Server) stopApprovals(done func(n int, saveErr error)) {
	var was []string
	s.approveGen.Add(1)
	s.updateAccessReport(true, func(p *store.Prefs) bool {
		// Who it was on for is read from the record in force, not from p, which
		// the file may already have narrowed (the command line writes it first).
		for id, a := range s.prefs.Devices {
			if a.Approve {
				was = append(was, id)
			}
		}
		ch, _ := approvalsOff(p)
		return ch
	}, func(applied bool, saveErr error) {
		slices.Sort(was)
		for _, d := range was {
			s.sockets.cut(d)
		}
		done(len(was), saveErr)
	})
}

// handleApprovalsStop is how `flockdeck remote approvals off` reaches a running
// instance. The command line has already written the file, but each device's
// record is the running program's to say, not the file's, so this switches
// approval off in memory too. Like /remote/artifacts/stop it acts, so it wants a
// POST and the token in the URL, which no remote window has.
func (s *Server) handleApprovalsStop(w http.ResponseWriter, r *http.Request) {
	if fromRemote(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !requirePost(w, r) || !s.requireURLToken(w, r) {
		return
	}
	applied := make(chan struct{})
	s.stopApprovals(func(int, error) { close(applied) })
	select {
	case <-applied:
	case <-s.closed:
	case <-r.Context().Done():
	}
	w.WriteHeader(http.StatusNoContent)
}

// RequestApprovalsStop asks a running instance to switch approval off for every
// device and end their sockets.
func RequestApprovalsStop(baseURL, token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/remote/approvals/stop?t="+token, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return refused("switch off phone approval", resp)
	}
	return nil
}

// usualKeyHash is KeyHash of a roster entry's usual key, or "" when it has none
// or one that cannot be read.
func usualKeyHash(d remote.Device) string {
	if d.PublicKey == "" {
		return ""
	}
	pub, err := e2e.DecodePublicKey(d.PublicKey)
	if err != nil {
		return ""
	}
	return store.KeyHash(pub.Bytes())
}

// approvalStale says a roster entry's grant no longer holds: the device's usual
// key is not the one the grant was made for, or it is no longer marked verified.
// A device the roster gives no key for, or a host with no record of
// verifications, is not judged here (the gate still refuses it).
func (s *Server) approvalStale(d remote.Device, a store.DeviceAccess) bool {
	if !a.Approve {
		return false
	}
	if h := usualKeyHash(d); h != "" && h != a.ApproveKey {
		return true
	}
	return s.deviceVerifier() != nil && d.PublicKey != "" && s.approveKeyState(d) != verifyStateVerified
}

// anyApprovalMissingFrom reports whether some device that may approve is not in
// the roster.
func (s *Server) anyApprovalMissingFrom(devices []remote.Device) bool {
	t := s.access.Load()
	if t == nil {
		return false
	}
	for id, a := range t.prefs.Devices {
		if !a.Approve {
			continue
		}
		found := false
		for _, d := range devices {
			found = found || d.ID == id
		}
		if !found {
			return true
		}
	}
	return false
}

// reconcileApprovals switches approval off for every device in a roster the
// relay returned whose grant no longer holds: its usual key changed, or it is
// no longer marked verified. This is bookkeeping; the gate already refuses such
// a device, but the desk should see it off and be asked again.
func (s *Server) reconcileApprovals(devices []remote.Device) {
	for _, d := range devices {
		if s.approvalStale(d, s.deviceAccess(d.ID)) {
			s.clearApproval(d.ID)
		}
	}
}

// clearApproval switches one device's approval off, ends its sockets, and makes
// any enable still being checked give up.
func (s *Server) clearApproval(device string) {
	s.approveGen.Add(1)
	changed := false
	s.updateAccessReport(true, func(p *store.Prefs) bool {
		old := p.AccessFor(device)
		if !old.Approve && old.ApproveKey == "" && old.ApproveV == 0 {
			return false
		}
		old.Approve = false
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
			s.noticeDesk("A device's approval was switched off because its key is no longer the one it was allowed with. Allow it again once you have verified the key", false)
		}
	})
}

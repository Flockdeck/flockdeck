package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// Stage 8 of /ws/sealed (API 1.42, cap sealedSettings): the few settings that
// belong on a phone, the list of paired devices and unpairing the phone itself,
// and GitHub.
//
//   - Push preferences: pushPrefsGet is a read (tier A, any device that is served);
//     pushNotify, pushAnonymous and pushDelay are tier B and are answered with a
//     pushPrefs message that says where the preference stands. They change what is
//     sent to every paired device, so each needs the device's "Control agents"
//     switch, as the other tier B commands do.
//   - Devices: remoteDevices is a tier A read for a full device, answered with a
//     deviceRoster: names and times, and which row is this device. No key,
//     fingerprint, device id of another device, host or plan. remoteRevoke is run
//     only on the device's own id (sealedSelfOnly), so a phone can leave and cannot
//     lock out another. Renaming, pairing, roles, the control switch, verification
//     and enabling or removing remote access are the desk's and are not here.
//   - GitHub: reads (ghStatus, ghPRs, ghPR, ghIssues, ghIssue) are tier A for a
//     full device, with the text of other people cut and redacted. A comment on a
//     pull request or an issue is tier C: the desk is asked for each one
//     (sealed_confirm.go). Creating a pull request or an issue, signing in or out,
//     and installing gh are the desk's.

// sealedSelfOnly are the commands a sealed device runs only on itself. They are
// tier A in sealedCommands (any served device, a viewer too), and are held to the
// same rate as tier B and written to the log.
var sealedSelfOnly = map[string]bool{"remoteRevoke": true}

// sealedCodeSelf: a command that may only be for the device itself, sent for
// another.
const sealedCodeSelf = "self"

// Words for the device. Fixed, so a refusal never quotes anything.
const (
	revokeSelfFailed = "Could not unpair this device. Try again, or do it from the desktop"
	// maxGHBody and maxGHComments bound what a pull request or an issue carries to a
	// phone: other people's text, of any length.
	maxGHBody     = 20000
	maxGHComments = 50
	maxGHComment  = 4000
)

// pushPrefsMsg is where the push preferences stand: what the Notifications page
// of the desktop shows. The delay is the effective one, in seconds.
type pushPrefsMsg struct {
	Type           string `json:"type"`
	Req            string `json:"req,omitempty"`
	Off            bool   `json:"off"`
	Anonymous      bool   `json:"anonymous"`
	DelaySeconds   int    `json:"delaySeconds"`
	DefaultSeconds int    `json:"defaultSeconds"`
	MinSeconds     int    `json:"minSeconds"`
	MaxSeconds     int    `json:"maxSeconds"`
}

func pushPrefsOf(p store.PushPrefs, req string) pushPrefsMsg {
	return pushPrefsMsg{
		Type: "pushPrefs", Req: clampReq(req), Off: p.Off, Anonymous: p.Anonymous,
		DelaySeconds:   int(pushDelay(p) / time.Second),
		DefaultSeconds: int(defaultPushDelay / time.Second),
		MinSeconds:     int(minPushDelay / time.Second),
		MaxSeconds:     int(maxPushDelay / time.Second),
	}
}

// sendPushPrefs answers pushPrefsGet.
func (s *Server) sendPushPrefs(c *controlClient, req string) {
	p, ok := ask(s, func() store.PushPrefs { return s.prefs.Push })
	if !ok {
		return
	}
	c.sendJSON(pushPrefsOf(p, req))
}

// pushPrefsReply is what a push preference command is followed by: for a sealed
// device, the preferences as they stand (changed or not, so a refused delay is
// answered too); for any other window nothing, which has the prefs broadcast.
func (s *Server) pushPrefsReply(c *controlClient, req string) func(prefsResult) {
	if c.sealed == nil {
		return nil
	}
	return func(prefsResult) {
		// Runs on the workspace goroutine, which owns s.prefs.
		c.sendJSON(pushPrefsOf(s.prefs.Push, req))
	}
}

// deviceRosterMsg is the list of paired devices as a phone is told it: no key,
// no fingerprint, no id but its own, no host.
type deviceRosterMsg struct {
	Type    string      `json:"type"`
	Enabled bool        `json:"enabled"`
	Self    string      `json:"self,omitempty"`
	Devices []deviceRow `json:"devices"`
	Error   string      `json:"error,omitempty"`
}

type deviceRow struct {
	Name     string    `json:"name"`
	Created  time.Time `json:"created,omitzero"`
	LastSeen time.Time `json:"lastSeen,omitzero"`
	Current  bool      `json:"current,omitempty"`
}

// sealedRoster cuts the desk's device list down to what a phone is sent.
func sealedRoster(m remoteDevicesMsg, self string) deviceRosterMsg {
	out := deviceRosterMsg{Type: "deviceRoster", Enabled: m.Enabled, Devices: []deviceRow{}}
	if m.Error != "" {
		out.Error = safeLine(m.Error, maxRemoteNotice)
	}
	for _, d := range m.Devices {
		row := deviceRow{Name: safeLine(d.Name, maxRemoteName), Created: d.Created, LastSeen: d.LastSeen}
		if self != "" && d.ID == self {
			row.Current = true
			out.Self = self
		}
		out.Devices = append(out.Devices, row)
	}
	return out
}

// deviceRevokedMsg is how unpairing the device itself ended.
type deviceRevokedMsg struct {
	Type  string `json:"type"`
	Req   string `json:"req,omitempty"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// revokeSelf unpairs the sealed device that asked, at the relay, and ends its
// sockets. The answer is written before the device's key is forgotten, because
// forgetting it closes the gate that every other write goes through. The desk is
// told, and the log names the device and the outcome.
func (s *Server) revokeSelf(c *controlClient, req string) {
	l := c.sealed
	if l == nil || c.device == "" {
		return
	}
	device, name := c.device, l.name
	go func() {
		defer s.surviveFor(c, "unpairing a device")
		ctx, cancel := context.WithTimeout(context.Background(), remoteCallTimeout)
		defer cancel()
		res := deviceRevokedMsg{Type: "deviceRevoked", Req: clampReq(req)}
		cl, err := s.remoteClient()
		if err == nil {
			err = cl.Revoke(ctx, device)
		}
		if err != nil {
			res.Error = revokeSelfFailed
			c.sendJSON(res)
			s.approvals.auditAsync(auditEvent{Event: "sealed-self", Device: device, DeviceName: name, Kind: "remoteRevoke", Reason: "failed"})
			return
		}
		res.OK = true
		l.writeFrame(ctx, mustMarshal(res))
		s.forgetVerified(device)
		s.forgetRole(device)
		s.approvals.auditAsync(auditEvent{Event: "sealed-self", Device: device, DeviceName: name, Kind: "remoteRevoke", Reason: "unpaired"})
		who := tidy(name)
		if who == "" {
			who = "A paired device"
		}
		s.noticeDesk(who+" unpaired itself", false)
		s.sockets.cut(device)
	}()
}

// scrubGHView cuts what a pull request or an issue carries that other people
// wrote, to a size a phone can be sent, and redacts it. A message that cannot be
// read is dropped.
func scrubGHView(data []byte) []byte {
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	if item, ok := m["item"].(map[string]any); ok {
		if b, ok := item["body"].(string); ok {
			item["body"] = cutRunes(b, maxGHBody)
		}
		if cs, ok := item["comments"].([]any); ok {
			if len(cs) > maxGHComments {
				cs = cs[len(cs)-maxGHComments:]
			}
			for _, c := range cs {
				if cm, ok := c.(map[string]any); ok {
					if b, ok := cm["body"].(string); ok {
						cm["body"] = cutRunes(b, maxGHComment)
					}
				}
			}
			item["comments"] = cs
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return redactSealed(out, nil)
}

// cutRunes cuts s to at most n characters.
func cutRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

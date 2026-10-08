package server

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/store"
)

// A paired device is either full or a viewer (store.Role). A viewer is
// enforced here, on the machine the agents run on, and by default-deny at every
// point a remote device can reach:
//
//   - a command from a viewer's control socket is refused unless commandAccess
//     lists it for viewers (allowCommand);
//   - what a viewer is sent is only what viewerMessageTypes names, and a
//     snapshot cut down to what it may see (viewerSnapshot);
//   - a viewer's terminal socket is refused, or for a viewer the desk allowed to
//     watch panes, opened with its input, resizes and focus dropped (handlePTY).
//
// The role is looked up on every command, message and keystroke rather than
// read once when the socket opened, and changing it ends the device's open
// sockets so the window reconnects into the new shape (setDeviceRole).

// Role is store.Role: what a paired device may do.
type Role = store.Role

// accessTable is the desk's decisions as the connection goroutines read them.
// prefs belongs to the workspace goroutine, which a command from a window must
// not wait on, so each change to it publishes a copy here.
type accessTable struct {
	prefs store.Prefs
}

// publishAccess makes the preferences' device record visible to connection
// goroutines. It is called wherever s.prefs is assigned.
func (s *Server) publishAccess() {
	s.access.Store(&accessTable{prefs: s.prefs})
}

// deviceAccess is what the desk allowed a device. A request that names no
// device is a viewer: the relay always names one, so one without a name is not
// something the desk could have been asked about.
func (s *Server) deviceAccess(deviceID string) store.DeviceAccess {
	if deviceID == "" {
		return store.DeviceAccess{Role: store.RoleViewer}
	}
	t := s.access.Load()
	// Before the table is first published nothing is known, and nothing is
	// allowed.
	if t == nil {
		return store.DeviceAccess{Role: store.RoleViewer}
	}
	return t.prefs.AccessFor(deviceID)
}

// deviceRole is the role the host enforces for a device. A device with no
// record is full.
func (s *Server) deviceRole(deviceID string) Role {
	return s.deviceAccess(deviceID).EffectiveRole()
}

// accessOf is what the desk allowed the device behind a window. A window on
// this machine is not a device and is not limited.
func (s *Server) accessOf(c *controlClient) store.DeviceAccess {
	if !c.remote {
		return store.DeviceAccess{Role: store.RoleFull}
	}
	return s.deviceAccess(c.device)
}

// cmdAccess says who may send a command.
type cmdAccess uint8

const (
	// accessFull is the zero value, so a command left out of the table is
	// refused to viewers. Only full devices, and windows on this machine, send it.
	accessFull cmdAccess = iota
	// accessWatcher is a read of pane content: a viewer the desk has let watch
	// panes may send it.
	accessWatcher
	// accessViewer is something any viewer may send.
	accessViewer
)

// commandAccess decides, for every command handleCommand knows, whether a
// viewer may send it. TestEveryCommandHasAnAccessDecision reads handleCommand's
// source and fails for a command missing here or listed here and no longer
// handled, so adding a command forces the decision to be written down.
//
// Everything is accessFull unless it says otherwise: the artifacts viewer a
// later version adds is not a command on this socket, so no command is open to
// every viewer yet. accessViewer exists for the day one is.
var commandAccess = map[string]cmdAccess{
	// Reading a pane's chat. Same content the terminal shows, so it needs the
	// same permission as watching the terminal.
	"conversationOpen":   accessWatcher,
	"conversationOlder":  accessWatcher,
	"conversationDetail": accessWatcher,
	"conversationClose":  accessWatcher,
	"conversationSearch": accessWatcher,

	// Everything below is refused to viewers. Spelled out, not left to the
	// default, so each is a decision somebody made.
	"worktrees": accessFull, "worktreeAdd": accessFull, "worktreeRemove": accessFull, "worktreePrune": accessFull,
	"browse": accessFull, "fanoutPreview": accessFull, "fanout": accessFull, "startAgent": accessFull,
	"makeBaton": accessFull, "saveBaton": accessFull, "startFromBaton": accessFull, "restartWithBaton": accessFull,
	"approveBaton": accessFull, "routeTasks": accessFull, "setRouting": accessFull, "clearRoutingLog": accessFull,
	"agents": accessFull, "keys": accessFull, "keySet": accessFull, "keyClear": accessFull,
	"refreshAgents": accessFull, "setAgentDefault": accessFull, "setAgentAddress": accessFull,
	"revealPane": accessFull, "changes": accessFull, "diff": accessFull, "commit": accessFull,
	"gitPush": accessFull, "gitPull": accessFull, "gitFetch": accessFull,
	"ghStatus": accessFull, "ghInstall": accessFull, "ghLogin": accessFull, "ghLoginCancel": accessFull,
	"ghLogout": accessFull, "ghPRs": accessFull, "ghPR": accessFull, "ghPRCreate": accessFull,
	"ghPRComment": accessFull, "ghIssues": accessFull, "ghIssue": accessFull, "ghIssueCreate": accessFull,
	"ghIssueComment": accessFull, "ghChecks": accessFull,
	"conversations": accessFull, "attachImage": accessFull, "resumeConversation": accessFull,
	"recents": accessFull, "fanoutHistory": accessFull,
	"todoPlanPreview": accessFull, "todos": accessFull, "todoSave": accessFull, "todoDelete": accessFull,
	"todoStepDone": accessFull, "todoStepStart": accessFull,
	"remoteDevices": accessFull, "remotePair": accessFull, "remoteRevoke": accessFull, "remoteRename": accessFull,
	"remoteEnable": accessFull, "remoteDisable": accessFull, "remoteRemove": accessFull, "remoteMove": accessFull,
	"remoteReconnect": accessFull, "setDeviceRole": accessFull,
	"remoteVerify": accessFull, "remoteUnverify": accessFull,
	"helpSeen": accessFull, "dismissTip": accessFull, "fontSize": accessFull, "notifications": accessFull,
	"scrollback": accessFull, "updates": accessFull, "checkForUpdate": accessFull,
	"helpers": accessFull, "helperPlan": accessFull, "helperInstall": accessFull, "helperStart": accessFull,
	"helperStop": accessFull, "helperOpen": accessFull, "helperUninstall": accessFull,
	"listVersions": accessFull, "installVersion": accessFull, "presence": accessFull,
	"pushNotify": accessFull, "pushAnonymous": accessFull, "pushDelay": accessFull, "resetTips": accessFull,
	"cursorBlink": accessFull, "cursorStyle": accessFull, "screenReader": accessFull, "statusLine": accessFull,
	"fontFamily": accessFull, "railExpanded": accessFull, "railWidth": accessFull, "theme": accessFull,
	"accentColor": accessFull, "fanOutSameTab": accessFull, "autoReviewDefault": accessFull,
	"jevStatus": accessFull, "conflictRadar": accessFull,
	"recordPane": accessFull, "exportTranscript": accessFull, "revealTranscript": accessFull, "openRecordings": accessFull,
	"paneInfo": accessFull, "findPane": accessFull, "jevKey": accessFull,
	"setKeybinding": accessFull, "resetKeybinding": accessFull, "resetKeybindings": accessFull,
	"forgetRecent": accessFull, "renameProject": accessFull, "archiveProject": accessFull,
	"reorderProjects": accessFull, "removeProject": accessFull,

	"newTab": accessFull, "closeTab": accessFull, "selectTab": accessFull, "nextTab": accessFull,
	"prevTab": accessFull, "renameTab": accessFull, "openProject": accessFull, "selectProject": accessFull,
	"selectRepo": accessFull, "closeProject": accessFull, "addRepoToGroup": accessFull,
	"removeRepoFromGroup": accessFull, "renameGroup": accessFull, "groupProjects": accessFull,
	"splitPane": accessFull, "closePane": accessFull, "closeFinishedPanes": accessFull, "lockPane": accessFull,
	"focusPane": accessFull, "restartPane": accessFull, "toggleZoom": accessFull, "mutePane": accessFull,
	"autoReview": accessFull, "movePane": accessFull, "swapPanes": accessFull, "movePaneDir": accessFull,
	"movePaneToTab": accessFull, "movePaneToNewTab": accessFull, "tilePanes": accessFull, "moveTab": accessFull,
	"mergeTab": accessFull, "mergeAllTabs": accessFull, "setWeights": accessFull, "resize": accessFull,
	"toggleBroadcast": accessFull, "toggleBroadcastMember": accessFull, "sendPrompt": accessFull,
	"save": accessFull, "detach": accessFull, "quit": accessFull, "restart": accessFull,
}

// allowCommand reports whether a window may send a command. A window on this
// machine and a full device may send any; a viewer only what the table lists
// for it. A command the table does not know is refused to a viewer.
func (s *Server) allowCommand(c *controlClient, name string) bool {
	a := s.accessOf(c)
	if a.EffectiveRole() == store.RoleFull {
		return true
	}
	switch commandAccess[name] {
	case accessViewer:
		return true
	case accessWatcher:
		return a.WatchPanes
	}
	return false
}

// viewerRefused is what a viewer is told when it sends a command it may not.
const viewerRefused = "This device can only view. Ask for it to be made a full device on the machine Flockdeck runs on"

// refuseViewer tells a viewer its command was not done. It goes round the
// outgoing filter, which would drop a notice, because this one is the answer to
// the viewer's own request and says nothing about the desk.
func (c *controlClient) refuseViewer() {
	if data, err := json.Marshal(noticeMsg{Type: "notice", Text: viewerRefused, Error: true}); err == nil {
		c.putMessage(data)
	}
}

// viewerMessageTypes are the control messages a viewer is sent, and
// watcherMessageTypes the ones added for a viewer allowed to watch panes. The
// state snapshot is not here: it goes by sendSnapshotTo, cut down. Every other
// message -- the desk's notices, preferences, approvals, device lists, helper
// and git results -- is dropped for a viewer, including any type added later.
var viewerMessageTypes = map[string]bool{
	"hello": true,
}

var watcherMessageTypes = map[string]bool{
	"conversationPage":   true,
	"conversationDetail": true,
	"conversationAppend": true,
	"conversationSearch": true,
}

// mayReceive reports whether a message may go to this window.
func (c *controlClient) mayReceive(data []byte) bool {
	if c.accessOf == nil {
		return true
	}
	a := c.accessOf()
	if a.EffectiveRole() == store.RoleFull {
		return true
	}
	var probe struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(data, &probe) != nil {
		return false
	}
	return viewerMessageTypes[probe.Type] || (a.WatchPanes && watcherMessageTypes[probe.Type])
}

// viewerSnapshot cuts a snapshot down for a viewer. One that may not watch
// panes is sent no projects, tabs, panes or paths: the snapshot is the most
// sensitive thing on the control socket, and it is not end-to-end encrypted.
// One that may watch is sent the panes, but not what only the desk has a use
// for: the agent catalog, update and recall state, the relay's address, which
// other devices are watching which pane, and the offers to start agents and
// change panes.
func viewerSnapshot(full stateMsg, watch bool) stateMsg {
	out := stateMsg{
		Type:     "state",
		Projects: []projectView{},
		Tabs:     []tabView{},
		Panes:    map[string]paneView{},
		Agents:   agentCatalog{Items: []catalogAgent{}},
	}
	if !watch {
		return out
	}
	out.Root, out.ActiveTab = full.Root, full.ActiveTab
	out.Waiting, out.Working = full.Waiting, full.Working
	out.Projects, out.Tabs = full.Projects, full.Tabs
	out.CanSearchConversation = full.CanSearchConversation
	out.Panes = make(map[string]paneView, len(full.Panes))
	for id, pv := range full.Panes {
		pv.RemoteViewers, pv.RemoteInsecure = nil, false
		out.Panes[id] = pv
	}
	return out
}

// sendSnapshotTo gives a window a snapshot: the full one to a window that may
// have it, a cut-down one to a viewer, and to a viewer nothing it already has.
// It runs on the workspace goroutine, which owns lastViewerState.
//
// A viewer that may not watch is sent the same few bytes every time, and
// sending them again on every change of the desk's state would still tell it
// when the agents were busy.
func (s *Server) sendSnapshotTo(c *controlClient, snap stateMsg, full []byte) {
	a := s.accessOf(c)
	if a.EffectiveRole() == store.RoleFull {
		c.putState(full)
		return
	}
	data, err := json.Marshal(viewerSnapshot(snap, a.WatchPanes))
	if err != nil || string(data) == string(c.lastViewerState) {
		return
	}
	c.lastViewerState = data
	c.putState(data)
}

// deviceSockets tracks the open sockets of each remote device so that changing
// a device's role can end them.
type deviceSockets struct {
	mu   sync.Mutex
	next uint64
	by   map[string]map[uint64]func()
}

// add records a socket of a device. cut ends it. The function returned forgets
// it, for when the socket has closed on its own.
func (d *deviceSockets) add(device string, cut func()) (forget func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.by == nil {
		d.by = map[string]map[uint64]func(){}
	}
	d.next++
	id := d.next
	if d.by[device] == nil {
		d.by[device] = map[uint64]func(){}
	}
	d.by[device][id] = cut
	return func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		delete(d.by[device], id)
		if len(d.by[device]) == 0 {
			delete(d.by, device)
		}
	}
}

// cut ends every open socket of a device.
func (d *deviceSockets) cut(device string) {
	d.mu.Lock()
	cuts := make([]func(), 0, len(d.by[device]))
	for _, f := range d.by[device] {
		cuts = append(cuts, f)
	}
	d.mu.Unlock()
	for _, f := range cuts {
		f()
	}
}

// accessState is the Server's part of all this, kept in one place so the
// field list in server.go stays short.
type accessState struct {
	access  atomic.Pointer[accessTable]
	sockets deviceSockets
	// roster is how the account's paired devices are listed; nil asks the relay.
	// A test replaces it.
	roster func(context.Context) ([]string, error)
}

// validDeviceID reports whether a string can be a device id. The relay's ids are
// sixteen characters of lower-case base32 (a-z and 2-7), and it sets the header
// that carries one itself, after removing any the browser sent; this accepts
// more than that, base64url and a few separators included, but nothing that
// could not sit in a file name or a log line.
func validDeviceID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', strings.ContainsRune("-_.:", r):
		default:
			return false
		}
	}
	return true
}

// deskOnlyRole is why a window reached through the relay may not change what a
// device is allowed: otherwise a full device could free a viewer, or stop
// the desk's own phone, from anywhere the relay reaches.
const deskOnlyRole = "What a paired device may do is set on the machine Flockdeck runs on, not from a window reached through the relay"

// rosterTimeout bounds asking the relay which devices the account has.
const rosterTimeout = 15 * time.Second

// deviceRoster is the ids of the account's paired devices as the relay lists
// them now. A test replaces it.
func (s *Server) deviceRoster(ctx context.Context) ([]string, error) {
	if s.roster != nil {
		return s.roster(ctx)
	}
	cl, err := s.remoteClient()
	if err != nil {
		return nil, err
	}
	r, err := cl.Devices(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(r.Devices))
	for i, d := range r.Devices {
		ids[i] = d.ID
	}
	return ids, nil
}

// setDeviceRole records what a paired device may do: ID is the device, Kind is
// "full" or "viewer", and Watch lets a viewer also watch panes. It is the
// desk's alone and takes effect at once: the device's open sockets are ended,
// so it reconnects into the new role. Only a device the relay lists for this
// account can be limited, so the table cannot be filled with ids that name
// nothing; a limit is lifted whether or not the device is still listed.
func (s *Server) setDeviceRole(c *controlClient, cmd command) {
	if c.remote {
		c.notify(deskOnlyRole, true)
		return
	}
	role := store.Role(cmd.Kind)
	if role != store.RoleFull && role != store.RoleViewer {
		c.notify("A device is either full or a viewer", true)
		return
	}
	if !validDeviceID(cmd.ID) {
		c.notify("No device was named", true)
		return
	}
	device := cmd.ID
	access := store.DeviceAccess{Role: role, WatchPanes: role == store.RoleViewer && cmd.Watch}
	go func() {
		defer s.surviveFor(c, "setting what a device may do")
		if role == store.RoleViewer {
			ctx, cancel := context.WithTimeout(context.Background(), rosterTimeout)
			ids, err := s.deviceRoster(ctx)
			cancel()
			if err != nil {
				c.notify("Could not check that device with the relay, so it was not limited: "+err.Error(), true)
				return
			}
			if !slices.Contains(ids, device) {
				c.notify("That device is not paired any more", true)
				return
			}
		}
		s.applyDeviceRole(c, device, access)
	}()
}

// applyDeviceRole makes the change setDeviceRole was asked for, in memory first.
func (s *Server) applyDeviceRole(c *controlClient, device string, access store.DeviceAccess) {
	changed := false
	s.updateAccess(func(p *store.Prefs) bool {
		old, had := p.Devices[device]
		next, ok := p.WithAccess(device, access)
		if !ok {
			c.notify("Too many devices have their own setting; make some of them full again first", true)
			return false
		}
		now, has := next.Devices[device]
		if had == has && old == now {
			return false
		}
		*p = next
		changed = true
		return true
	}, func(saveErr error) {
		if !changed {
			return
		}
		// After the new record is published, so that a socket opened between
		// the two already sees it.
		s.sockets.cut(device)
		var text string
		switch {
		case access.EffectiveRole() == store.RoleFull:
			text = "That device is a full device again"
		case access.WatchPanes:
			text = "That device can now only view, and may watch panes"
		default:
			text = "That device can now only view artifacts"
		}
		if saveErr != nil {
			c.notify(text+". This is applied now, but could not be saved, so it will be lost when Flockdeck restarts: "+saveErr.Error(), true)
		} else {
			c.notify(text, false)
		}
		s.remoteDevices(c)
	})
}

// forgetRolesExcept drops the access record of every device missing from a
// roster the relay returned. Like the verified records, it is meant for a
// roster that was read successfully, and it does nothing for one that lists no
// devices: an empty answer is what a failed or hostile response looks like,
// and a restriction must not be lifted by it. A device unpaired from its own
// page leaves a harmless stale record until the next roster that has other
// devices in it.
func (s *Server) forgetRolesExcept(devices []remote.Device) {
	if len(devices) == 0 {
		return
	}
	ids := make([]string, len(devices))
	for i, d := range devices {
		ids[i] = d.ID
	}
	s.updateAccess(func(p *store.Prefs) bool {
		next, changed := p.WithoutDevicesNotIn(ids)
		if changed {
			*p = next
		}
		return changed
	}, nil)
}

// forgetRole drops the access record of a device that was unpaired.
func (s *Server) forgetRole(device string) {
	s.updateAccess(func(p *store.Prefs) bool {
		if _, ok := p.Devices[device]; !ok {
			return false
		}
		next, _ := p.WithAccess(device, store.DeviceAccess{Role: store.RoleFull})
		*p = next
		return true
	}, nil)
}

// mayPush reports whether a push notification may be sent to a device. A
// notification names a pane, its project and a link to it, so it goes only
// where that could be seen on screen: to a full device, and to a viewer the
// desk let watch panes. A viewer that sees artifacts only, a device the desk has
// no record of being full for while the record is unknown, and a push for no
// named device get nothing.
func (s *Server) mayPush(deviceID string) bool {
	a := s.deviceAccess(deviceID)
	return a.EffectiveRole() == store.RoleFull || a.WatchPanes
}

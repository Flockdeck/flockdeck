package store

import (
	"maps"
	"time"
)

// Role is what a paired device is allowed to do through Flockdeck Remote.
type Role string

const (
	// RoleFull is a device that can do everything a window on this machine can,
	// bar the few things that are the desk's alone. It is what every device is
	// until the desk says otherwise, so a device paired before roles existed
	// behaves as it always did.
	RoleFull Role = "full"
	// RoleViewer is a device that can look and cannot act. It is artifacts-only
	// unless the desk also lets it watch panes (DeviceAccess.WatchPanes).
	RoleViewer Role = "viewer"
)

// MaxDeviceAccess bounds how many devices the file keeps a role for, so a
// loop of pairings cannot grow it without end.
const MaxDeviceAccess = 256

// DeviceAccess is what the desk decided about one paired device.
type DeviceAccess struct {
	// Role is full or viewer. Anything else read from the file is a viewer: a
	// value this build does not understand is never read as more access.
	Role Role `json:"role"`
	// WatchPanes lets a viewer see the panes -- their names, tasks, replies,
	// terminal output and chat -- though still not type, resize or run
	// anything. It does nothing for a full device.
	WatchPanes bool `json:"watchPanes,omitempty"`
	// Approve lets the device answer a structured pane's permission request
	// from its phone. It is off for every device, full devices included: a
	// record that says nothing about it is off, and the "no record means full"
	// rule does not reach it. It is turned on at the desk, for one device, after
	// its key is verified and the person has agreed to ApproveAck's wording.
	Approve bool `json:"approve,omitempty"`
	// ApproveV is the version of the wording the person agreed to when they
	// turned Approve on, and ApproveAt when. A change to what the device is
	// shown raises the version the host asks for, and an older agreement then
	// no longer counts (ApproveOn).
	ApproveV  int       `json:"approveV,omitempty"`
	ApproveAt time.Time `json:"approveAt,omitzero"`
}

// ApproveOn reports whether the device may answer permission requests: it is
// switched on and the person agreed to the wording at version or later. Neither
// the role nor the key is looked at here; the host checks the key.
func (a DeviceAccess) ApproveOn(version int) bool {
	return a.Approve && a.ApproveV >= version
}

// EffectiveRole is the role the host enforces: viewer for anything that is not
// exactly full.
func (a DeviceAccess) EffectiveRole() Role {
	if a.Role == RoleFull {
		return RoleFull
	}
	return RoleViewer
}

// AccessFor is what is recorded for a device. A device with no record is full;
// that default is the compatibility rule for devices paired before roles. When
// the file was unreadable or damaged every device is a viewer, not full.
func (p Prefs) AccessFor(deviceID string) DeviceAccess {
	if p.accessLost {
		return DeviceAccess{Role: RoleViewer}
	}
	if a, ok := p.Devices[deviceID]; ok {
		return a
	}
	return DeviceAccess{Role: RoleFull}
}

// AccessUnknown says the file this was read from could not be trusted, so the
// record of device roles is not known and AccessFor answers viewer for all.
func (p Prefs) AccessUnknown() bool { return p.accessLost }

// KeepAccessUnknown marks the record of device roles as not known, so AccessFor
// answers viewer for every device.
func (p *Prefs) KeepAccessUnknown() { p.accessLost = true }

// AdoptAccess replaces the device roles in p with those in from, and whether
// they are known. The running program's copy is the one in force, so preferences
// read from the file again take it over before they are changed and written:
// a file that was deleted, damaged or edited while Flockdeck ran must not widen
// or narrow what any device may do.
func (p *Prefs) AdoptAccess(from Prefs) {
	p.Devices = maps.Clone(from.Devices)
	p.accessLost = from.accessLost
}

// WithoutDevicesNotIn returns the preferences with the access record of every
// device not in ids dropped, and whether anything was. It drops nothing for an
// empty ids: an empty list is what a failed, filtered or hostile answer looks
// like, and the cost of keeping a stale record is a slot in the table, while
// the cost of dropping a live one is a restricted device made full.
func (p Prefs) WithoutDevicesNotIn(ids []string) (Prefs, bool) {
	if len(ids) == 0 || len(p.Devices) == 0 || p.accessLost {
		return p, false
	}
	keep := make(map[string]bool, len(ids))
	for _, id := range ids {
		keep[id] = true
	}
	next := map[string]DeviceAccess{}
	for id, a := range p.Devices {
		if keep[id] {
			next[id] = a
		}
	}
	if len(next) == len(p.Devices) {
		return p, false
	}
	if len(next) == 0 {
		next = nil
	}
	p.Devices = next
	return p, true
}

// WithAccess returns the preferences with one device's access set. A full
// device with nothing else to say is dropped from the map rather than kept, so
// the file holds only the devices that differ from the default. It reports
// false when a new entry would go past MaxDeviceAccess.
func (p Prefs) WithAccess(deviceID string, a DeviceAccess) (Prefs, bool) {
	if a.EffectiveRole() == RoleFull {
		a.Role, a.WatchPanes = RoleFull, false
	}
	if !a.Approve {
		a.ApproveV, a.ApproveAt = 0, time.Time{}
	}
	next := maps.Clone(p.Devices)
	if a == (DeviceAccess{Role: RoleFull}) {
		delete(next, deviceID)
	} else {
		if _, had := next[deviceID]; !had && len(next) >= MaxDeviceAccess {
			return p, false
		}
		if next == nil {
			next = map[string]DeviceAccess{}
		}
		next[deviceID] = a
	}
	if len(next) == 0 {
		next = nil
	}
	p.Devices = next
	return p, true
}

// WithoutApprovals returns the preferences with approval switched off for every
// device, and the ids of the devices it was on for. Roles and watch settings
// are kept. A full device with nothing else to say is dropped from the map, as
// in WithAccess. It is the kill switch's change.
func (p Prefs) WithoutApprovals() (Prefs, []string) {
	var was []string
	next := maps.Clone(p.Devices)
	for id, a := range p.Devices {
		if !a.Approve && a.ApproveV == 0 && a.ApproveAt.IsZero() {
			continue
		}
		if a.Approve {
			was = append(was, id)
		}
		a.Approve, a.ApproveV, a.ApproveAt = false, 0, time.Time{}
		if a == (DeviceAccess{Role: RoleFull}) {
			delete(next, id)
		} else {
			next[id] = a
		}
	}
	if len(next) == 0 {
		next = nil
	}
	p.Devices = next
	return p, was
}

// ForWindow is the preferences as a window is sent them. The record of which
// device may do what is the desk's own business: a phone has no use for the
// list, and a viewer in particular should not learn which other devices are
// paired or how they are limited.
func (p Prefs) ForWindow() Prefs {
	p.Devices = nil
	// Project paths are not something a window needs for this.
	p.Knowledge = nil
	// Their env may hold secrets, and a window has no use for them.
	p.MCPServers = nil
	// An interface built before the blob reads the old fields.
	return p.withLegacyUI()
}

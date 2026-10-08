package store

import (
	"slices"
	"time"
)

// The kinds of thing a paired device may be allowed to view. Only the ones the
// host offers (see the server's artifact consent code) can be turned on.
const (
	ArtifactKindRecordings = "recordings"
	ArtifactKindLinks      = "links"
	ArtifactKindFiles      = "files"
)

// ArtifactAck records that the user was shown what viewing one kind remotely
// exposes, and agreed. Version is the wording's version: a change to what is
// exposed raises it, and an older acknowledgement then no longer counts.
type ArtifactAck struct {
	Version int       `json:"v"`
	At      time.Time `json:"at"`
}

// RemoteArtifactsPrefs is what the desk has allowed paired devices to view
// through the artifacts socket. Everything is off until the desk turns it on,
// and nothing here can be changed from a window reached through the relay.
//
// It is never sent to a window reached through the relay: the device list is
// the desk's business, and the control socket is readable by the relay.
type RemoteArtifactsPrefs struct {
	// Kinds says which kinds are switched on.
	Kinds map[string]bool `json:"kinds,omitempty"`
	// Acks is the acknowledgement for each kind, checked by the host before a
	// kind is switched on.
	Acks map[string]ArtifactAck `json:"ack,omitempty"`
	// Devices is the ids of the paired devices that may use artifacts at all.
	Devices []string `json:"devices,omitempty"`
}

// KindOn reports whether a kind is switched on, ignoring whether it was
// acknowledged.
func (a RemoteArtifactsPrefs) KindOn(kind string) bool { return a.Kinds[kind] }

// Acked reports whether kind was acknowledged at version or later.
func (a RemoteArtifactsPrefs) Acked(kind string, version int) bool {
	ack, ok := a.Acks[kind]
	return ok && ack.Version >= version
}

// HasDevice reports whether a device is on the allowlist.
func (a RemoteArtifactsPrefs) HasDevice(id string) bool {
	return id != "" && slices.Contains(a.Devices, id)
}

// SetKind switches a kind on or off and reports whether that changed it.
func (a *RemoteArtifactsPrefs) SetKind(kind string, on bool) bool {
	if a.Kinds[kind] == on {
		return false
	}
	if !on {
		delete(a.Kinds, kind)
		if len(a.Kinds) == 0 {
			a.Kinds = nil
		}
		return true
	}
	if a.Kinds == nil {
		a.Kinds = map[string]bool{}
	}
	a.Kinds[kind] = true
	return true
}

// SetAck records an acknowledgement.
func (a *RemoteArtifactsPrefs) SetAck(kind string, ack ArtifactAck) {
	if a.Acks == nil {
		a.Acks = map[string]ArtifactAck{}
	}
	a.Acks[kind] = ack
}

// AddDevice puts a device on the allowlist and reports whether that changed it.
func (a *RemoteArtifactsPrefs) AddDevice(id string) bool {
	if id == "" || a.HasDevice(id) {
		return false
	}
	a.Devices = append(a.Devices, id)
	return true
}

// RemoveDevice takes a device off the allowlist and reports whether that
// changed it.
func (a *RemoteArtifactsPrefs) RemoveDevice(id string) bool {
	i := slices.Index(a.Devices, id)
	if i < 0 {
		return false
	}
	a.Devices = slices.Delete(a.Devices, i, i+1)
	if len(a.Devices) == 0 {
		a.Devices = nil
	}
	return true
}

// AllOff switches every kind off, keeping the acknowledgements and the device
// list, and reports whether that changed anything.
func (a *RemoteArtifactsPrefs) AllOff() bool {
	if len(a.Kinds) == 0 {
		return false
	}
	a.Kinds = nil
	return true
}

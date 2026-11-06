package remote

import "github.com/jmwri/flockdeck/internal/store"

// HostInUse reports whether this machine is one a remote device can reach or
// has been set up to reach: remote access is on, or the desk holds a record of
// a paired device. Structured panes are the default only where it is false,
// because a phone cannot yet answer a structured pane's prompts (see
// store.Prefs.StructuredOn).
//
// It reads local files only, so it is cheap and works offline. The relay's list
// of paired devices is not asked: a machine with remote access turned off and
// no local record of a device reads as not in use, even if the account has
// devices paired on the relay.
//
// What is not known counts as in use. An enrolment that cannot be read might be
// on, and wrongly starting its panes structured is the failure to avoid.
func HostInUse(p store.Prefs) bool {
	cfg, err := Load()
	if err != nil {
		return true
	}
	if cfg != nil && !cfg.Disabled {
		return true
	}
	if len(p.Devices) > 0 {
		return true
	}
	var v verifiedStore
	v.mu.Lock()
	defer v.mu.Unlock()
	v.load()
	return len(v.recs) > 0
}

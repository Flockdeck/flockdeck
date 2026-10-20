package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
)

// ErrPrefsDamaged is what ReadWake returns when the preferences file is there
// but cannot be read as preferences, or was found so and moved aside.
var ErrPrefsDamaged = errors.New("the preferences file is damaged")

// wakeDamagedFile is a marker kept beside the preferences file. ReadPrefs
// writes it when it finds the file damaged and moves it aside; the file is not
// where it was on the next read, or after a restart, and without the marker the
// switches the user set would read as all on. ReadWake treats it as damage, and
// a wake switch set afterwards removes it (ClearWakeDamaged).
const wakeDamagedFile = "prefs.wake-damaged"

// wakeDamaged is the same fact in memory, set only when the marker could not
// be written. When the marker is there it is the marker that counts, so that a
// switch set by another process, which removes it, is seen here too.
var wakeDamaged atomic.Bool

// writeMarker is os.WriteFile, replaced by a test.
var writeMarker = os.WriteFile

func markWakeDamaged(dir string) {
	err := writeMarker(filepath.Join(dir, wakeDamagedFile), []byte("a damaged preferences file was moved aside"), 0o600)
	if err != nil {
		// A directory that cannot take the marker could hardly have taken the
		// preferences either; the flag holds for this run.
		wakeDamaged.Store(true)
	}
}

func wakeIsDamaged(dir string) bool {
	if wakeDamaged.Load() {
		return true
	}
	_, err := os.Stat(filepath.Join(dir, wakeDamagedFile))
	return err == nil
}

// ClearWakeDamaged says the user has set a wake switch since the preferences
// were found damaged, so wake is theirs to have on again. It removes the marker.
func ClearWakeDamaged() {
	wakeDamaged.Store(false)
	if dir, err := Dir(); err == nil {
		_ = os.Remove(filepath.Join(dir, wakeDamagedFile))
	}
}

// ReadWake reads only the wake switches from the preferences file, for the one
// reader that has to fail closed. It differs from ReadPrefs in two ways. It
// does not move a damaged file aside. And damage anywhere in the wake part of
// the file, or a file that is not a JSON object at all, is an error
// (ErrPrefsDamaged) with whatever switches did decode, where ReadPrefs gives
// the defaults, which turn wake on. A bad value elsewhere in the file (a theme
// of 5) leaves the wake switches readable, so it does not lose them. A file
// that is not there is the defaults, unless a damaged one was moved aside (a
// marker file records that, across restarts).
func ReadWake() (Prefs, error) {
	dir, err := Dir()
	if err != nil {
		return Prefs{}, err
	}
	data, err := readState(filepath.Join(dir, prefsFile))
	if errors.Is(err, fs.ErrNotExist) {
		if wakeIsDamaged(dir) {
			return Prefs{}, ErrPrefsDamaged
		}
		return Prefs{}, nil
	}
	if err != nil {
		return Prefs{}, err
	}
	wake, ok := wakeMembers(data)
	if !ok {
		return Prefs{}, ErrPrefsDamaged
	}
	var p Prefs
	// More than one is merged by the decoder, as ReadPrefs merges them, which
	// keeps an off switch that appears in either. The key is matched without
	// regard to case, as encoding/json does for ReadPrefs.
	for _, raw := range wake {
		if err := json.Unmarshal(raw, &p.Wake); err != nil {
			return p, ErrPrefsDamaged
		}
	}
	if len(wake) > 1 {
		// Two members of one name is not a file this program wrote.
		return p, ErrPrefsDamaged
	}
	if wakeIsDamaged(dir) {
		return p, ErrPrefsDamaged
	}
	return p, nil
}

// wakeMembers returns the values of the top-level members of data named wake,
// in any case, and false if data is not a JSON object.
func wakeMembers(data []byte) ([]json.RawMessage, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, false
	}
	var out []json.RawMessage
	for dec.More() {
		t, err := dec.Token()
		key, isKey := t.(string)
		if err != nil || !isKey {
			return nil, false
		}
		var raw json.RawMessage
		if dec.Decode(&raw) != nil {
			return nil, false
		}
		if strings.EqualFold(key, "wake") {
			out = append(out, raw)
		}
	}
	if t, err := dec.Token(); err != nil || t != json.Delim('}') {
		return nil, false
	}
	// Anything after the object is damage, as it is to json.Unmarshal.
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	return out, true
}

// Scopes of a wake switch. See WakePrefs.
const (
	WakeGlobal = "global"
	WakeAgent  = "agent"
	WakePane   = "pane"
)

// maxWakeOff is the most agent types, and the most panes, a WakePrefs lists.
// The desk sets these, so the cap is for a damaged file or a runaway script.
const maxWakeOff = 500

// WakePrefs are the off switches for typed wake, the line Flockdeck types into
// an idle agent's input when it has unread mail. Everything is on until one is
// switched off, so the zero value is the default and the file stays free of it.
// Any switch that is off wins: there is no way to turn a pane on that its type
// or the global switch has turned off.
type WakePrefs struct {
	// Off turns wake off for every pane.
	Off bool `json:"off,omitempty"`
	// Agents are the agent ids (claude, aider and so on) wake is off for.
	Agents []string `json:"agents,omitempty"`
	// Panes are the pane ids wake is off for.
	Panes []string `json:"panes,omitempty"`
}

// WakeEnabled says whether wake may type into the pane. agentID is the pane's
// agent type and may be empty.
func (p Prefs) WakeEnabled(agentID, paneID string) bool {
	w := p.Wake
	if w.Off {
		return false
	}
	if agentID != "" && slices.Contains(w.Agents, agentID) {
		return false
	}
	return paneID == "" || !slices.Contains(w.Panes, paneID)
}

// SetWake switches wake on or off for a scope and reports whether that changed
// anything. scope is WakeGlobal (id is ignored), WakeAgent (an agent id) or
// WakePane (a pane id); anything else, or an empty id where one is needed, changes
// nothing. Turning a scope on only clears that scope: a pane whose type is off
// stays off. The lists are replaced and not written into, because a Prefs is
// copied by value and the copies share them.
func (p *Prefs) SetWake(scope, id string, on bool) bool {
	switch scope {
	case WakeGlobal:
		if p.Wake.Off == !on {
			return false
		}
		p.Wake.Off = !on
		return true
	case WakeAgent:
		list, changed := withWakeOff(p.Wake.Agents, id, on)
		if changed {
			p.Wake.Agents = list
		}
		return changed
	case WakePane:
		list, changed := withWakeOff(p.Wake.Panes, id, on)
		if changed {
			p.Wake.Panes = list
		}
		return changed
	}
	return false
}

// withWakeOff returns list with id added (on false) or removed (on true), and
// whether that differs from list.
func withWakeOff(list []string, id string, on bool) ([]string, bool) {
	if id == "" || len(id) > 128 {
		return list, false
	}
	has := slices.Contains(list, id)
	switch {
	case on && has:
		return slices.DeleteFunc(slices.Clone(list), func(s string) bool { return s == id }), true
	case !on && !has && len(list) < maxWakeOff:
		return append(slices.Clone(list), id), true
	}
	return list, false
}

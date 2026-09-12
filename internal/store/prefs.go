package store

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

// Prefs is what the interface remembers about the person using it, as opposed
// to what it remembers about a workspace.
//
// It cannot live in the browser. The server binds a fresh random port on every
// run, so each run is a different origin as far as the browser is concerned,
// and anything kept in local storage is gone by the next one.
type Prefs struct {
	// HelpSeen records that the help has been opened at least once, which is
	// what keeps the first-run welcome from reappearing forever.
	HelpSeen bool `json:"helpSeen,omitempty"`
	// DismissedTips holds the ids of the inline hints that have been sent
	// away. A hint that is dismissed should stay dismissed.
	DismissedTips []string `json:"dismissedTips,omitempty"`
	// FontSize is the size the terminals are drawn at, in pixels. Zero is the
	// default size.
	FontSize int `json:"fontSize,omitempty"`
	// NotificationsOff stops the desktop notification raised when an agent
	// stops to wait while the window is behind something else.
	NotificationsOff bool `json:"notificationsOff,omitempty"`
	// Scrollback is how many lines each terminal keeps once they have
	// scrolled off the top. Zero is the default.
	Scrollback int `json:"scrollback,omitempty"`
	// UpdatesOff stops the background check for a new release, as the
	// FLOCKDECK_UPDATE=off environment variable does.
	UpdatesOff bool `json:"updatesOff,omitempty"`
	// CursorSteady stops the terminal cursors blinking.
	CursorSteady bool `json:"cursorSteady,omitempty"`
	// FontFamily is the typeface the terminals are drawn in, as a CSS font
	// family list. Empty is the default.
	FontFamily string `json:"fontFamily,omitempty"`
}

// Dismissed reports whether a hint has been sent away.
func (p Prefs) Dismissed(id string) bool {
	for _, t := range p.DismissedTips {
		if t == id {
			return true
		}
	}
	return false
}

// Dismiss records that a hint has been sent away, and reports whether that
// changed anything.
func (p *Prefs) Dismiss(id string) bool {
	if id == "" || p.Dismissed(id) {
		return false
	}
	p.DismissedTips = append(p.DismissedTips, id)
	return true
}

const prefsFile = "prefs.json"

// LoadPrefs reads the preferences. Absent or damaged, they are the defaults:
// nothing here is worth failing a start-up over.
func LoadPrefs() Prefs {
	dir, err := Dir()
	if err != nil {
		return Prefs{}
	}
	data, err := readState(filepath.Join(dir, prefsFile))
	if err != nil {
		return Prefs{}
	}
	var p Prefs
	if json.Unmarshal(data, &p) != nil {
		// Still the defaults, but the file is moved aside first, as every
		// other damaged state file is: the next hint dismissed rewrites it
		// from what was read, and what was read is nothing.
		quarantine(filepath.Join(dir, prefsFile))
		return Prefs{}
	}
	return p
}

// SavePrefs writes the preferences.
func SavePrefs(p Prefs) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("encode prefs: %w", err)
	}
	if err := writeAtomic(filepath.Join(dir, prefsFile), data); err != nil {
		return fmt.Errorf("write prefs: %w", err)
	}
	return nil
}

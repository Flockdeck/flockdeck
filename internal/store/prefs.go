package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
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
	// ScreenReader has every terminal keep an accessible copy of its lines,
	// which is what lets a screen reader read what the agents write. It costs
	// every terminal some speed, so it is off until it is asked for.
	ScreenReader bool `json:"screenReader,omitempty"`
	// CursorStyle is the shape of the terminal cursors: "bar" or
	// "underline". Empty is the default, a block.
	CursorStyle string `json:"cursorStyle,omitempty"`
	// FontFamily is the typeface the terminals are drawn in, as a CSS font
	// family list. Empty is the default.
	FontFamily string `json:"fontFamily,omitempty"`
	// RailExpanded shows the rail as a panel of icons and names, rather than
	// icons alone. False is the default, the rail as it has always been.
	RailExpanded bool `json:"railExpanded,omitempty"`
	// RailWidth is how wide the rail is drawn while it is expanded, in
	// pixels. Zero is the default width.
	RailWidth int `json:"railWidth,omitempty"`
	// Spend is how the pane headers show what the agents spend and how near
	// they are to their limits. Left out of the file while it is all defaults.
	Spend SpendPrefs `json:"spend,omitzero"`
	// Push is what the relay is asked to send the account's paired devices
	// when an agent has been waiting a while. Left out of the file while it is
	// all defaults.
	Push PushPrefs `json:"push,omitzero"`
	// Theme is which palette the window is drawn in: "dark" (the default,
	// unchanged from before this existed), "light", or "system", which follows
	// prefers-color-scheme. Empty means "dark", so a file written before this
	// existed reads the same as it always looked.
	Theme string `json:"theme,omitempty"`
	// AccentColor is one of a small fixed set of swatches, by name, rather than
	// a free colour: a colour picked by hand could go illegible against either
	// palette. Empty is the default blue.
	AccentColor string `json:"accentColor,omitempty"`
	// FanOut is the default the fan-out dialog's own preference-worthy
	// checkbox starts on. Left out of the file while it is all defaults.
	FanOut FanOutPrefs `json:"fanOut,omitzero"`
	// AutoReviewDefault is the value Pane.AutoReview starts at for a pane with
	// no parent -- one opened by hand, or a fresh row of a fan-out run from the
	// window -- rather than inherited from a parent that spawned it with its
	// own `flockdeck spawn`. Off by default, as the switch itself is: a restart
	// still asks about everything until this, or the pane's own switch, says
	// otherwise. See workspace.Pane.AutoReview.
	AutoReviewDefault bool `json:"autoReviewDefault,omitempty"`

	// extra is every top-level key the file held that this build does not
	// know, as it was written. A newer build's setting would otherwise go at
	// the first change made here, since the file is written back whole: step
	// back a version, dismiss a hint, and stepping forward again found that
	// setting gone. It is not sent to the windows, which would not know it
	// either.
	extra map[string]json.RawMessage
}

// prefsKeys are the top-level keys Prefs decodes itself; every other key in
// the file is kept in extra.
var prefsKeys = func() []string {
	t := reflect.TypeFor[Prefs]()
	var keys []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" {
			name = f.Name
		}
		if name != "-" {
			keys = append(keys, name)
		}
	}
	return keys
}()

// unknownPrefs returns the top-level keys of a preferences file that Prefs
// does not decode, or nil when there are none.
func unknownPrefs(data []byte) map[string]json.RawMessage {
	var all map[string]json.RawMessage
	if json.Unmarshal(data, &all) != nil {
		return nil
	}
	for _, name := range prefsKeys {
		delete(all, name)
	}
	if len(all) == 0 {
		return nil
	}
	return all
}

// PushPrefs are the preferences for push notifications to paired devices.
// Each is kept as nothing while it is the default, so a file written before
// there were any reads the same.
type PushPrefs struct {
	// Off stops asking the relay to notify the paired devices.
	Off bool `json:"off,omitempty"`
	// Anonymous has a notification say only that an agent on this machine
	// needs you, rather than naming the pane and its project.
	Anonymous bool `json:"anonymous,omitempty"`
	// DelaySeconds is how long a pane has to have been waiting before the
	// devices are told. Zero is the default, 30 seconds.
	DelaySeconds int `json:"delaySeconds,omitempty"`
}

// FanOutPrefs are the preferences the fan-out dialog starts on.
type FanOutPrefs struct {
	// SameTab starts the dialog's "Put them in this tab, beside the agent
	// that planned them" checkbox ticked, so a fan-out's children land beside
	// their parent instead of in a new tab of their own. False, its zero
	// value, is a new tab -- the dialog's own default before this existed, so
	// a file written before this existed still means what it always meant.
	SameTab bool `json:"sameTab,omitempty"`
}

// SpendPrefs are the preferences for spend and limits.
type SpendPrefs struct {
	// StatusLine says when a Claude pane's status line is routed through
	// Flockdeck, which is how its subscription limits are read: "on", "off",
	// or empty for only where the user has a status line of their own, which
	// it keeps. Those are the session.StatusLine constants.
	StatusLine string `json:"statusLine,omitempty"`
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

// prefsWhat is how the user is told of the preferences file.
const (
	prefsFile = "prefs.json"
	prefsWhat = "the preferences"
)

// LoadPrefs reads the preferences. Absent, damaged or unreadable, they are the
// defaults: nothing here is worth failing a start-up over.
func LoadPrefs() Prefs {
	p, _ := ReadPrefs()
	return p
}

// ReadPrefs is LoadPrefs for a caller about to write the preferences back with
// one change made, which has to know whether what it read is what is there.
//
// Absent or damaged, they are the defaults and no error: there is nothing
// there to lose. A file that could not be read is the defaults too, with the
// read's own failure, because written back those defaults and one change
// would stand in for every preference the user had set. The save would move
// the file aside first and keep it, but the settings would still be gone from
// the window until somebody went looking for the copy.
func ReadPrefs() (Prefs, error) {
	dir, err := Dir()
	if err != nil {
		return Prefs{}, err
	}
	file := filepath.Join(dir, prefsFile)
	data, err := readState(file)
	// Read or not, the defaults are what this run goes on with; one that could
	// not be read is kept from the next save, which would otherwise write
	// those defaults over every preference the user had set.
	noteRead(file, err)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Prefs{}, nil
		}
		return Prefs{}, fmt.Errorf("read prefs: %w", err)
	}
	var p Prefs
	if json.Unmarshal(data, &p) != nil {
		// Still the defaults, but the file is moved aside first, as every
		// other damaged state file is: the next hint dismissed rewrites it
		// from what was read, and what was read is nothing.
		quarantine(filepath.Join(dir, prefsFile), prefsWhat, KeptDamaged)
		return Prefs{}, nil
	}
	p.extra = unknownPrefs(data)
	return p, nil
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
	// What a newer build wrote goes back beside what this one knows. A key
	// this build knows is never taken from there: one it has left out of the
	// file is a setting put back to its default, and must stay out.
	if len(p.extra) > 0 {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(data, &m); err != nil {
			return fmt.Errorf("encode prefs: %w", err)
		}
		for name, raw := range p.extra {
			if !slices.Contains(prefsKeys, name) {
				m[name] = raw
			}
		}
		if data, err = json.MarshalIndent(m, "", "  "); err != nil {
			return fmt.Errorf("encode prefs: %w", err)
		}
	}
	if err := keepUnread(filepath.Join(dir, prefsFile), prefsWhat); err != nil {
		return fmt.Errorf("write prefs: %w", err)
	}
	if err := writeAtomic(filepath.Join(dir, prefsFile), data); err != nil {
		return fmt.Errorf("write prefs: %w", err)
	}
	return nil
}

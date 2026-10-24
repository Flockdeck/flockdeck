package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
)

// The interface's own preferences are kept as one opaque object, Prefs.UI: the
// server stores it and sends it back and does not read what is in it. A setting
// the interface invents needs no change here. What the server must read itself
// (a hint dismissed for a knowledge tip, the switches of typed wake) keeps a
// field of its own.
//
// The blob is changed by merging a patch into it, key by key, so two windows
// changing different settings do not undo each other. A key given null is
// removed. The limits below keep a window from filling the preferences file.
const (
	// MaxUIKeys is how many keys the blob may hold.
	MaxUIKeys = 64
	// MaxUIValueBytes is the most one value may be, as the JSON it is sent in.
	MaxUIValueBytes = 4 << 10
	// MaxUIBytes is the most all the values may be together.
	MaxUIBytes = 32 << 10
)

// uiKey is what a key may look like: a letter, then letters, digits and . _ -.
var uiKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,63}$`)

// The keys the interface already used as fields of Prefs. They are in the blob
// now; the fields are still read (to migrate a file written before) and are
// still sent, filled from the blob, so that an older interface keeps working.
const (
	UIFontSize   = "fontSize"
	UIFontFamily = "fontFamily"
	UIHelpSeen   = "helpSeen"
	UIScrollback = "scrollback"
)

// PatchUI merges a patch into the interface's preferences and reports whether
// that changed them. It refuses a key or value outside the limits and then
// changes nothing. The map is replaced, not changed in place, because Prefs is
// copied by value and the copies share it.
func (p *Prefs) PatchUI(patch map[string]json.RawMessage) (bool, error) {
	next := make(map[string]json.RawMessage, len(p.UI)+len(patch))
	for k, v := range p.UI {
		next[k] = v
	}
	changed := false
	for k, raw := range patch {
		if !uiKey.MatchString(k) {
			return false, fmt.Errorf("ui preference name %q is not allowed", clip(k, 24))
		}
		v := bytes.TrimSpace(raw)
		if len(v) == 0 {
			return false, fmt.Errorf("ui preference %q has no value", k)
		}
		if len(v) > MaxUIValueBytes {
			return false, fmt.Errorf("ui preference %q is over %d bytes", k, MaxUIValueBytes)
		}
		if !json.Valid(v) {
			return false, fmt.Errorf("ui preference %q is not JSON", k)
		}
		if bytes.Equal(v, []byte("null")) {
			if _, had := next[k]; had {
				delete(next, k)
				changed = true
			}
			continue
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, v); err != nil {
			return false, fmt.Errorf("ui preference %q is not JSON", k)
		}
		if old, had := next[k]; had && bytes.Equal(old, compact.Bytes()) {
			continue
		}
		next[k] = json.RawMessage(compact.Bytes())
		changed = true
	}
	if len(next) > MaxUIKeys {
		return false, fmt.Errorf("ui preferences are over %d names", MaxUIKeys)
	}
	total := 0
	for _, v := range next {
		total += len(v)
	}
	if total > MaxUIBytes {
		return false, fmt.Errorf("ui preferences are over %d bytes", MaxUIBytes)
	}
	if !changed {
		return false, nil
	}
	if len(next) == 0 {
		next = nil
	}
	p.UI = next
	return true, nil
}

// SetUIValue sets one key of the interface's preferences to a value, for the
// commands that predate uiPrefs.
func (p *Prefs) SetUIValue(key string, value any) bool {
	raw, err := json.Marshal(value)
	if err != nil {
		return false
	}
	changed, err := p.PatchUI(map[string]json.RawMessage{key: raw})
	return err == nil && changed
}

// uiInt and uiBool read a key as a number or a flag, and say whether it was
// there and of that kind.
func (p Prefs) uiInt(key string) (int, bool) {
	raw, ok := p.UI[key]
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(string(raw))
	return n, err == nil
}

func (p Prefs) uiString(key string) (string, bool) {
	raw, ok := p.UI[key]
	if !ok {
		return "", false
	}
	var s string
	return s, json.Unmarshal(raw, &s) == nil
}

func (p Prefs) uiBool(key string) (bool, bool) {
	raw, ok := p.UI[key]
	if !ok {
		return false, false
	}
	return string(raw) == "true", string(raw) == "true" || string(raw) == "false"
}

// migrateUI moves the settings that were fields of Prefs into the blob, where it
// has none of its own for them, and clears the fields so the file is written
// without them. It runs when the file is read. A value the blob already has wins:
// it is the newer.
func (p *Prefs) migrateUI() {
	move := func(key string, value any, set bool) {
		if !set {
			return
		}
		if _, has := p.UI[key]; !has {
			p.SetUIValue(key, value)
		}
	}
	move(UIFontSize, p.FontSize, p.FontSize != 0)
	move(UIFontFamily, p.FontFamily, p.FontFamily != "")
	move(UIHelpSeen, true, p.HelpSeen)
	move(UIScrollback, p.Scrollback, p.Scrollback != 0)
	p.FontSize, p.FontFamily, p.HelpSeen, p.Scrollback = 0, "", false, 0
}

// withLegacyUI fills the old fields from the blob. A window built before the
// blob existed reads these, so it still sees the size and the welcome it was
// given. They are not written to the file.
func (p Prefs) withLegacyUI() Prefs {
	if n, ok := p.uiInt(UIFontSize); ok {
		p.FontSize = n
	}
	if s, ok := p.uiString(UIFontFamily); ok {
		p.FontFamily = s
	}
	if b, ok := p.uiBool(UIHelpSeen); ok {
		p.HelpSeen = b
	}
	if n, ok := p.uiInt(UIScrollback); ok {
		p.Scrollback = n
	}
	return p
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

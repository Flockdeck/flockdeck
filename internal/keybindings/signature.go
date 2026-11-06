package keybindings

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// keyNames maps a binding's own spelling of a few keys, as help/keys.go and
// the settings' capture field both write them, to the name signature keys
// them by. It is the Go twin of app.js's KEY_NAMES; the two must agree, or a
// binding that looks like a conflict in one would not in the other.
var keyNames = map[string]string{
	"←": "arrowleft",
	"→": "arrowright",
	"↑": "arrowup",
	"↓": "arrowdown",
}

// signatureOf turns a binding as it is written for a reader ("Ctrl+Shift+D")
// into the shape two bindings are compared by, or "" for one with no chord
// that can be typed as a single keystroke: no binding at all, or a range like
// selectTab's "Alt+1 … Alt+9", which expandInlineKeysWith already refuses to
// treat as one binding.
func signatureOf(keys string) string {
	if keys == "" || strings.Contains(keys, "…") {
		return ""
	}
	parts := strings.Split(keys, "+")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	name := parts[len(parts)-1]
	if name == "" {
		return ""
	}
	var ctrl, shift, alt bool
	for _, p := range parts[:len(parts)-1] {
		switch strings.ToLower(p) {
		case "ctrl":
			ctrl = true
		case "shift":
			shift = true
		case "alt":
			alt = true
		default:
			// A modifier neither this nor app.js's signatureOf recognises:
			// there is nothing sound to compare it as, so it is left to
			// collide with nothing rather than guessed at.
			return ""
		}
	}
	key, ok := keyNames[name]
	if !ok {
		key = strings.ToLower(name)
	}
	return signature(ctrl, shift, alt, key)
}

// signature is the comparable form of a chord's modifiers and key, matching
// app.js's own function of the same name byte for byte.
func signature(ctrl, shift, alt bool, key string) string {
	sig := ""
	if ctrl {
		sig += "c"
	}
	if shift {
		sig += "s"
	}
	if alt {
		sig += "a"
	}
	return sig + ":" + key
}

// namedKeys are the spellings of keys that are more than one character, as
// signatureOf lower-cases them: the arrows in either of their spellings and
// the editing and navigation keys. Function keys are checked separately.
var namedKeys = map[string]bool{
	"enter": true, "escape": true, "esc": true, "tab": true, "space": true,
	"backspace": true, "delete": true, "insert": true, "home": true, "end": true,
	"pageup": true, "pagedown": true,
	"arrowleft": true, "arrowright": true, "arrowup": true, "arrowdown": true,
	"←": true, "→": true, "↑": true, "↓": true,
}

// isFunctionKey reports whether name (any case) is F1 to F24.
func isFunctionKey(name string) bool {
	n := strings.ToLower(name)
	if len(n) < 2 || len(n) > 3 || n[0] != 'f' {
		return false
	}
	v := 0
	for _, c := range n[1:] {
		if c < '0' || c > '9' {
			return false
		}
		v = v*10 + int(c-'0')
	}
	return v >= 1 && v <= 24 && n[1] != '0'
}

// validChord reports whether keys is a binding a window can type as one
// keystroke and that does not take over ordinary typing: modifiers from Ctrl,
// Alt and Shift (each at most once), then one key, with Ctrl or Alt among the
// modifiers unless the key is a function key. It is the check the settings
// dialog makes before it sends a binding, made again here because the server
// is what saves it, for a window on this machine and one reached through the
// relay alike: a bare letter saved as a binding is claimed from every
// terminal, and a spelling signatureOf does not read (Cmd+K, Control+K) is a
// binding nothing can press.
func validChord(keys string) bool {
	if keys == "" || strings.Contains(keys, "…") || keys != strings.TrimSpace(keys) {
		return false
	}
	mods, key := "", keys
	if i := strings.LastIndex(keys, "+"); i >= 0 {
		mods, key = keys[:i], keys[i+1:]
		if key == "" { // the key is "+" itself: "Ctrl++"
			if !strings.HasSuffix(keys, "++") {
				return false
			}
			key = "+"
			mods = strings.TrimSuffix(mods, "+")
		}
		if mods == "" {
			return false
		}
	}
	var ctrl, shift, alt bool
	if mods != "" {
		for _, m := range strings.Split(mods, "+") {
			switch strings.ToLower(m) {
			case "ctrl":
				if ctrl {
					return false
				}
				ctrl = true
			case "shift":
				if shift {
					return false
				}
				shift = true
			case "alt":
				if alt {
					return false
				}
				alt = true
			default:
				return false
			}
		}
	}
	fn := isFunctionKey(key)
	switch {
	case fn, namedKeys[strings.ToLower(key)]:
	case utf8.RuneCountInString(key) == 1 && key != " ":
		r, _ := utf8.DecodeRuneInString(key)
		if !unicode.IsPrint(r) {
			return false
		}
	default:
		return false
	}
	return fn || ctrl || alt
}

package keybindings

import "strings"

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

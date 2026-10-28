package bridge

import (
	"bytes"
	"encoding/json"
	"strings"
)

// The notes a card can carry about its call, as fixed codes. A note says the
// request does something the card does not show, so a reader that cannot show it
// must not let it be allowed unseen. They are made from the shape of the agent's
// input and hold none of its text.
const (
	// NoteSandbox: the call asks to run outside the sandbox
	// (dangerouslyDisableSandbox is not false).
	NoteSandbox = "sandbox"
	// NoteBackground: the call runs in the background (run_in_background is not
	// false).
	NoteBackground = "background"
	// NoteReplaceAll: an edit replaces every match, not one (replace_all is not
	// false).
	NoteReplaceAll = "replace_all"
	// NoteExtra: a command has input the card does not show: a key outside the
	// ones the adapter is known to send, or input that is not an object.
	NoteExtra = "extra_input"
)

// defaultShownInput are the keys of a command's input that are known and shown
// or harmless, for an adapter that Config.ShownInput says nothing of.
var defaultShownInput = []string{"command", "description", "timeout", "cwd"}

// cardInput is what a card reads of the agent's input for a call.
type cardInput struct {
	// command is the value of the key "command", when it is a string.
	command string
	// altered says the command may not be the one a reader of the input would
	// take: the key is repeated, or another key differs from it in case only.
	// Go's own decoding takes the last of a repeated key and matches keys
	// without regard to case, so each of those can show a different command from
	// the one an adapter reads.
	altered bool
	// cwd is the value of the key "cwd", when it is a string and is shown.
	cwd   string
	notes []string
}

// readInput reads the input of a call. shown are the keys of a command that are
// known (Config.ShownInput); execute says the call is a command, the only kind
// whose unknown keys are noted.
func readInput(raw json.RawMessage, shown []string, execute bool) cardInput {
	var in cardInput
	if len(bytes.TrimSpace(raw)) == 0 {
		return in
	}
	type field struct {
		key string
		val json.RawMessage
	}
	var fields []field
	dec := json.NewDecoder(bytes.NewReader(raw))
	ok := false
	if t, err := dec.Token(); err == nil && t == json.Delim('{') {
		ok = true
		for dec.More() {
			kt, err := dec.Token()
			key, isStr := kt.(string)
			if err != nil || !isStr {
				ok = false
				break
			}
			var v json.RawMessage
			if dec.Decode(&v) != nil {
				ok = false
				break
			}
			fields = append(fields, field{key, v})
		}
		if ok {
			if t, err := dec.Token(); err != nil || t != json.Delim('}') {
				ok = false
			}
			if _, err := dec.Token(); err == nil {
				ok = false // something after the object
			}
		}
	}
	if !ok {
		if execute {
			in.notes = append(in.notes, NoteExtra)
		}
		return in
	}
	if shown == nil {
		shown = defaultShownInput
	}
	has := func(set []string, k string) bool {
		for _, s := range set {
			if s == k {
				return true
			}
		}
		return false
	}
	notFalse := func(v json.RawMessage) bool {
		s := string(bytes.TrimSpace(v))
		return s != "false" && s != "null"
	}
	seen := map[string]bool{}
	var sandbox, background, replaceAll, extra bool
	for _, f := range fields {
		if seen[f.key] {
			if strings.EqualFold(f.key, "command") {
				in.altered = true
			}
			extra = extra || execute
		}
		seen[f.key] = true
		switch {
		case f.key == "command":
			var s string
			if json.Unmarshal(f.val, &s) == nil {
				in.command = s
			}
		case strings.EqualFold(f.key, "command"):
			in.altered = true
		case f.key == "cwd" && has(shown, "cwd"):
			var s string
			if json.Unmarshal(f.val, &s) == nil {
				in.cwd = s
			}
		case strings.EqualFold(f.key, "dangerouslyDisableSandbox"):
			sandbox = sandbox || notFalse(f.val)
		case strings.EqualFold(f.key, "run_in_background"):
			background = background || notFalse(f.val)
		case strings.EqualFold(f.key, "replace_all"):
			replaceAll = replaceAll || notFalse(f.val)
		case execute && !has(shown, f.key):
			extra = true
		}
	}
	if sandbox {
		in.notes = append(in.notes, NoteSandbox)
	}
	if background {
		in.notes = append(in.notes, NoteBackground)
	}
	if replaceAll {
		in.notes = append(in.notes, NoteReplaceAll)
	}
	if extra {
		in.notes = append(in.notes, NoteExtra)
	}
	return in
}

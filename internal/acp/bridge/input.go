package bridge

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"unicode/utf8"
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

// field is one member of a JSON object, in the order it was sent.
type field struct {
	key string
	val json.RawMessage
}

// parseObject reads raw as exactly one JSON object and returns its members in
// order, repeats included. ok is false for anything else: another kind of value,
// a damaged object, or text after it.
func parseObject(raw json.RawMessage) (fields []field, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, false
	}
	for dec.More() {
		kt, err := dec.Token()
		key, isStr := kt.(string)
		if err != nil || !isStr {
			return nil, false
		}
		var v json.RawMessage
		if dec.Decode(&v) != nil {
			return nil, false
		}
		fields = append(fields, field{key, v})
	}
	if t, err := dec.Token(); err != nil || t != json.Delim('}') {
		return nil, false
	}
	if _, err := dec.Token(); err == nil {
		return nil, false // something after the object
	}
	return fields, true
}

// editKeys are the keys of one element of a MultiEdit's "edits".
var editKeys = []string{"old_string", "new_string", "replace_all"}

// readEdits reads the "edits" of a MultiEdit: replaceAll says an element replaces
// every match, extra says an element is not an object of the known keys, or the
// value is not a list of them.
func readEdits(raw json.RawMessage) (replaceAll, extra bool) {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		return false, true
	}
	for _, el := range list {
		fields, ok := parseObject(el)
		if !ok {
			extra = true
			continue
		}
		seen := map[string]bool{}
		for _, f := range fields {
			if seen[f.key] {
				extra = true
			}
			seen[f.key] = true
			switch {
			case f.key == "replace_all" || strings.EqualFold(f.key, "replace_all"):
				replaceAll = replaceAll || notFalse(f.val)
				extra = extra || f.key != "replace_all"
			case !slices.Contains(editKeys, f.key):
				extra = true
			}
		}
	}
	return replaceAll, extra
}

// notFalse says a JSON flag is set: any value but false or null.
func notFalse(v json.RawMessage) bool {
	s := string(bytes.TrimSpace(v))
	return s != "false" && s != "null"
}

// readInput reads the input of a call. shown are the keys of a command that are
// known (Config.ShownInput); execute says the call is a command, the only kind
// whose unknown keys are noted.
func readInput(raw json.RawMessage, shown []string, execute bool) cardInput {
	var in cardInput
	if len(bytes.TrimSpace(raw)) == 0 {
		return in
	}
	fields, ok := parseObject(raw)
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
				// A lone surrogate decodes to U+FFFD, which is no longer what was sent.
				in.altered = in.altered || strings.ContainsRune(s, utf8.RuneError)
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
		case strings.EqualFold(f.key, "edits"):
			r, x := readEdits(f.val)
			replaceAll, extra = replaceAll || r, extra || x || f.key != "edits"
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

package bridge

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
)

func inputCard(t *testing.T, cfg Config, kind, raw string) Card {
	t.Helper()
	b, _ := newBridge(cfg)
	return b.PermissionOpen(acp.PermissionRequest{RequestID: "r", SessionID: "s",
		ToolCall: acp.ToolCall{ToolCallID: "c", Title: "t", Kind: kind, RawInput: json.RawMessage(raw)}})
}

// The command is read from the key "command" exactly. A repeated key, or a key
// that differs in case only, is something an adapter's own decoder may read
// differently from this one, so the card says the command may not be the one run.
func TestReadInputTakesTheCommandKeyExactly(t *testing.T) {
	for name, tc := range map[string]struct {
		raw     string
		command string
		altered bool
	}{
		"plain":            {`{"command":"ls"}`, "ls", false},
		"repeated":         {`{"command":"ls","command":"rm x"}`, "rm x", true},
		"repeated, benign": {`{"command":"ls","timeout":5,"timeout":6}`, "ls", false},
		"upper case":       {`{"Command":"rm x"}`, "", true},
		"upper beside":     {`{"command":"ls","COMMAND":"rm x"}`, "ls", true},
		"number":           {`{"command":5}`, "", false},
		"not an object":    {`["command"]`, "", false},
		"after the object": {`{"command":"ls"} {"command":"rm x"}`, "", false},
		"empty":            {``, "", false},
	} {
		t.Run(name, func(t *testing.T) {
			in := readInput(json.RawMessage(tc.raw), nil, true)
			if in.command != tc.command || in.altered != tc.altered {
				t.Errorf("command %q altered %v, want %q altered %v", in.command, in.altered, tc.command, tc.altered)
			}
		})
	}
}

// Input a card does not show is noted, for a command and by key, so that a
// reader that cannot show it does not let it be allowed unseen.
func TestReadInputNotesWhatTheCardDoesNotShow(t *testing.T) {
	own := []string{"command", "description", "timeout"}
	for name, tc := range map[string]struct {
		raw     string
		shown   []string
		execute bool
		notes   []string
	}{
		"nothing more":        {`{"command":"ls","description":"d","timeout":5}`, own, true, nil},
		"unknown key":         {`{"command":"ls","env":{"A":"b"}}`, own, true, []string{NoteExtra}},
		"cwd, not listed":     {`{"command":"ls","cwd":"/x"}`, own, true, []string{NoteExtra}},
		"cwd, listed":         {`{"command":"ls","cwd":"/x"}`, []string{"command", "cwd"}, true, nil},
		"sandbox":             {`{"command":"ls","dangerouslyDisableSandbox":true}`, own, true, []string{NoteSandbox}},
		"sandbox off":         {`{"command":"ls","dangerouslyDisableSandbox":false}`, own, true, nil},
		"sandbox as text":     {`{"command":"ls","dangerouslyDisableSandbox":"false"}`, own, true, []string{NoteSandbox}},
		"sandbox, other case": {`{"command":"ls","DangerouslyDisableSandbox":1}`, own, true, []string{NoteSandbox}},
		"background":          {`{"command":"ls","run_in_background":true}`, own, true, []string{NoteBackground}},
		"background null":     {`{"command":"ls","run_in_background":null}`, own, true, nil},
		"replace all":         {`{"file_path":"a","replace_all":true}`, own, false, []string{NoteReplaceAll}},
		"replace all off":     {`{"file_path":"a","replace_all":false}`, own, false, nil},
		"an edit's keys":      {`{"file_path":"a","old_string":"b","new_string":"c"}`, own, false, nil},
		"repeated key":        {`{"command":"ls","timeout":1,"timeout":2}`, own, true, []string{NoteExtra}},
		"not an object":       {`"ls"`, own, true, []string{NoteExtra}},
		"not an object, edit": {`"ls"`, own, false, nil},
		"default keys":        {`{"command":"ls","cwd":"/x"}`, nil, true, nil},
		"all three":           {`{"command":"ls","dangerouslyDisableSandbox":true,"run_in_background":true,"x":1}`, own, true, []string{NoteSandbox, NoteBackground, NoteExtra}},
	} {
		t.Run(name, func(t *testing.T) {
			in := readInput(json.RawMessage(tc.raw), tc.shown, tc.execute)
			if !slices.Equal(in.notes, tc.notes) {
				t.Errorf("notes = %v, want %v", in.notes, tc.notes)
			}
		})
	}
}

func TestACardCarriesTheDirectoryAndNotes(t *testing.T) {
	cfg := Config{ShownInput: []string{"command", "cwd"}}
	c := inputCard(t, cfg, "execute", `{"command":"ls","cwd":"/work/api","run_in_background":true}`)
	if c.Cwd != "/work/api" || !slices.Equal(c.Notes, []string{NoteBackground}) {
		t.Errorf("Cwd %q Notes %v", c.Cwd, c.Notes)
	}
	// A directory that cleaning changed is not the one named, and says so.
	c = inputCard(t, cfg, "execute", "{\"command\":\"ls\",\"cwd\":\"/work\\u202e/api\"}")
	if c.Cwd != "/work/api" || !slices.Contains(c.Notes, NoteExtra) {
		t.Errorf("Cwd %q Notes %v, want the directory cleaned and the extra note", c.Cwd, c.Notes)
	}
	// Without the key listed the directory is not shown, and is noted.
	c = inputCard(t, Config{ShownInput: []string{"command"}}, "execute", `{"command":"ls","cwd":"/work/api"}`)
	if c.Cwd != "" || !slices.Equal(c.Notes, []string{NoteExtra}) {
		t.Errorf("Cwd %q Notes %v", c.Cwd, c.Notes)
	}
	c = inputCard(t, cfg, "execute", `{"Command":"rm x"}`)
	if !c.CommandAltered || c.CommandText != "" {
		t.Errorf("a differently-cased command key: text %q altered %v", c.CommandText, c.CommandAltered)
	}
}

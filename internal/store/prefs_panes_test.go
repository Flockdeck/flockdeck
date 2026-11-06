package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writePrefsFile puts text where the preferences are kept, in the test's own
// config directory.
func writePrefsFile(t *testing.T, text string) {
	t.Helper()
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, prefsFile), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// prefsKeysOnDisk is the top-level keys of the saved file.
func prefsKeysOnDisk(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, prefsFile))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("saved prefs are not JSON: %v\n%s", err, data)
	}
	return m
}

// TestPanesChoiceAndTheDefault checks what each file a user could have means
// for new panes: no choice is the default, the older true still counts, and an
// explicit terminal is a choice.
func TestPanesChoiceAndTheDefault(t *testing.T) {
	tests := []struct {
		name         string
		file         string
		choice       string
		onNormalHost bool
		onReachable  bool
	}{
		{"absent", `{}`, "", true, false},
		{"no file at all", ``, "", true, false},
		{"older structuredPanes true", `{"structuredPanes": true}`, PanesStructured, true, true},
		{"older structuredPanes false", `{"structuredPanes": false}`, "", true, false},
		{"panes structured", `{"panes": "structured"}`, PanesStructured, true, true},
		{"panes terminal", `{"panes": "terminal"}`, PanesTerminal, false, false},
		{"panes terminal beats the older true", `{"panes": "terminal", "structuredPanes": true}`, PanesTerminal, false, false},
		{"panes structured with the older false", `{"panes": "structured", "structuredPanes": false}`, PanesStructured, true, true},
		{"a value from a newer build is no choice", `{"panes": "hybrid"}`, "", true, false},
		{"a value from a newer build does not use the older true", `{"panes": "hybrid", "structuredPanes": true}`, "", true, false},
		{"a file from before either key", `{"helpSeen": true, "fontSize": 15}`, "", true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateConfig(t)
			if tc.file != "" {
				writePrefsFile(t, tc.file)
			}
			p, err := ReadPrefs()
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if got := p.PanesChoice(); got != tc.choice {
				t.Errorf("choice = %q, want %q", got, tc.choice)
			}
			if got := p.StructuredOn(false); got != tc.onNormalHost {
				t.Errorf("StructuredOn(false) = %v, want %v", got, tc.onNormalHost)
			}
			if got := p.StructuredOn(true); got != tc.onReachable {
				t.Errorf("StructuredOn(true) = %v, want %v", got, tc.onReachable)
			}
		})
	}
}

func TestSetPanes(t *testing.T) {
	var p Prefs
	if p.SetPanes("") {
		t.Error("clearing a choice that was never made reported a change")
	}
	if p.SetPanes("both") || p.Panes != "" {
		t.Errorf("an unknown value was taken: %q", p.Panes)
	}
	if !p.SetPanes(PanesTerminal) || p.Panes != PanesTerminal {
		t.Fatalf("terminal not set: %+v", p)
	}
	if p.SetPanes(PanesTerminal) {
		t.Error("setting the same choice again reported a change")
	}
	if !p.SetPanes("") || p.Panes != "" || !p.StructuredOn(false) {
		t.Errorf("clearing did not go back to the default: %+v", p)
	}

	// The older key is moved to the new one, so the two cannot disagree.
	old := Prefs{StructuredPanes: true}
	if !old.SetPanes(PanesStructured) || old.StructuredPanes || old.Panes != PanesStructured {
		t.Errorf("older true not moved: %+v", old)
	}
	old = Prefs{StructuredPanes: true}
	if !old.SetPanes(PanesTerminal) || old.StructuredPanes || old.PanesChoice() != PanesTerminal {
		t.Errorf("terminal over older true: %+v", old)
	}
}

// Writer: SavePrefs. An explicit terminal choice is stored and read back, and
// a person who made no choice does not get one written for them.
func TestSavePrefsKeepsAnExplicitTerminalChoiceAndInventsNoChoice(t *testing.T) {
	isolateConfig(t)
	if err := SavePrefs(Prefs{Panes: PanesTerminal}); err != nil {
		t.Fatal(err)
	}
	if got := prefsKeysOnDisk(t)["panes"]; string(got) != `"terminal"` {
		t.Errorf(`file holds panes %s, want "terminal"`, got)
	}
	back, err := ReadPrefs()
	if err != nil || back.PanesChoice() != PanesTerminal || back.StructuredOn(false) {
		t.Errorf("round trip: %+v, %v", back, err)
	}

	if err := SavePrefs(Prefs{HelpSeen: true}); err != nil {
		t.Fatal(err)
	}
	keys := prefsKeysOnDisk(t)
	for _, k := range []string{"panes", "structuredPanes"} {
		if _, ok := keys[k]; ok {
			t.Errorf("a save with no choice wrote %q", k)
		}
	}
	if back = LoadPrefs(); back.PanesChoice() != "" || !back.StructuredOn(false) {
		t.Errorf("no choice read back as %+v", back)
	}
}

// Writer: UpdatePrefs (the knowledge and wake commands, and the workspace's
// one-time notice, all go through it). A change to some other setting must not
// turn an absent choice into false or lose a stored one, and a file written by
// an older build keeps working.
func TestUpdatePrefsLeavesThePanesChoiceAlone(t *testing.T) {
	tests := []struct {
		name string
		file string
		want map[string]string // key -> raw JSON that must be there; "" = must be absent
	}{
		{"absent", `{"helpSeen": true}`, map[string]string{"panes": "", "structuredPanes": ""}},
		{"terminal", `{"panes": "terminal"}`, map[string]string{"panes": `"terminal"`, "structuredPanes": ""}},
		{"structured", `{"panes": "structured"}`, map[string]string{"panes": `"structured"`, "structuredPanes": ""}},
		{"older true", `{"structuredPanes": true}`, map[string]string{"panes": "", "structuredPanes": "true"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateConfig(t)
			writePrefsFile(t, tc.file)
			if err := UpdatePrefs(func(p *Prefs) bool { return p.Dismiss("a-hint") }); err != nil {
				t.Fatal(err)
			}
			keys := prefsKeysOnDisk(t)
			for k, want := range tc.want {
				raw, ok := keys[k]
				if want == "" && ok {
					t.Errorf("%q appeared in the file: %s", k, raw)
				}
				if want != "" && string(raw) != want {
					t.Errorf("%q = %s, want %s", k, raw, want)
				}
			}
			if !LoadPrefs().Dismissed("a-hint") {
				t.Error("the change itself was lost")
			}
		})
	}
}

// The older key is carried until the person chooses, and the choice then
// replaces it.
func TestChoosingReplacesTheOlderStructuredPanesKey(t *testing.T) {
	isolateConfig(t)
	writePrefsFile(t, `{"structuredPanes": true}`)
	if err := UpdatePrefs(func(p *Prefs) bool { return p.SetPanes(PanesTerminal) }); err != nil {
		t.Fatal(err)
	}
	keys := prefsKeysOnDisk(t)
	if string(keys["panes"]) != `"terminal"` {
		t.Errorf("panes = %s", keys["panes"])
	}
	if _, ok := keys["structuredPanes"]; ok {
		t.Error("the older key is still in the file beside the new one")
	}
	if LoadPrefs().StructuredOn(false) {
		t.Error("terminal choice did not turn structured off")
	}
}

// A stored choice survives a save made while a key this build does not know is
// in the file.
func TestPanesChoiceSitsBesideKeysFromANewerBuild(t *testing.T) {
	isolateConfig(t)
	writePrefsFile(t, `{"panes": "terminal", "futureThing": {"a": 1}}`)
	p, err := ReadPrefs()
	if err != nil {
		t.Fatal(err)
	}
	p.Dismiss("x")
	if err := SavePrefs(p); err != nil {
		t.Fatal(err)
	}
	keys := prefsKeysOnDisk(t)
	if string(keys["panes"]) != `"terminal"` || keys["futureThing"] == nil {
		t.Errorf("keys after save: %v", keys)
	}
}

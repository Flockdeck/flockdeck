package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSettingsAndDataFromFanOutAndTodoAreLeftAlone covers a configuration
// folder written while Fan out and Todo existed: preferences with a "fanOut"
// section, and a todos.json that nothing reads any more. Both load without an
// error, the preferences keep what they hold, and a save writes the files this
// build does not read back untouched, so going back to an older build finds
// them as they were.
func TestSettingsAndDataFromFanOutAndTodoAreLeftAlone(t *testing.T) {
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	prefs := filepath.Join(dir, prefsFile)
	todos := filepath.Join(dir, "todos.json")
	todoJSON := `{"version":1,"todos":[{"id":"t1","root":"C:/code","title":"Ship it","steps":[{"id":"s1","text":"one","done":false}]}]}`
	if err := os.WriteFile(prefs, []byte(`{"fontSize":17,"helpSeen":true,"fanOut":{"sameTab":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(todos, []byte(todoJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	p, err := ReadPrefs()
	if err != nil {
		t.Fatalf("read prefs: %v", err)
	}
	if !p.HelpSeen || p.FontSize != 17 {
		t.Errorf("the preferences next to the old section were not read: %+v", p)
	}
	p.Dismiss("palette")
	if err := SavePrefs(p); err != nil {
		t.Fatalf("save prefs: %v", err)
	}
	got, err := os.ReadFile(prefs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"fanOut"`) {
		t.Errorf("the old Fan out setting was dropped on save: %s", got)
	}

	// A project's own layout loads and saves as it always did, with the old
	// file in the same folder.
	root := t.TempDir()
	if err := Save(root, &State{}); err != nil {
		t.Fatalf("save a layout: %v", err)
	}
	if _, err := Load(root); err != nil {
		t.Fatalf("load a layout: %v", err)
	}
	after, err := os.ReadFile(todos)
	if err != nil {
		t.Fatalf("todos.json was removed: %v", err)
	}
	if string(after) != todoJSON {
		t.Errorf("todos.json was changed: %s", after)
	}
}

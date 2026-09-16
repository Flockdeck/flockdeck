package keybindings

import (
	"errors"
	"os"
	"testing"

	"github.com/jmwri/flockdeck/internal/help"
)

// isolateConfig points store.Dir at a fresh temporary directory for the
// length of the test, the same way internal/store's own tests do, so runs
// never share state with a real installation or with each other.
func isolateConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)         // Windows
	t.Setenv("XDG_CONFIG_HOME", dir) // Linux
	t.Setenv("HOME", dir)            // macOS and fallback
}

func TestLoadWithNoFileIsAllDefaults(t *testing.T) {
	isolateConfig(t)
	f, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(f.Overrides) != 0 {
		t.Errorf("a fresh install should have no overrides, got %+v", f.Overrides)
	}
	eff := Effective(f.Overrides)
	if len(eff) != len(help.Keys) {
		t.Fatalf("Effective returned %d keys, want %d", len(eff), len(help.Keys))
	}
	for i, k := range eff {
		if k.Keys != help.Keys[i].Keys {
			t.Errorf("Effective()[%d].Keys = %q, want the built-in default %q", i, k.Keys, help.Keys[i].Keys)
		}
	}
}

func TestSetBindingRoundTrips(t *testing.T) {
	isolateConfig(t)
	// closePane defaults to Ctrl+Shift+W; give it something nothing else has.
	if _, err := SetBinding("closePane", "Ctrl+Alt+W"); err != nil {
		t.Fatalf("SetBinding: %v", err)
	}
	f, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Overrides["closePane"] != "Ctrl+Alt+W" {
		t.Fatalf("override did not save, got %+v", f.Overrides)
	}
	eff := Effective(f.Overrides)
	k, ok := help.Lookup("closePane")
	if !ok {
		t.Fatal("closePane missing from help.Keys")
	}
	var got string
	for _, e := range eff {
		if e.ID == "closePane" {
			got = e.Keys
		}
	}
	if got != "Ctrl+Alt+W" {
		t.Errorf("Effective gave closePane %q, want Ctrl+Alt+W", got)
	}
	// help.Keys itself must be untouched.
	if cur, _ := help.Lookup("closePane"); cur.Keys != k.Keys {
		t.Errorf("help.Keys was mutated: closePane.Keys = %q, want %q", cur.Keys, k.Keys)
	}
}

func TestSetBindingRefusesAConflict(t *testing.T) {
	isolateConfig(t)
	// worktrees defaults to Ctrl+Shift+G; ask changes (Ctrl+Shift+S) for it.
	worktrees, _ := help.Lookup("worktrees")
	_, err := SetBinding("changes", worktrees.Keys)
	if err == nil {
		t.Fatal("SetBinding accepted a chord another action already has")
	}
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("SetBinding's error = %v, want a *ConflictError", err)
	}
	if ce.With.ID != "worktrees" {
		t.Errorf("ConflictError.With.ID = %q, want worktrees", ce.With.ID)
	}
	f, _ := Load()
	if _, has := f.Overrides["changes"]; has {
		t.Error("a refused binding was saved anyway")
	}
}

func TestSetBindingBackToDefaultDropsTheOverride(t *testing.T) {
	isolateConfig(t)
	def, _ := help.Lookup("closePane")
	if _, err := SetBinding("closePane", "Ctrl+Alt+W"); err != nil {
		t.Fatalf("SetBinding: %v", err)
	}
	if _, err := SetBinding("closePane", def.Keys); err != nil {
		t.Fatalf("SetBinding back to default: %v", err)
	}
	f, _ := Load()
	if _, has := f.Overrides["closePane"]; has {
		t.Error("a binding set back to its default is still kept as an override")
	}
}

func TestSetBindingCanClearTheBinding(t *testing.T) {
	isolateConfig(t)
	if _, err := SetBinding("closePane", ""); err != nil {
		t.Fatalf("SetBinding(\"\"): %v", err)
	}
	f, _ := Load()
	v, has := f.Overrides["closePane"]
	if !has || v != "" {
		t.Errorf("clearing a binding should leave an explicit empty override, got %+v", f.Overrides)
	}
	eff := Effective(f.Overrides)
	for _, k := range eff {
		if k.ID == "closePane" && k.Keys != "" {
			t.Errorf("Effective still gives closePane a binding: %q", k.Keys)
		}
	}
}

func TestResetBindingAndResetAll(t *testing.T) {
	isolateConfig(t)
	if _, err := SetBinding("closePane", "Ctrl+Alt+W"); err != nil {
		t.Fatalf("SetBinding: %v", err)
	}
	if _, err := SetBinding("zoomPane", ""); err != nil {
		t.Fatalf("SetBinding: %v", err)
	}
	if _, err := ResetBinding("closePane"); err != nil {
		t.Fatalf("ResetBinding: %v", err)
	}
	f, _ := Load()
	if _, has := f.Overrides["closePane"]; has {
		t.Error("ResetBinding left an override behind")
	}
	if _, has := f.Overrides["zoomPane"]; !has {
		t.Error("ResetBinding touched an action it was not asked about")
	}
	if _, err := ResetAll(); err != nil {
		t.Fatalf("ResetAll: %v", err)
	}
	f, _ = Load()
	if len(f.Overrides) != 0 {
		t.Errorf("ResetAll left overrides behind: %+v", f.Overrides)
	}
}

func TestSetBindingRefusesARange(t *testing.T) {
	isolateConfig(t)
	if _, err := SetBinding("selectTab", "Ctrl+Alt+1"); err == nil {
		t.Fatal("SetBinding accepted a single chord for selectTab, which is a range")
	}
}

func TestDamagedFileReadsAsDefaults(t *testing.T) {
	isolateConfig(t)
	p, err := path()
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	f, err := Load()
	if err != nil {
		t.Fatalf("Load on a damaged file returned an error: %v", err)
	}
	if len(f.Overrides) != 0 {
		t.Errorf("a damaged file should read as no overrides, got %+v", f.Overrides)
	}
}

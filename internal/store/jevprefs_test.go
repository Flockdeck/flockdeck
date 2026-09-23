package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Sending terminal output to a third party is opt-in: a prefs.json that says
// nothing about it means off, the default is never written out, and turning it
// on survives the round trip.
func TestJevStatusIsOffUnlessAskedFor(t *testing.T) {
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	path := filepath.Join(dir, prefsFile)
	if err := os.WriteFile(path, []byte(`{"helpSeen":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := LoadPrefs()
	if p.JevStatus {
		t.Fatal("an earlier prefs.json turned on sending terminal output to TypeSafe")
	}
	if err := SavePrefs(p); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "jev") {
		t.Errorf("the default was written out:\n%s", data)
	}
	if LoadPrefs().JevStatus {
		t.Error("saving the defaults turned it on")
	}

	p.JevStatus = true
	if err := SavePrefs(p); err != nil {
		t.Fatal(err)
	}
	if !LoadPrefs().JevStatus {
		t.Error("the setting did not survive the round trip")
	}
	// A damaged file is the defaults, which is off.
	if err := os.WriteFile(path, []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if LoadPrefs().JevStatus {
		t.Error("a damaged prefs file turned it on")
	}
}

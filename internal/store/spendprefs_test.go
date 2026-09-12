package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The spend preferences are new, so a prefs.json written before them has to
// load as it did, and saving the defaults must not write a key that an earlier
// build would carry around for nothing.
func TestSpendPrefsAreLeftOutWhileTheyAreDefaults(t *testing.T) {
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	path := filepath.Join(dir, prefsFile)
	if err := os.WriteFile(path, []byte(`{"helpSeen":true,"fontSize":15}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := LoadPrefs()
	if !p.HelpSeen || p.FontSize != 15 || p.Spend.StatusLine != "" {
		t.Fatalf("an earlier prefs.json read as %+v", p)
	}
	if err := SavePrefs(p); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "spend") {
		t.Errorf("the default spend preferences were written out:\n%s", data)
	}

	p.Spend.StatusLine = "on"
	if err := SavePrefs(p); err != nil {
		t.Fatal(err)
	}
	if back := LoadPrefs(); back.Spend.StatusLine != "on" {
		t.Errorf("the status line setting did not survive the round trip: %+v", back.Spend)
	}
}

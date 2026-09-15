package store

import "testing"

// Theme, AccentColor, FanOut and AutoReviewDefault follow prefs.json's own
// convention: each round-trips, and each is left out of the file while it is
// the default, so a file written before any of them existed still reads the
// same.
func TestAppearanceAndBehaviourPrefsRoundTrip(t *testing.T) {
	isolateConfig(t)

	p := LoadPrefs()
	p.Theme = "light"
	p.AccentColor = "teal"
	p.FanOut.SameTab = true
	p.AutoReviewDefault = true
	if err := SavePrefs(p); err != nil {
		t.Fatalf("SavePrefs: %v", err)
	}

	back := LoadPrefs()
	if back.Theme != "light" {
		t.Errorf("Theme = %q, want light", back.Theme)
	}
	if back.AccentColor != "teal" {
		t.Errorf("AccentColor = %q, want teal", back.AccentColor)
	}
	if !back.FanOut.SameTab {
		t.Error("FanOut.SameTab did not survive the round trip")
	}
	if !back.AutoReviewDefault {
		t.Error("AutoReviewDefault did not survive the round trip")
	}
}

// The zero value of every new field means what prefs.json has always meant
// for a fresh install, or a file written before these existed: dark, the
// default accent, a new tab, and auto-review off until it is turned on.
func TestAppearanceAndBehaviourPrefsDefaultToTheOldBehaviour(t *testing.T) {
	isolateConfig(t)
	p := LoadPrefs()
	if p.Theme != "" || p.AccentColor != "" || p.FanOut.SameTab || p.AutoReviewDefault {
		t.Errorf("a fresh install should have every new preference at its zero value, got %+v", p)
	}
}

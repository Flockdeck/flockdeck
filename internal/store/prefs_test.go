package store

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestPrefsRoundTrip(t *testing.T) {
	isolateConfig(t)

	if got := LoadPrefs(); got.HelpSeen || len(got.DismissedTips) != 0 {
		t.Fatalf("a fresh install should have empty prefs, got %+v", got)
	}

	p := LoadPrefs()
	p.HelpSeen = true
	if !p.Dismiss("welcome") {
		t.Fatal("Dismiss reported no change for a new id")
	}
	if p.Dismiss("welcome") {
		t.Error("Dismiss reported a change for an id already dismissed")
	}
	if err := SavePrefs(p); err != nil {
		t.Fatalf("SavePrefs: %v", err)
	}

	back := LoadPrefs().withLegacyUI()
	if !back.HelpSeen {
		t.Error("HelpSeen did not survive the round trip")
	}
	if !back.Dismissed("welcome") {
		t.Error("the dismissed hint did not survive the round trip")
	}
	if back.Dismissed("something-else") {
		t.Error("Dismissed reported true for a hint that was never dismissed")
	}
}

// Damaged prefs must not stop a start-up, so they read as the defaults — but
// the file is kept aside, since the next save would otherwise write the
// defaults over the only copy.
func TestPrefsIgnoreDamagedFile(t *testing.T) {
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, prefsFile), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write prefs: %v", err)
	}

	if got := LoadPrefs(); got.HelpSeen {
		t.Errorf("damaged prefs should read as the defaults, got %+v", got)
	}
	if err := SavePrefs(Prefs{HelpSeen: true}); err != nil {
		t.Fatalf("SavePrefs: %v", err)
	}
	kept, err := os.ReadFile(filepath.Join(dir, prefsFile+damagedSuffix))
	if err != nil || string(kept) != "{not json" {
		t.Errorf("damaged prefs = %q, %v; want them kept aside from the save", kept, err)
	}
}

func TestSpendLimitIsPerProjectBoundedAndClears(t *testing.T) {
	var p Prefs
	root := t.TempDir()
	if p.SpendLimitFor(root) != 0 {
		t.Error("a new Prefs has a limit")
	}
	if !p.SetSpendLimit(root, 12.345) || p.SpendLimitFor(root) != 12.35 {
		t.Errorf("limit = %v, want it rounded to cents", p.SpendLimitFor(root))
	}
	if p.SetSpendLimit(root, 12.35) {
		t.Error("the same limit reported a change")
	}
	for _, bad := range []float64{-1, MaxSpendLimit + 1, math.NaN(), math.Inf(1)} {
		if p.SetSpendLimit(root, bad) {
			t.Errorf("%v was accepted", bad)
		}
	}
	if p.SpendLimitFor(root) != 12.35 || p.SpendLimitFor(t.TempDir()) != 0 {
		t.Error("another project, or a refused figure, changed the limit")
	}
	if p.ForWindow().SpendLimits != nil {
		t.Error("project paths are sent to a window")
	}
	if !p.SetSpendLimit(root, 0) || p.SpendLimits != nil {
		t.Errorf("zero did not clear: %v", p.SpendLimits)
	}
}

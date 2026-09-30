package store

import (
	"os"
	"path/filepath"
	"testing"
)

// writeStateFile puts name in the state directory with body, as an earlier
// run would have left it.
func writeStateFile(t *testing.T, name, body string) {
	t.Helper()
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A new installation -- a state directory with nothing in it -- starts with
// auto-review on for every new pane, and says so from then on.
func TestANewInstallationStartsWithAutoReviewOn(t *testing.T) {
	isolateConfig(t)

	on, err := SettleAutoReviewDefault()
	if err != nil || !on {
		t.Fatalf("SettleAutoReviewDefault() = %v, %v, want true, nil", on, err)
	}
	if !LoadPrefs().AutoReviewDefault {
		t.Error("AutoReviewDefault is off after a new installation settled it on")
	}

	// Settled once, it is simply the setting: a second start leaves it be.
	if on, err := SettleAutoReviewDefault(); err != nil || on {
		t.Errorf("second SettleAutoReviewDefault() = %v, %v, want false, nil", on, err)
	}
	if !LoadPrefs().AutoReviewDefault {
		t.Error("AutoReviewDefault went off at the second start")
	}
}

// An installation from before auto-review was on by default keeps it off:
// whether its preferences file says nothing about it, or it never had one at
// all because no setting was ever changed -- and it stays off when the file
// is written back with some other change.
func TestAnUpgradeKeepsAutoReviewOff(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string // what the earlier run left in the state directory
		body string
	}{
		{"prefs without the key", prefsFile, `{"helpSeen":true,"theme":"light"}`},
		{"no prefs, but a layout", "layout-0123456789abcdef.json", `{}`},
		{"no prefs, but recent projects", recentsFile, `[]`},
		{"no prefs, but a session", sessionFile, `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateConfig(t)
			writeStateFile(t, tc.file, tc.body)

			if on, err := SettleAutoReviewDefault(); err != nil || on {
				t.Fatalf("SettleAutoReviewDefault() = %v, %v, want false, nil", on, err)
			}
			p := LoadPrefs()
			if p.AutoReviewDefault {
				t.Fatal("an upgraded installation came up with auto-review on")
			}

			p.FontSize = 15
			if err := SavePrefs(p); err != nil {
				t.Fatal(err)
			}
			if _, err := SettleAutoReviewDefault(); err != nil {
				t.Fatal(err)
			}
			if LoadPrefs().AutoReviewDefault {
				t.Error("auto-review came on after the preferences were saved and the app started again")
			}
		})
	}
}

// What someone chose is kept, either way, however many times the app starts.
func TestAnExplicitAutoReviewDefaultIsKept(t *testing.T) {
	for _, want := range []bool{true, false} {
		isolateConfig(t)
		body := `{"autoReviewDefault":false,"helpSeen":true}`
		if want {
			body = `{"autoReviewDefault":true}`
		}
		writeStateFile(t, prefsFile, body)
		for range 2 {
			if _, err := SettleAutoReviewDefault(); err != nil {
				t.Fatal(err)
			}
			if got := LoadPrefs().AutoReviewDefault; got != want {
				t.Errorf("AutoReviewDefault = %v, want %v as the file said", got, want)
			}
		}
	}
}

// A new installation that turns it off keeps it off: a false setting is left
// out of the file like every other default, and a file there without it reads
// as off rather than as a new installation.
func TestTurningItOffOnANewInstallationSticks(t *testing.T) {
	isolateConfig(t)
	if _, err := SettleAutoReviewDefault(); err != nil {
		t.Fatal(err)
	}
	p := LoadPrefs()
	p.AutoReviewDefault = false
	if err := SavePrefs(p); err != nil {
		t.Fatal(err)
	}
	if on, err := SettleAutoReviewDefault(); err != nil || on {
		t.Fatalf("SettleAutoReviewDefault() = %v, %v, want false, nil", on, err)
	}
	if LoadPrefs().AutoReviewDefault {
		t.Error("auto-review came back on after a new installation turned it off")
	}
}

// A preferences file that cannot be made sense of is an installation that
// had one, not a new one: it is never replaced with auto-review on.
func TestADamagedPrefsFileIsNotANewInstallation(t *testing.T) {
	isolateConfig(t)
	writeStateFile(t, prefsFile, `{not json`)
	if on, err := SettleAutoReviewDefault(); on {
		t.Fatalf("SettleAutoReviewDefault() = %v, %v, want false", on, err)
	}
	if LoadPrefs().AutoReviewDefault {
		t.Error("a damaged preferences file read as auto-review on")
	}
}

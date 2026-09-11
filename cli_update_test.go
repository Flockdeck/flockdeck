package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stageForTest leaves a staged update of the given version in a directory of
// its own, beside a program to be replaced, and returns both.
func stageForTest(t *testing.T, staged string) (dir, exe string) {
	t.Helper()
	dir, install := t.TempDir(), t.TempDir()
	exe = filepath.Join(install, "flockdeck")
	binary := filepath.Join(dir, "staged")
	for path, body := range map[string]string{exe: "running", binary: "staged"} {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rec, err := json.Marshal(map[string]string{"version": staged, "binary": binary})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pending.json"), rec, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, exe
}

// A restart comes back on the project that was on screen. Started with nothing,
// it opened the directory the first run was launched from, which after -C is
// somewhere the user never asked to work.
func TestRelaunchReopensTheProjectOnScreen(t *testing.T) {
	root := filepath.Join(t.TempDir(), "api")
	got := relaunchCommand("flockdeck", root).Args
	if want := []string{"flockdeck", "-C", root}; strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("relaunch runs %q, want %q", got, want)
	}
	if got := relaunchCommand("flockdeck", "").Args; len(got) != 1 {
		t.Errorf("with no project known, relaunch runs %q, want the program alone", got)
	}
}

// A release staged by one build is not an update to every build that finds it.
// A build of the user's own, or a newer release installed since, shares the
// same state directory, and putting the leftover in place on the way out would
// replace it with an older version.
func TestApplyStagedOnlyMovesForward(t *testing.T) {
	cases := []struct {
		running  string
		replaced bool
	}{
		{"dev", false},
		{"v2.0.0", false},
		{"v1.5.0", false}, // the very version that was staged
		{"v1.4.0", true},
	}
	for _, c := range cases {
		dir, exe := stageForTest(t, "v1.5.0")
		var out bytes.Buffer
		applyStaged(&out, dir, exe, c.running)
		got, err := os.ReadFile(exe)
		if err != nil {
			t.Fatal(err)
		}
		if replaced := string(got) == "staged"; replaced != c.replaced {
			t.Errorf("running %s with v1.5.0 staged: replaced = %v, want %v (%q)",
				c.running, replaced, c.replaced, out.String())
		}
	}
}

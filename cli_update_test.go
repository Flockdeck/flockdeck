package main

import (
	"bytes"
	"encoding/json"
	"errors"
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

// `flockdeck update check` is somebody who forgot the dash on -check. Ignoring
// the word ran the whole update, which is what they were asking not to do.
func TestUpdateRefusesAStrayWord(t *testing.T) {
	var out bytes.Buffer
	fs := updateFlagSet(&updateFlags{})
	fs.SetOutput(&out)
	if err := parseUpdate(fs, []string{"check"}); !errors.Is(err, errReported) {
		t.Fatalf("update check = %v, want it refused", err)
	}
	if !strings.Contains(out.String(), "Did you mean -check?") {
		t.Errorf("the refusal does not point at -check:\n%s", out.String())
	}

	var f updateFlags
	if err := parseUpdate(updateFlagSet(&f), []string{"-check"}); err != nil || !f.check {
		t.Errorf("update -check = %v, check = %v; want it read", err, f.check)
	}
}

// FLOCKDECK_UPDATE=off is for somebody who wants the program left as it is. An
// update staged before it was set must not go in on the way out regardless.
func TestApplyStagedRespectsUpdatesOff(t *testing.T) {
	t.Setenv(updateEnv, "off")
	dir, exe := stageForTest(t, "v1.5.0")
	applyStaged(&bytes.Buffer{}, dir, exe, "v1.4.0")
	if got, _ := os.ReadFile(exe); string(got) != "running" {
		t.Errorf("program = %q with updates turned off, want it left alone", got)
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

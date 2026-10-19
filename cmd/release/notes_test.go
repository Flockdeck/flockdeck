package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The tests of .github/scripts/release-notes.sh: where the notes in the signed
// manifest and the GitHub release come from. gh is a shell script that
// answers from files, and the script runs in a directory holding the
// docs/releases a checkout of the tag would.

const notesScript = "../../.github/scripts/release-notes.sh"

// fakeNotesGH answers `release view` with $GH_STATE/release (a body) or, when
// there is no such file, GitHub's not-found; $GH_STATE/view_fails makes it
// fail some other way. `api` answers with generated notes.
const fakeNotesGH = `#!/bin/sh
case "$1 $2" in
	"release view")
		if [ -f "$GH_STATE/view_fails" ]; then echo "HTTP 502: bad gateway" >&2; exit 1; fi
		if [ -f "$GH_STATE/release" ]; then cat "$GH_STATE/release"; exit 0; fi
		echo "release not found" >&2; exit 1 ;;
	"api "*) echo "## What's Changed
* generated from a pull request title"; exit 0 ;;
esac
echo "unexpected gh $*" >&2
exit 2
`

func runNotes(t *testing.T, tag string, setup func(work, state string)) (notes string, out string, err error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the notes script runs on the release workflow's Linux runner, or by hand in a POSIX shell")
	}
	if _, lookErr := exec.LookPath("sh"); lookErr != nil {
		t.Skip("no sh to run the notes script with")
	}
	script, absErr := filepath.Abs(notesScript)
	if absErr != nil {
		t.Fatal(absErr)
	}
	dir := t.TempDir()
	work, state, bin := filepath.Join(dir, "work"), filepath.Join(dir, "state"), filepath.Join(dir, "bin")
	for _, d := range []string{filepath.Join(work, "docs", "releases"), state, bin} {
		if mkErr := os.MkdirAll(d, 0o755); mkErr != nil {
			t.Fatal(mkErr)
		}
	}
	if wErr := os.WriteFile(filepath.Join(bin, "gh"), []byte(fakeNotesGH), 0o755); wErr != nil {
		t.Fatal(wErr)
	}
	setup(work, state)
	dest := filepath.Join(dir, "notes.md")
	cmd := exec.Command("sh", script)
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_STATE="+state, "TAG="+tag, "OUT="+dest, "GITHUB_REPOSITORY=Flockdeck/flockdeck")
	combined, runErr := cmd.CombinedOutput()
	b, _ := os.ReadFile(dest)
	return string(b), string(combined), runErr
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The point of the script: a release not yet on GitHub is signed with the
// customer notes, not the pull request titles.
func TestNotesAreTheReleaseNotesFile(t *testing.T) {
	got, out, err := runNotes(t, "v1.2.3", func(work, _ string) {
		write(t, filepath.Join(work, "docs", "releases", "v1.2.3.md"), "# Flockdeck v1.2.3\n\nCustomer notes.\n")
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got != "# Flockdeck v1.2.3\n\nCustomer notes.\n" {
		t.Errorf("notes = %q, want the contents of docs/releases/v1.2.3.md", got)
	}
}

// A release already published keeps the body it has, even if the file differs.
func TestNotesOfAPublishedReleaseAreKept(t *testing.T) {
	got, out, err := runNotes(t, "v1.2.3", func(work, state string) {
		write(t, filepath.Join(work, "docs", "releases", "v1.2.3.md"), "from the file\n")
		write(t, filepath.Join(state, "release"), "already on GitHub\n")
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got != "already on GitHub\n" {
		t.Errorf("notes = %q, want the published body", got)
	}
}

// A candidate has no notes file and falls back to what GitHub generates.
func TestNotesOfACandidateWithoutAFileAreGenerated(t *testing.T) {
	got, out, err := runNotes(t, "v1.2.3-rc.1", func(string, string) {})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(got, "generated from a pull request title") {
		t.Errorf("notes = %q, want the generated notes", got)
	}
}

// A candidate that has a notes file uses it, not the generated notes.
func TestNotesOfACandidateWithAFileAreTheFile(t *testing.T) {
	got, out, err := runNotes(t, "v1.2.3-rc.1", func(work, _ string) {
		write(t, filepath.Join(work, "docs", "releases", "v1.2.3-rc.1.md"), "candidate notes\n")
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got != "candidate notes\n" {
		t.Errorf("notes = %q, want the file", got)
	}
}

// Any other tag without a notes file is an error, not generated notes.
func TestNotesStopForAReleaseWithoutAFile(t *testing.T) {
	got, out, err := runNotes(t, "v1.2.3", func(string, string) {})
	if err == nil {
		t.Fatalf("the script succeeded with notes %q, want it to stop", got)
	}
	if !strings.Contains(out, "docs/releases/v1.2.3.md is missing") {
		t.Errorf("output = %q, want the missing file named", out)
	}
}

// Failing to ask GitHub is not the same as there being no release.
func TestNotesStopWhenGitHubCannotBeAsked(t *testing.T) {
	got, out, err := runNotes(t, "v1.2.3", func(work, state string) {
		write(t, filepath.Join(work, "docs", "releases", "v1.2.3.md"), "from the file\n")
		write(t, filepath.Join(state, "view_fails"), "")
	})
	if err == nil {
		t.Fatalf("the script succeeded with notes %q, want it to stop", got)
	}
	if !strings.Contains(out, "nothing has been signed or published") {
		t.Errorf("output = %q, want the reason", out)
	}
}

// release.yml signs with the script's notes, not with ones of its own.
func TestSignStepUsesTheNotesScript(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	if !strings.Contains(text, ".github/scripts/release-notes.sh\n          go run ./cmd/release -sign") {
		t.Error("the Sign step does not run release-notes.sh before signing")
	}
	if strings.Contains(text, "generate-notes") {
		t.Error("release.yml still generates notes itself")
	}
}

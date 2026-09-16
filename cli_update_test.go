package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/selfupdate"
)

// `flockdeck update -version=v1.4.0` is the rollback entry point: it takes a
// release name and, with -yes, skips the confirmation that otherwise stands
// between it and installing something other than the latest.
func TestUpdateFlagSetParsesVersionAndYes(t *testing.T) {
	var f updateFlags
	if err := parseUpdate(updateFlagSet(&f), []string{"-version=v1.4.0", "-yes"}); err != nil {
		t.Fatalf("parseUpdate: %v", err)
	}
	if f.version != "v1.4.0" || !f.yes {
		t.Errorf("version = %q, yes = %v; want v1.4.0, true", f.version, f.yes)
	}

	var g updateFlags
	if err := parseUpdate(updateFlagSet(&g), nil); err != nil || g.version != "" || g.yes {
		t.Errorf("with neither flag given: version = %q, yes = %v, err = %v; want unset", g.version, g.yes, err)
	}
}

// rollbackVerb says what is actually about to happen -- moving forward,
// back, or reinstalling the very version already running -- so `flockdeck
// update -version=` never claims to "roll back" an upgrade or "install" a
// version already in place.
func TestRollbackVerb(t *testing.T) {
	cases := []struct{ candidate, running, want string }{
		{"v1.5.0", "v1.4.0", "Installing"},
		{"v1.4.0", "v1.5.0", "Rolling back to"},
		{"v1.4.0", "v1.4.0", "Reinstalling"},
	}
	for _, c := range cases {
		if got := rollbackVerb(c.candidate, c.running); got != c.want {
			t.Errorf("rollbackVerb(%q, %q) = %q, want %q", c.candidate, c.running, got, c.want)
		}
	}
}

// Installing a version other than the latest is deliberate, so it is never
// done without asking -- and piped input never counts as an answer: a script
// has to pass -yes instead, the same way a pipe is never mistaken for
// somebody at a keyboard confirming a downgrade they did not mean.
func TestConfirmRollback(t *testing.T) {
	if _, err := confirmRollback(&bytes.Buffer{}, strings.NewReader(""), false, "v1.4.0", "v1.5.0"); err == nil {
		t.Error("confirmRollback with no terminal succeeded without -yes")
	}

	cases := []struct {
		typed string
		want  bool
	}{
		{"y\n", true},
		{"yes\n", true},
		{"Y\n", true},
		{"\n", false},
		{"n\n", false},
		{"", false}, // EOF with nothing typed
	}
	for _, c := range cases {
		var out bytes.Buffer
		ok, err := confirmRollback(&out, strings.NewReader(c.typed), true, "v1.4.0", "v1.5.0")
		if err != nil {
			t.Fatalf("confirmRollback(%q): %v", c.typed, err)
		}
		if ok != c.want {
			t.Errorf("confirmRollback(%q) = %v, want %v", c.typed, ok, c.want)
		}
		if !strings.Contains(out.String(), "v1.4.0") || !strings.Contains(out.String(), "v1.5.0") {
			t.Errorf("prompt %q does not name both versions", out.String())
		}
	}
}

// withVersion sets the package's own stamp for the life of a test, and puts
// it back after: checkForUpdatesNow's first question is whether this build
// was made from a release at all, and "dev" -- what every test binary is
// stamped, having gone through none of cmd/release -- answers no before
// either of the two tests below can ask anything else.
func withVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

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

// stageChosenForTest is stageForTest for a release staged by naming it --
// the interface's version picker -- rather than reached as the latest.
func stageChosenForTest(t *testing.T, staged string) (dir, exe string) {
	t.Helper()
	dir, install := t.TempDir(), t.TempDir()
	exe = filepath.Join(install, "flockdeck")
	binary := filepath.Join(dir, "staged")
	for path, body := range map[string]string{exe: "running", binary: "staged"} {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rec, err := json.Marshal(map[string]any{"version": staged, "binary": binary, "chosen": true})
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
	got := relaunchArgs(root)
	if want := []string{"-C", root}; strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("relaunch passes %q, want %q", got, want)
	}
	if got := relaunchArgs(""); len(got) != 0 {
		t.Errorf("with no project known, relaunch passes %q, want nothing", got)
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

// A build stamped nothing shows the version Go recorded for it, marked as not
// a release; a stamped release, or a build Go recorded nothing useful for, shows
// what it always did.
func TestDisplayVersion(t *testing.T) {
	pseudo := "v0.2.3-0.20260912013310-d1bd2676a15b"
	cases := []struct{ stamp, built, want string }{
		{"v1.4.0", pseudo, "v1.4.0"},
		{"dev", pseudo, pseudo + " (built from source, not a release)"},
		{"dev", "v1.4.0", "v1.4.0 (built from source, not a release)"},
		{"dev", "(devel)", "dev"},
		{"dev", "", "dev"},
	}
	for _, c := range cases {
		if got := displayVersion(c.stamp, c.built); got != c.want {
			t.Errorf("displayVersion(%q, %q) = %q, want %q", c.stamp, c.built, got, c.want)
		}
	}
}

// A build newer than anything published is not "the latest release", and
// saying so about a candidate or a withdrawn release was simply untrue.
func TestUpToDate(t *testing.T) {
	if got := upToDate("v1.5.0", "v1.4.0"); !strings.Contains(got, "newer than the latest release, v1.4.0") {
		t.Errorf("ahead of the latest: %q", got)
	}
	if got := upToDate("v1.4.0", "v1.4.0"); got != "flockdeck v1.4.0 is the latest release." {
		t.Errorf("on the latest: %q", got)
	}
}

// Each round of the watcher: fetch what moves this build forward, keep what is
// already staged, and throw away a staged release that has since been
// withdrawn, which is one newer than anything still published.
func TestUpdateSteps(t *testing.T) {
	cases := []struct {
		name                     string
		latest, staged, running  string
		wantDiscard, wantFetched bool
	}{
		{"a new release, nothing staged", "v1.5.0", "", "v1.4.0", false, true},
		{"the new release already staged", "v1.5.0", "v1.5.0", "v1.4.0", false, false},
		{"a newer one than the staged", "v1.6.0", "v1.5.0", "v1.4.0", false, true},
		{"nothing newer than this build", "v1.4.0", "", "v1.4.0", false, false},
		{"the staged one withdrawn, an older one still new", "v1.4.0", "v1.5.0", "v1.3.0", true, true},
		{"the staged one withdrawn, nothing new left", "v1.4.0", "v1.5.0", "v1.4.0", true, false},
		{"an answer that is not a version", "", "v1.5.0", "v1.4.0", false, false},
	}
	for _, c := range cases {
		discard, fetch := updateSteps(c.latest, c.staged, c.running)
		if discard != c.wantDiscard || fetch != c.wantFetched {
			t.Errorf("%s: updateSteps(%q, %q, %q) = discard %v, fetch %v; want %v, %v",
				c.name, c.latest, c.staged, c.running, discard, fetch, c.wantDiscard, c.wantFetched)
		}
	}
}

// Reaching neither the download site nor GitHub is explained in words; an
// answer GitHub gave is passed on as it is, since it already says what
// happened.
func TestExplainUnreachable(t *testing.T) {
	offline := &url.Error{Op: "Get", URL: "https://api.github.com/x", Err: errors.New("dial tcp: lookup api.github.com: no such host")}
	got := explainUnreachable(fmt.Errorf("wrapped: %w", offline), "download the release").Error()
	if !strings.Contains(got, "could not reach dl.flockdeck.ai or GitHub to download the release") || !strings.Contains(got, "no such host") || strings.Contains(got, "api.github.com/x") {
		t.Errorf("unreachable: %q, want it explained, with the cause but not the URL", got)
	}
	limited := errors.New("GitHub is limiting how often this address may ask for releases; try again later")
	if got := explainUnreachable(limited, "look for a newer release"); got != limited {
		t.Errorf("an answer from GitHub became %q", got)
	}
}

// `update -h` is a request for the usage it has just printed, so the command
// succeeds rather than exiting as though something had gone wrong.
func TestUpdateHelpSucceeds(t *testing.T) {
	fs := updateFlagSet(&updateFlags{})
	fs.SetOutput(&bytes.Buffer{})
	if err := parseUpdate(fs, []string{"-h"}); !errors.Is(err, errHelpAsked) {
		t.Errorf("parseUpdate(-h) = %v, want errHelpAsked", err)
	}
}

// The usage says every download is checked against a SHA-256 signed by the
// release key, and that is now so wherever it came from: a release read from
// GitHub's API has to carry the key's signature of its checksums.txt too. It
// used to claim the signature only for what dl.flockdeck.ai gave, because
// GitHub's checksums.txt was taken as it was.
func TestUpdateUsageSaysTheSignatureIsChecked(t *testing.T) {
	var out bytes.Buffer
	fs := updateFlagSet(&updateFlags{})
	fs.SetOutput(&out)
	fs.Usage()
	text := strings.Join(strings.Fields(out.String()), " ")
	if !strings.Contains(text, "checks it against its published SHA-256, signed by the release key, and") {
		t.Errorf("update's usage does not say what is checked:\n%s", out.String())
	}
	for _, l := range strings.Split(out.String(), "\n") {
		if n := len([]rune(l)); n > 80 {
			t.Errorf("a line %d wide: %q", n, l)
		}
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

// The manual "Check for updates" action in the settings is the same question
// `flockdeck update -check` asks, asked from the window instead. A build
// nothing published was ever compared with has nothing to check.
func TestCheckForUpdatesNowRefusesABuildThatIsNotARelease(t *testing.T) {
	withVersion(t, "dev")
	msg, isErr := checkForUpdatesNow(nil)
	if !isErr || !strings.Contains(msg, "not made from a release") {
		t.Errorf("checkForUpdatesNow() = %q, isErr %v, want it refused as not a release", msg, isErr)
	}
}

// FLOCKDECK_UPDATE=off is for a machine that is never to touch updates,
// packaged by somebody who runs their own updater; a button in a window is
// still asking, so it has to be refused the same as the background watcher.
func TestCheckForUpdatesNowRespectsTheEnvironment(t *testing.T) {
	withVersion(t, "v1.4.0")
	t.Setenv(updateEnv, "off")
	msg, isErr := checkForUpdatesNow(nil)
	if !isErr || !strings.Contains(msg, updateEnv+"=off") {
		t.Errorf("checkForUpdatesNow() = %q, isErr %v, want it refused by the environment", msg, isErr)
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

// A release chosen by name -- the interface's version picker, or the CLI's
// own -version staged by a run before this one -- is applied at the next
// restart regardless of direction: rolling back to an older version is the
// whole point, and applyStaged's ordinary "only moves forward" guard
// (TestApplyStagedOnlyMovesForward) is for what the background watcher staged
// on its own, not for somebody's deliberate choice.
func TestApplyStagedAppliesAChosenReleaseRegardlessOfDirection(t *testing.T) {
	running := []string{"v1.4.0", "v1.5.0", "v1.6.0"} // older, same, newer than v1.5.0
	for _, r := range running {
		dir, exe := stageChosenForTest(t, "v1.5.0")
		var out bytes.Buffer
		applyStaged(&out, dir, exe, r)
		got, err := os.ReadFile(exe)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "staged" {
			t.Errorf("running %s with a Chosen v1.5.0 staged: not applied (%q)", r, out.String())
		}
	}
}

// stagedUpdate offers a Chosen release exactly as staged, whichever way it
// compares to what is running -- unlike an ordinary staged release, which is
// only offered when it would move the running version forward.
func TestStagedUpdateHonoursChosen(t *testing.T) {
	dir, _ := stageChosenForTest(t, "v1.2.0")
	p, ok := stagedUpdate(dir, "v1.5.0") // v1.2.0 is older than what's running
	if !ok || p.Version != "v1.2.0" {
		t.Errorf("stagedUpdate = %+v, %v; want the Chosen release offered although it is older", p, ok)
	}

	dir2, _ := stageForTest(t, "v1.2.0") // the same, staged the ordinary way
	if _, ok := stagedUpdate(dir2, "v1.5.0"); ok {
		t.Error("stagedUpdate offered an ordinary (non-Chosen) staged release that is older than what's running")
	}
}

// chosenStillOffered is checkRound's guard against second-guessing a
// deliberate choice: nothing pending, or something staged the ordinary way,
// leaves the rest of checkRound to decide; a Chosen release is reported as
// ready and nothing else runs.
func TestChosenStillOffered(t *testing.T) {
	if _, handled := chosenStillOffered(nil); handled {
		t.Error("chosenStillOffered(nil) = handled, want checkRound to decide")
	}
	if _, handled := chosenStillOffered(&selfupdate.Pending{Version: "v1.5.0"}); handled {
		t.Error("chosenStillOffered on an ordinary staged release = handled, want checkRound to decide")
	}
	msg, handled := chosenStillOffered(&selfupdate.Pending{Version: "v1.2.0", Chosen: true})
	if !handled || !strings.Contains(msg, "v1.2.0") {
		t.Errorf("chosenStillOffered on a Chosen release = %q, %v; want it reported ready", msg, handled)
	}
}

// relationOf is the picker's own comparison, in words rather than a boolean,
// so the front end can offer "Reinstall", "Update to…" or "Roll back to…"
// without ordering versions itself.
func TestRelationOf(t *testing.T) {
	cases := []struct{ candidate, running, want string }{
		{"v1.5.0", "v1.4.0", "newer"},
		{"v1.4.0", "v1.5.0", "older"},
		{"v1.4.0", "v1.4.0", "current"},
	}
	for _, c := range cases {
		if got := relationOf(c.candidate, c.running); got != c.want {
			t.Errorf("relationOf(%q, %q) = %q, want %q", c.candidate, c.running, got, c.want)
		}
	}
}

// publishedString is a release's publish date for the wire: RFC 3339, or ""
// where nothing is known -- releases.json's own entries are not required to
// carry one, unlike a release read from GitHub's API or the site's manifest.
func TestPublishedString(t *testing.T) {
	if got := publishedString(time.Time{}); got != "" {
		t.Errorf("publishedString(zero) = %q, want empty", got)
	}
	at := time.Date(2026, 9, 12, 13, 0, 0, 0, time.UTC)
	if got := publishedString(at); got != "2026-09-12T13:00:00Z" {
		t.Errorf("publishedString = %q, want RFC 3339", got)
	}
}

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The tests of .github/scripts/regen-followup.sh: what release.yml's
// followups job runs to bring flockdeck-site and flockdeck-docs up to a
// release. The target repository is a real git repository beside a bare one
// standing in for GitHub, the generator is replaced by one that writes what a
// test says, and gh is a shell script that logs what it is asked and answers
// from files, so what the script did can be read back, in order.

const regenScript = "../../.github/scripts/regen-followup.sh"

// fakeGH answers the few gh calls the script makes. Its state is files in
// $GH_STATE: open_pr holds the number of an open pull request for the branch,
// auto_merge_refused makes --auto fail as GitHub does for a repository that
// does not allow it, and checks holds what the check runs add up to.
const fakeGH = `#!/bin/sh
echo "$*" >> "$GH_LOG"
case "$1 $2" in
	"pr list") [ -f "$GH_STATE/open_pr" ] && cat "$GH_STATE/open_pr"; exit 0 ;;
	"pr create") echo "https://github.com/Flockdeck/flockdeck-site/pull/7"; exit 0 ;;
	"pr edit" | "pr close") exit 0 ;;
	"pr merge")
		case " $* " in
			*" --auto "*) if [ -f "$GH_STATE/auto_merge_refused" ]; then echo "GraphQL: Auto merge is not allowed for this repository" >&2; exit 1; fi ;;
		esac
		exit 0 ;;
	"api "*) cat "$GH_STATE/checks"; exit 0 ;;
esac
echo "unexpected gh $*" >&2
exit 2
`

type regen struct {
	t      *testing.T
	origin string // the bare repository standing in for GitHub
	target string // the checkout of it the script works in
	state  string
	log    string
	bin    string
}

func newRegen(t *testing.T) *regen {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the follow-up script runs on the release workflow's Linux runner, or by hand in a POSIX shell")
	}
	for _, tool := range []string{"sh", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s to run the follow-up script with", tool)
		}
	}
	dir := t.TempDir()
	r := &regen{t: t, origin: filepath.Join(dir, "origin.git"), target: filepath.Join(dir, "target"), state: filepath.Join(dir, "state"), log: filepath.Join(dir, "gh.log"), bin: filepath.Join(dir, "bin")}
	for _, d := range []string{r.state, r.bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(r.bin, "gh"), []byte(fakeGH), 0o755); err != nil {
		t.Fatal(err)
	}
	r.git("", "init", "-q", "--bare", "-b", "main", r.origin)
	seed := filepath.Join(dir, "seed")
	r.git("", "init", "-q", "-b", "main", seed)
	r.git(seed, "config", "user.name", "seed")
	r.git(seed, "config", "user.email", "seed@example.invalid")
	if err := os.WriteFile(filepath.Join(seed, "index.html"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.git(seed, "add", "-A")
	r.git(seed, "commit", "-q", "-m", "the site")
	r.git(seed, "push", "-q", r.origin, "main")
	r.git("", "clone", "-q", r.origin, r.target)
	return r
}

func (r *regen) git(dir string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *regen) set(name, content string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.state, name), []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// reset puts the target back on a clean main, as a fresh checkout is.
func (r *regen) reset() {
	r.git(r.target, "checkout", "-q", "-f", "main")
	r.git(r.target, "reset", "-q", "--hard", "origin/main")
	r.git(r.target, "clean", "-qfd")
}

func (r *regen) ghLog() string {
	b, _ := os.ReadFile(r.log)
	return string(b)
}

func (r *regen) originBranch(branch string) string {
	out, err := exec.Command("git", "-C", r.origin, "rev-parse", "--verify", "-q", "refs/heads/"+branch).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// run sources the script, replaces its generator with one that writes site (a
// shell command run in the target), and runs main, or the command given.
func (r *regen) run(env map[string]string, generator, command string) (string, error) {
	r.t.Helper()
	script := ". " + regenScript + "\n"
	if generator != "" {
		script += "generate() { (cd \"$TARGET_DIR\" && " + generator + "); }\n"
	}
	script += command
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(),
		"REGEN_SOURCED=1",
		"PATH="+r.bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_LOG="+r.log, "GH_STATE="+r.state,
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"TMPDIR="+r.state, "GITHUB_OUTPUT=", "GITHUB_STEP_SUMMARY=",
		"TAG=v0.3.41", "TARGET=site", "TARGET_DIR="+r.target, "TARGET_REPO=Flockdeck/flockdeck-site",
		"SRC_DIR=.", "POLL_SECONDS=0", "WAIT_SECONDS=0",
	)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

const (
	same    = "printf 'v1\\n' > index.html"
	changed = "printf 'v2\\n' > index.html && printf 'new\\n' > install.sh"
)

var apply = map[string]string{"MODE": "apply", "GH_TOKEN": "fake"}

func TestFollowupTagsAndSwitch(t *testing.T) {
	r := newRegen(t)
	for tag, ok := range map[string]bool{
		"v0.3.41": true, "v1.20.300": true,
		"v0.3.41-rc.1": false, "0.3.41": false, "v0.3": false, "v0.3.41; touch x": false, "v0.3.41\nv0.3.42": false, "": false,
	} {
		script := `valid_tag "$1"`
		cmd := exec.Command("sh", "-c", ". "+regenScript+"; "+script, "sh", tag)
		cmd.Env = append(os.Environ(), "REGEN_SOURCED=1")
		if got := cmd.Run() == nil; got != ok {
			t.Errorf("valid_tag(%q) = %v, want %v", tag, got, ok)
		}
	}
	for value, on := range map[string]bool{"": true, "true": true, "TRUE": true, "1": true, "yes": true, "weird": true, "false": false, "False": false, "0": false, "no": false, "off": false, "OFF": false} {
		out, err := r.run(map[string]string{"AUTO_MERGE": value}, "", "auto_merge_on")
		if got := err == nil; got != on {
			t.Errorf("AUTO_MERGE=%q: on = %v, want %v\n%s", value, got, on, out)
		}
	}
}

func TestFollowupNoDiffDoesNothing(t *testing.T) {
	r := newRegen(t)
	out, err := r.run(apply, same, "main")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "nothing to do") {
		t.Errorf("did not say there was nothing to do:\n%s", out)
	}
	if b := r.originBranch("auto/regen-v0.3.41"); b != "" {
		t.Errorf("a branch was pushed with nothing to push: %s", b)
	}
	// Only the look for a pull request left open by an earlier run.
	if got := r.ghLog(); strings.Contains(got, "pr create") || strings.Contains(got, "pr merge") {
		t.Errorf("gh was asked for more than a look:\n%s", got)
	}
}

func TestFollowupClosesAnEmptiedPullRequest(t *testing.T) {
	r := newRegen(t)
	r.set("open_pr", "7")
	if out, err := r.run(apply, same, "main"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := r.ghLog(); !strings.Contains(got, "pr close 7") {
		t.Errorf("the pull request with nothing left in it was not closed:\n%s", got)
	}
}

func TestFollowupOpensOnePullRequestAndMergesWhenChecksPass(t *testing.T) {
	r := newRegen(t)
	out, err := r.run(apply, changed, "main")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	head := r.originBranch("auto/regen-v0.3.41")
	if head == "" {
		t.Fatalf("no branch was pushed\n%s", out)
	}
	if n := r.git(r.origin, "rev-list", "--count", "main.."+head); n != "1" {
		t.Errorf("the branch is %s commits ahead of main, want 1", n)
	}
	if files := r.git(r.origin, "diff", "--name-only", "main", head); files != "index.html\ninstall.sh" {
		t.Errorf("the branch changes %q", files)
	}
	if subject := r.git(r.origin, "log", "-1", "--format=%s", head); subject != "Regenerate for v0.3.41" {
		t.Errorf("commit subject %q", subject)
	}
	log := r.ghLog()
	for _, want := range []string{
		"pr create --repo Flockdeck/flockdeck-site --base main --head auto/regen-v0.3.41",
		"pr merge 7 --repo Flockdeck/flockdeck-site --auto --squash --match-head-commit " + head,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("gh was never asked %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "pr edit") {
		t.Errorf("a pull request that was just opened was edited:\n%s", log)
	}
}

// A second run for the same tag, with nothing changed since, changes nothing:
// not the branch, not the pull request. The commit it makes has a new hash,
// which is why it is compared by what it holds.
func TestFollowupRerunChangesNothing(t *testing.T) {
	r := newRegen(t)
	if out, err := r.run(apply, changed, "main"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	first := r.originBranch("auto/regen-v0.3.41")
	r.set("open_pr", "7")
	r.reset()
	if err := os.Remove(r.log); err != nil {
		t.Fatal(err)
	}
	out, err := r.run(apply, changed, "main")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := r.originBranch("auto/regen-v0.3.41"); got != first {
		t.Errorf("the branch was pushed again: %s -> %s", first, got)
	}
	log := r.ghLog()
	if strings.Contains(log, "pr create") || strings.Contains(log, "pr edit") {
		t.Errorf("the pull request was opened or edited again:\n%s", log)
	}
	// Auto-merge is asked for on the commit that is there, not on the one the
	// run made and threw away.
	if !strings.Contains(log, "--match-head-commit "+first) {
		t.Errorf("auto-merge was not asked for on the pushed commit %s:\n%s", first, log)
	}
}

// A run whose result differs from the branch updates it, and the pull request
// with it, rather than opening a second one.
func TestFollowupUpdatesTheBranchAndItsPullRequest(t *testing.T) {
	r := newRegen(t)
	if out, err := r.run(apply, changed, "main"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	first := r.originBranch("auto/regen-v0.3.41")
	r.set("open_pr", "7")
	r.reset()
	if out, err := r.run(apply, "printf 'v3\\n' > index.html", "main"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := r.originBranch("auto/regen-v0.3.41"); got == first || got == "" {
		t.Errorf("the branch was not updated: %s", got)
	}
	if n := r.git(r.origin, "rev-list", "--count", "main..auto/regen-v0.3.41"); n != "1" {
		t.Errorf("the branch is %s commits ahead of main after an update, want 1 (force-pushed, not stacked)", n)
	}
	if log := r.ghLog(); !strings.Contains(log, "pr edit 7") || strings.Count(log, "pr create") != 1 {
		t.Errorf("want the one pull request edited, not another opened:\n%s", log)
	}
}

func TestFollowupAutoMergeOffLeavesThePullRequest(t *testing.T) {
	r := newRegen(t)
	env := map[string]string{"AUTO_MERGE": "false"}
	for k, v := range apply {
		env[k] = v
	}
	if out, err := r.run(env, changed, "main"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	log := r.ghLog()
	if !strings.Contains(log, "pr create") {
		t.Errorf("no pull request was opened:\n%s", log)
	}
	if strings.Contains(log, "pr merge") {
		t.Errorf("a pull request was merged with auto-merge off:\n%s", log)
	}
}

func TestFollowupDryRunPushesNothing(t *testing.T) {
	r := newRegen(t)
	// No token, and gh is not there: a dry run must not need either.
	if err := os.Remove(filepath.Join(r.bin, "gh")); err != nil {
		t.Fatal(err)
	}
	out, err := r.run(map[string]string{"MODE": "dry-run"}, changed, "main")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "differs from what v0.3.41 generates") || !strings.Contains(out, "install.sh") {
		t.Errorf("the diff was not reported:\n%s", out)
	}
	if b := r.originBranch("auto/regen-v0.3.41"); b != "" {
		t.Errorf("a dry run pushed %s", b)
	}
	if head := r.git(r.target, "rev-parse", "HEAD"); head != r.git(r.origin, "rev-parse", "main") {
		t.Errorf("a dry run committed")
	}
}

func TestFollowupDryRunNoDiff(t *testing.T) {
	r := newRegen(t)
	out, err := r.run(map[string]string{"MODE": "dry-run"}, same, "main")
	if err != nil || !strings.Contains(out, "nothing to do") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// Line endings are not a difference: a checkout that holds CRLF, as a
// Windows one does, must not turn a regeneration that changed nothing into a
// pull request of every file.
func TestFollowupIgnoresCRLFStatNoise(t *testing.T) {
	r := newRegen(t)
	r.git(r.target, "config", "core.autocrlf", "true")
	if err := os.Remove(filepath.Join(r.target, "index.html")); err != nil {
		t.Fatal(err)
	}
	r.git(r.target, "checkout", "--", "index.html")
	if b, _ := os.ReadFile(filepath.Join(r.target, "index.html")); string(b) != "v1\r\n" {
		t.Fatalf("the checkout does not hold CRLF: %q", b)
	}
	// The generator writes LF, as it does on the runner and on Windows too.
	out, err := r.run(apply, same, "main")
	if err != nil || !strings.Contains(out, "nothing to do") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestFollowupRefusesWhatItShouldNot(t *testing.T) {
	r := newRegen(t)
	for name, env := range map[string]map[string]string{
		"a candidate":           {"TAG": "v0.3.41-rc.1"},
		"a tag that is shell":   {"TAG": "v0.3.41;touch pwned"},
		"an unknown target":     {"TARGET": "relay"},
		"apply with no token":   {"MODE": "apply", "GH_TOKEN": ""},
		"a mode that is a typo": {"MODE": "aply"},
	} {
		e := map[string]string{"MODE": "apply", "GH_TOKEN": "fake"}
		for k, v := range env {
			e[k] = v
		}
		if out, err := r.run(e, changed, "main"); err == nil {
			t.Errorf("%s was accepted:\n%s", name, out)
		}
	}
	if _, err := os.Stat(filepath.Join(r.target, "pwned")); err == nil {
		t.Error("a tag was run as shell")
	}
	if b := r.originBranch("auto/regen-v0.3.41"); b != "" || r.ghLog() != "" {
		t.Errorf("something was pushed or asked of GitHub for a refused run: %q\n%s", b, r.ghLog())
	}

	// A checkout with something in it would put that into the pull request.
	if err := os.WriteFile(filepath.Join(r.target, "stray"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := r.run(apply, changed, "main"); err == nil || !strings.Contains(out, "not clean") {
		t.Errorf("a checkout that was not clean was used: %v\n%s", err, out)
	}
}

// checkRuns is what GitHub answers to a commit's check runs.
func checkRuns(runs ...[3]string) string {
	var parts []string
	for _, r := range runs {
		conclusion := "null"
		if r[2] != "" {
			conclusion = `"` + r[2] + `"`
		}
		parts = append(parts, `{"name":"`+r[0]+`","status":"`+r[1]+`","conclusion":`+conclusion+`}`)
	}
	return `{"total_count":` + string(rune('0'+len(runs))) + `,"check_runs":[` + strings.Join(parts, ",") + `]}` + "\n"
}

// Where GitHub will not auto-merge (flockdeck-site has no branch protection
// for it to wait on), the script waits for the checks itself, and merges only
// once the required check has passed and nothing has failed.
func TestFollowupFallsBackToWaitingForChecks(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("no jq to add the check runs up with")
	}
	site := [3]string{"Check the site", "completed", "success"}
	image := [3]string{"Image", "completed", "success"}
	for name, tc := range map[string]struct {
		checks  string
		merged  bool
		wantErr bool
	}{
		"both pass":                   {checks: checkRuns(site, image), merged: true},
		"a skipped one does not stop": {checks: checkRuns(site, [3]string{"Scan", "completed", "skipped"}), merged: true},
		"the required check fails":    {checks: checkRuns([3]string{"Check the site", "completed", "failure"}, image), wantErr: true},
		"another check fails":         {checks: checkRuns(site, [3]string{"Image", "completed", "failure"}), wantErr: true},
		"a failure beats a pending":   {checks: checkRuns([3]string{"Check the site", "completed", "failure"}, [3]string{"Image", "in_progress", ""}), wantErr: true},
		"the image is still running":  {checks: checkRuns(site, [3]string{"Image", "in_progress", ""}), wantErr: true},
		"cancelled is not a pass":     {checks: checkRuns([3]string{"Check the site", "completed", "cancelled"}), wantErr: true},
		"nothing has reported yet":    {checks: checkRuns(), wantErr: true},
		"only the wrong check":        {checks: checkRuns(image), wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRegen(t)
			r.set("auto_merge_refused", "")
			r.set("checks", tc.checks)
			env := map[string]string{}
			for k, v := range apply {
				env[k] = v
			}
			out, err := r.run(env, changed, "main")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v\n%s", err, tc.wantErr, out)
			}
			head := r.originBranch("auto/regen-v0.3.41")
			merged := strings.Contains(r.ghLog(), "pr merge 7 --repo Flockdeck/flockdeck-site --squash --match-head-commit "+head)
			if merged != tc.merged {
				t.Errorf("merged = %v, want %v\n%s", merged, tc.merged, r.ghLog())
			}
		})
	}
}

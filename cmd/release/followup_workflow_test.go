package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// followupsJob is release.yml's followups job, cut into its steps.
func followupsJob(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	_, job, ok := strings.Cut(text, "\n  followups:\n")
	if !ok {
		t.Fatal("release.yml has no followups job")
	}
	if next := regexp.MustCompile(`\n  [a-z-]+:\n`).FindStringIndex(job); next != nil {
		job = job[:next[0]]
	}
	_, steps, ok := strings.Cut(job, "\n    steps:\n")
	if !ok {
		t.Fatal("the followups job has no steps")
	}
	return strings.Split(steps, "\n      - ")
}

// The followups job's workspace root is not a git checkout (each of its
// checkouts is in a folder of its own), and gh finds the repository from one.
// A gh call that does not name it fails with "not a git repository", and the
// job is continue-on-error, so the follow-ups would silently never run.
func TestFollowupsStepsThatRunGHNameTheRepository(t *testing.T) {
	gh := regexp.MustCompile(`(?m)^[^#\n]*\bgh (release|api|issue|pr|run|repo)\b`)
	seen := 0
	for _, step := range followupsJob(t) {
		_, run, ok := strings.Cut(step, "\n        run:")
		if !ok || !gh.MatchString(run) {
			continue
		}
		seen++
		if !strings.Contains(step, "GH_REPO: ${{ github.repository }}") {
			t.Errorf("a followups step runs gh with no GH_REPO:\n%s", step)
		}
	}
	if seen < 4 {
		t.Errorf("found %d followups steps that run gh, want at least 4 (latest, bot, checksums, issue): the test is not looking at the right thing", seen)
	}
}

// A failed follow-up is not a red run, so it says so in an issue.
func TestFollowupsSaysSoInAnIssueWhenItFails(t *testing.T) {
	steps := followupsJob(t)
	last := steps[len(steps)-1]
	if !strings.Contains(last, "if: ${{ failure() }}") || !strings.Contains(last, "gh issue") {
		t.Errorf("the last followups step is not the failure issue:\n%s", last)
	}
}

// The generators are the tag's code. The installation token is for git's push
// and gh, and is not in the environment the generators run in.
func TestFollowupGeneratorsDoNotSeeTheToken(t *testing.T) {
	for _, target := range []string{"docs", "site"} {
		t.Run(target, func(t *testing.T) {
			r := newRegen(t)
			fakeGo := "#!/bin/sh\nprintf 'GH_TOKEN=[%s] GITHUB_TOKEN=[%s]\\n' \"$GH_TOKEN\" \"$GITHUB_TOKEN\" > \"$GH_STATE/go.env\"\nprintf 'v2\\n' > \"$TARGET_DIR/index.html\"\n"
			if err := os.WriteFile(filepath.Join(r.bin, "go"), []byte(fakeGo), 0o755); err != nil {
				t.Fatal(err)
			}
			checksums := filepath.Join(r.state, "checksums.txt")
			for _, f := range []string{checksums, checksums + ".sig"} {
				if err := os.WriteFile(f, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			env := map[string]string{"TARGET": target, "CHECKSUMS": checksums, "GITHUB_TOKEN": "leaked"}
			for k, v := range apply {
				env[k] = v
			}
			out, err := r.run(env, "", "main")
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			got, err := os.ReadFile(filepath.Join(r.state, "go.env"))
			if err != nil {
				t.Fatalf("the generator was not run: %v", err)
			}
			if strings.TrimSpace(string(got)) != "GH_TOKEN=[] GITHUB_TOKEN=[]" {
				t.Errorf("the generator ran with %s", got)
			}
			// It still has it for gh: the pull request was opened.
			if !strings.Contains(r.ghLog(), "pr create") {
				t.Errorf("no pull request was opened:\n%s", r.ghLog())
			}
		})
	}
}

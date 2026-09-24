package main

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

const self = "Flockdeck/flockdeck"

func writer(f *fakeGitHub) *ghClient { return newClient(f.srv.URL, "issue-token") }

// driftingFake has site 60h behind and relay's pin one behind remote's latest.
func driftingFake(t *testing.T) *fakeGitHub {
	f := healthy(t)
	f.Repos["flockdeck-site"].Commits = commits(testNow, "bbb", 60, "install.sh", "ddd", 2, "index.html")
	f.Repos[remoteRepo].Tags = []string{"v0.10.1", "v0.10.0"}
	f.Repos[remoteRepo].TagDate = testNow.Add(-30 * time.Hour)
	return f
}

func sync1(t *testing.T, f *fakeGitHub, now time.Time) syncResult {
	t.Helper()
	pic, err := analyse(context.Background(), f.client(), now, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	res, err := syncIssue(context.Background(), writer(f), self, pic)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestRenderBody(t *testing.T) {
	f := driftingFake(t)
	pic, err := analyse(context.Background(), f.client(), testNow, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	body := renderBody(pic)
	for _, want := range []string{
		trackerMarker,
		"| Repo | Latest tag | Commits since | Shipping | Oldest shipping | Remote pin gap | Status |",
		"| Flockdeck/flockdeck-site | `v0.10.0` | 2 | 2 | 2d 12h (`bbb`) | n/a | **DRIFT** |",
		"| Flockdeck/flockdeck-relay | `v0.10.0` | 0 | 0 | n/a | pins `v0.10.0`, remote's latest is `v0.10.1` (1d 6h old) | clean |",
		"| Flockdeck/flockdeck | `v0.10.0` | 0 | 0 | n/a | n/a | info only, clean |",
		"older than **24h**",
		"Last checked 2026-09-24 12:00 UTC",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q\n%s", want, body)
		}
	}
	if m := fingerprintRe.FindStringSubmatch(body); m == nil || m[1] == "clear" {
		t.Errorf("a drifting body should carry a real fingerprint: %v", m)
	}
	// Under-threshold rows are shown, not flagged.
	f2 := healthy(t)
	f2.Repos[relayRepo].Commits = commits(testNow, "r1", 3, "internal/a.go")
	p2, _ := analyse(context.Background(), f2.client(), testNow, 24*time.Hour)
	b2 := renderBody(p2)
	if !strings.Contains(b2, "| 1 | 3h (`r1`) | in step (`v0.10.0`) | under threshold |") || !strings.Contains(b2, "Nothing is drifting") {
		t.Errorf("under-threshold rendering wrong:\n%s", b2)
	}
	if !strings.Contains(b2, "fingerprint:clear") {
		t.Error("nothing flagged should fingerprint as clear")
	}
}

func TestFingerprintIgnoresAgesButNotThePicture(t *testing.T) {
	f := driftingFake(t)
	a, _ := analyse(context.Background(), f.client(), testNow, 24*time.Hour)
	b, _ := analyse(context.Background(), f.client(), testNow.Add(20*time.Hour), 24*time.Hour)
	if fingerprint(a) != fingerprint(b) {
		t.Error("a later run with the same commits must keep the fingerprint")
	}
	f.Repos["flockdeck-site"].Commits = append(f.Repos["flockdeck-site"].Commits, fakeCommit{SHA: "eee", When: testNow, Files: []string{"site.css"}})
	c, _ := analyse(context.Background(), f.client(), testNow, 24*time.Hour)
	if fingerprint(a) == fingerprint(c) {
		t.Error("a new shipping commit must change the fingerprint")
	}
}

func TestIssueLifecycleDoesNotSpam(t *testing.T) {
	f := driftingFake(t)

	if res := sync1(t, f, testNow); res.Action != "created" || res.Commented {
		t.Fatalf("first drifting run: %+v", res)
	}
	if got := f.writes(); !reflect.DeepEqual(got, []string{"POST issue #1"}) {
		t.Fatalf("writes = %v", got)
	}

	// Same picture, same moment: nothing at all is sent.
	f.resetWrites()
	if res := sync1(t, f, testNow); res.Action != "unchanged" || res.Commented {
		t.Fatalf("identical re-run: %+v", res)
	}
	if got := f.writes(); len(got) != 0 {
		t.Fatalf("identical re-run wrote %v", got)
	}

	// A day later: ages moved, the picture did not. The body is refreshed in
	// place (no notification) and nobody is commented at.
	f.resetWrites()
	if res := sync1(t, f, testNow.Add(24*time.Hour)); res.Action != "updated" || res.Commented {
		t.Fatalf("next-day re-run: %+v", res)
	}
	if got := f.writes(); !reflect.DeepEqual(got, []string{"PATCH body #1"}) {
		t.Fatalf("next-day writes = %v", got)
	}
	if !strings.Contains(f.Issues[0].Body, "3d 12h") {
		t.Errorf("body should show the refreshed age:\n%s", f.Issues[0].Body)
	}

	// The picture changes: another repo starts drifting. One comment.
	f.Repos["flockdeck-billing"].Commits = commits(testNow, "b1", 40, "internal/x.go")
	f.resetWrites()
	if res := sync1(t, f, testNow.Add(24*time.Hour)); !res.Commented {
		t.Fatalf("a changed picture should comment: %+v", res)
	}
	if got := f.writes(); !reflect.DeepEqual(got, []string{"PATCH body #1", "POST comment #1"}) {
		t.Fatalf("changed writes = %v", got)
	}
	f.resetWrites()
	sync1(t, f, testNow.Add(24*time.Hour))
	if got := f.writes(); len(got) != 0 {
		t.Fatalf("re-run after the comment wrote %v", got)
	}

	// Everything is tagged: comment once and close.
	f.Repos["flockdeck-site"].Commits = nil
	f.Repos["flockdeck-billing"].Commits = nil
	f.Repos[remoteRepo].Tags = []string{"v0.10.0"}
	f.resetWrites()
	if res := sync1(t, f, testNow.Add(48*time.Hour)); res.Action != "closed" {
		t.Fatalf("clear run: %+v", res)
	}
	if got := f.writes(); !reflect.DeepEqual(got, []string{"POST comment #1", "PATCH state=closed #1"}) {
		t.Fatalf("close writes = %v", got)
	}

	// Still clear on later days: silence, and no new issue.
	f.resetWrites()
	if res := sync1(t, f, testNow.Add(72*time.Hour)); res.Action != "none" {
		t.Fatalf("clear re-run: %+v", res)
	}
	if got := f.writes(); len(got) != 0 {
		t.Fatalf("clear re-run wrote %v", got)
	}

	// Drift returns: the old issue is reopened, not duplicated.
	f.Repos["flockdeck-docs"].Commits = commits(testNow, "d1", 80, "content/x.md")
	f.resetWrites()
	if res := sync1(t, f, testNow.Add(72*time.Hour)); res.Action != "reopened" {
		t.Fatalf("returning drift: %+v", res)
	}
	if got := f.writes(); !reflect.DeepEqual(got, []string{"PATCH state=open #1", "POST comment #1"}) {
		t.Fatalf("reopen writes = %v", got)
	}
	if len(f.Issues) != 1 {
		t.Fatalf("there should still be exactly one tracking issue, got %d", len(f.Issues))
	}
}

func TestUnderThresholdNeverOpensAnIssue(t *testing.T) {
	f := healthy(t)
	f.Repos[relayRepo].Commits = commits(testNow, "r1", 3, "internal/a.go")
	if res := sync1(t, f, testNow); res.Action != "none" || len(f.writes()) != 0 {
		t.Fatalf("%+v %v", res, f.writes())
	}
}

func TestForeignIssuesAreNeverEdited(t *testing.T) {
	f := driftingFake(t)
	f.Issues = []*fakeIssue{
		// Same title, opened by a person, even one copying the marker.
		{Number: 1, Title: issueTitle, Body: trackerMarker + "\nhi", State: "open", Author: "mallory"},
		// Ours by author and title, but no marker.
		{Number: 2, Title: issueTitle, Body: "no marker", State: "open", Author: botLogin},
		// A pull request that looks like it.
		{Number: 3, Title: issueTitle, Body: trackerMarker, State: "open", Author: botLogin, IsPR: true},
	}
	if res := sync1(t, f, testNow); res.Action != "created" {
		t.Fatalf("got %+v", res)
	}
	for _, w := range f.writes() {
		if w != "POST issue #4" {
			t.Errorf("unexpected write %q", w)
		}
	}
}

func run1(t *testing.T, f *fakeGitHub, env map[string]string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	getenv := func(k string) string { return env[k] }
	if _, ok := env["GITHUB_API_URL"]; !ok {
		env["GITHUB_API_URL"] = f.srv.URL
	}
	err := run(context.Background(), args, getenv, testNow, &out)
	return out.String(), err
}

func TestRunFailsLoudlyWithoutTheReadToken(t *testing.T) {
	f := driftingFake(t)
	_, err := run1(t, f, map[string]string{"GITHUB_TOKEN": "x", "GITHUB_REPOSITORY": self})
	if err == nil {
		t.Fatal("a missing DEPLOY_DRIFT_TOKEN must not pass")
	}
	for _, want := range []string{"DEPLOY_DRIFT_TOKEN is not set", "Contents: read", "Flockdeck/flockdeck"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q lacks %q", err, want)
		}
	}
	if len(f.writes()) != 0 {
		t.Error("nothing may be written")
	}
}

func TestRunDoesNotTouchTheIssueWhenARepoIsUnreadable(t *testing.T) {
	f := driftingFake(t)
	// An open tracker exists. A partial picture (billing unreadable) would
	// look clean for billing; it must not be allowed to close anything.
	sync1(t, f, testNow)
	f.resetWrites()
	f.Repos["flockdeck-site"].Commits = nil
	f.Repos[remoteRepo].Tags = []string{"v0.10.0"}
	f.Deny["flockdeck-billing"] = 403
	_, err := run1(t, f, map[string]string{"DEPLOY_DRIFT_TOKEN": "r", "GITHUB_TOKEN": "w", "GITHUB_REPOSITORY": self})
	if err == nil {
		t.Fatal("expected an error")
	}
	if len(f.writes()) != 0 || f.Issues[0].State != "open" {
		t.Fatalf("issue touched on a failed run: %v %s", f.writes(), f.Issues[0].State)
	}
}

func TestRunDryRunAndThresholdFlag(t *testing.T) {
	f := driftingFake(t)
	out, err := run1(t, f, map[string]string{"DEPLOY_DRIFT_TOKEN": "r"}, "-dry-run", "-threshold-hours", "100")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "dry run") || !strings.Contains(out, "older than **100h**") || !strings.Contains(out, "fingerprint:clear") {
		t.Errorf("output:\n%s", out)
	}
	if len(f.Issues) != 0 || len(f.writes()) != 0 {
		t.Error("dry run wrote")
	}
	for _, bad := range []string{"abc", "-1", "NaN", ""} {
		if _, err := run1(t, f, map[string]string{"DEPLOY_DRIFT_TOKEN": "r"}, "-threshold-hours", bad); err == nil {
			t.Errorf("threshold %q should be rejected", bad)
		}
	}
}

func TestRunNeedsWriteCredentialsUnlessDryRun(t *testing.T) {
	f := driftingFake(t)
	if _, err := run1(t, f, map[string]string{"DEPLOY_DRIFT_TOKEN": "r"}); err == nil {
		t.Fatal("no GITHUB_TOKEN should be an error")
	}
}

func TestAgeFormatting(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Minute:             "<1h",
		5*time.Hour + 59*time.Minute: "5h",
		24 * time.Hour:               "1d 0h",
		61 * time.Hour:               "2d 13h",
	} {
		if got := formatAge(d); got != want {
			t.Errorf("formatAge(%v) = %q, want %q", d, got, want)
		}
	}
}

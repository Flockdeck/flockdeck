package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestIgnoredPath(t *testing.T) {
	cases := []struct {
		p    profile
		path string
		want bool
	}{
		{serviceProfile, ".github/workflows/release.yml", true},
		{serviceProfile, ".github/scripts/x.sh", true},
		{serviceProfile, "internal/store/store_test.go", true},
		{serviceProfile, "web/app.test.ts", true},
		{serviceProfile, "web/app.spec.js", true},
		{serviceProfile, "internal/x/testdata/golden.json", true},
		{serviceProfile, "README.md", true},
		{serviceProfile, "docs/notes.md", true},
		{serviceProfile, "docs/deep/notes.md", true},
		{serviceProfile, ".gitattributes", true},
		{serviceProfile, ".editorconfig", true},
		{serviceProfile, ".gitignore", true},
		{serviceProfile, ".dockerignore", true},
		{serviceProfile, "CODEOWNERS", true},
		{serviceProfile, ".github/CODEOWNERS", true},
		{serviceProfile, "docs/CODEOWNERS", true},
		{serviceProfile, "web/.gitignore", true},
		{contentProfile, ".gitattributes", true},
		{contentProfile, ".editorconfig", true},
		{contentProfile, ".gitignore", true},
		{contentProfile, ".dockerignore", true},
		{contentProfile, "CODEOWNERS", true},
		// Only the exact names: look-alikes ship.
		{serviceProfile, ".gitattributes.tmpl", false},
		{serviceProfile, "deploy/CODEOWNERS.yaml", false},
		{serviceProfile, "gitignore", false},
		// These ship, or might: kept conservative.
		{serviceProfile, "docs/openapi.yaml", false},
		{serviceProfile, "internal/store/store.go", false},
		{serviceProfile, "go.mod", false},
		{serviceProfile, "Dockerfile", false},
		{serviceProfile, "deploy/values.yaml", false},
		{serviceProfile, "cmd/relay/README.md", false},
		{serviceProfile, "latest_test.go.bak", false},
		// The product itself is content in site and docs.
		{contentProfile, "README.md", true},
		{contentProfile, "content/sso.md", false},
		{contentProfile, "docs/notes.md", false},
		{contentProfile, "index.html", false},
		{contentProfile, "install.sh", false},
		{contentProfile, ".github/workflows/x.yml", true},
	}
	for _, c := range cases {
		if got := ignoredPath(c.p, c.path); got != c.want {
			t.Errorf("ignoredPath(%v, %q) = %v, want %v", c.p, c.path, got, c.want)
		}
	}
}

func TestShips(t *testing.T) {
	f := func(names ...string) []commitFile {
		var fs []commitFile
		for _, n := range names {
			fs = append(fs, commitFile{Filename: n})
		}
		return fs
	}
	if ships(serviceProfile, f(".github/workflows/a.yml", "a_test.go", "docs/n.md")) {
		t.Error("a commit of only ignorable files should not ship")
	}
	if !ships(serviceProfile, f("a_test.go", "internal/x.go")) {
		t.Error("one shipping file among ignorable ones should ship")
	}
	if ships(serviceProfile, f(".gitattributes")) || ships(contentProfile, f(".gitattributes", ".editorconfig")) {
		t.Error("a commit of only repo config should not ship")
	}
	// Renaming a shipping file onto a config name still removed it.
	if !ships(serviceProfile, []commitFile{{Filename: ".gitignore", PreviousFilename: "internal/x.go"}}) {
		t.Error("a rename away from a shipping path should ship")
	}
	if ships(serviceProfile, nil) {
		t.Error("a commit touching nothing ships nothing")
	}
	// A rename out of a shipping path into an ignorable one still removed
	// shipping code.
	if !ships(serviceProfile, []commitFile{{Filename: "docs/x.md", PreviousFilename: "internal/x.go"}}) {
		t.Error("a rename away from a shipping path should ship")
	}
}

func TestLatestTagIsHighestSemverNotLastListed(t *testing.T) {
	f := healthy(t)
	// The API lists lexically: v0.9.0 sorts after v0.10.0. Prereleases and
	// non-versions never count.
	f.Repos[relayRepo].Tags = []string{"v0.10.0", "v0.9.9", "v0.9.10", "v1.0.0-rc.1", "v0.10.0-rc.1", "vnext", "v0.9"}
	got, err := latestTag(context.Background(), f.client(), relayRepo)
	if err != nil || got != "v0.10.0" {
		t.Fatalf("latestTag = %q, %v; want v0.10.0", got, err)
	}
	f.Repos[relayRepo].Tags = []string{"vnext", "v1.0.0-rc.1"}
	if _, err := latestTag(context.Background(), f.client(), relayRepo); err == nil {
		t.Fatal("no release tag should be an error, not a silent pass")
	}
}

func commits(now time.Time, specs ...any) []fakeCommit {
	// specs: sha, hoursAgo, files-as-comma-string, ...
	var out []fakeCommit
	for i := 0; i+2 < len(specs); i += 3 {
		out = append(out, fakeCommit{
			SHA:   specs[i].(string),
			When:  now.Add(-time.Duration(specs[i+1].(int)) * time.Hour),
			Files: strings.Split(specs[i+2].(string), ","),
		})
	}
	return out
}

func analyseWith(t *testing.T, f *fakeGitHub, threshold time.Duration) *picture {
	t.Helper()
	pic, err := analyse(context.Background(), f.client(), testNow, threshold)
	if err != nil {
		t.Fatalf("analyse: %v", err)
	}
	return pic
}

func rowFor(p *picture, name string) repoReport {
	for _, r := range p.Repos {
		if r.Spec.Name == name {
			return r
		}
	}
	panic("no row " + name)
}

func TestHealthyPictureIsClean(t *testing.T) {
	pic := analyseWith(t, healthy(t), 24*time.Hour)
	if pic.drifting() {
		t.Fatal("nothing untagged should not drift")
	}
	for _, r := range pic.Repos {
		if r.LatestTag != "v0.10.0" || r.Total != 0 {
			t.Errorf("%s: tag %q total %d", r.Spec.Name, r.LatestTag, r.Total)
		}
	}
}

func TestOldestIsTheOldestShippingCommitAndIgnoredOnesDoNotCount(t *testing.T) {
	f := healthy(t)
	f.Repos["flockdeck-site"].Commits = commits(testNow,
		"aaa", 90, ".github/workflows/ci.yml", // oldest, but CI only
		"bbb", 60, "install.sh", // oldest shipping
		"ccc", 5, "README.md", // ignorable in site too
		"ddd", 2, "content/sso.md,index.html", // ships
	)
	pic := analyseWith(t, f, 24*time.Hour)
	site := rowFor(pic, "flockdeck-site")
	if site.Total != 4 || site.Shipping != 2 {
		t.Fatalf("total/shipping = %d/%d, want 4/2", site.Total, site.Shipping)
	}
	if site.OldestShipping.SHA != "bbb" || site.Age != 60*time.Hour {
		t.Fatalf("oldest = %+v age %v; want bbb at 60h", site.OldestShipping, site.Age)
	}
	if !site.Flagged {
		t.Error("60h shipping drift should flag against a 24h threshold")
	}
}

func TestOnlyNonShippingCommitsNeverFlag(t *testing.T) {
	f := healthy(t)
	f.Repos[relayRepo].Commits = commits(testNow,
		"a1", 500, ".github/workflows/x.yml",
		"a2", 400, "internal/a_test.go",
		"a3", 300, "docs/notes.md",
	)
	relay := rowFor(analyseWith(t, f, time.Hour), relayRepo)
	if relay.Total != 3 || relay.Shipping != 0 || relay.Flagged || relay.OldestShipping != nil {
		t.Fatalf("got %+v", relay)
	}
}

func TestThreshold(t *testing.T) {
	f := healthy(t)
	f.Repos["flockdeck-billing"].Commits = commits(testNow, "b1", 20, "internal/x.go")
	if p := analyseWith(t, f, 24*time.Hour); rowFor(p, "flockdeck-billing").Flagged || p.drifting() {
		t.Error("20h old commit must not flag at a 24h threshold")
	}
	if p := analyseWith(t, f, 12*time.Hour); !rowFor(p, "flockdeck-billing").Flagged || !p.drifting() {
		t.Error("20h old commit must flag at a 12h threshold")
	}
}

func TestInfoOnlyRepoNeverFlags(t *testing.T) {
	f := healthy(t)
	f.Repos["flockdeck"].Commits = commits(testNow, "c1", 900, "main.go")
	pic := analyseWith(t, f, time.Hour)
	if r := rowFor(pic, "flockdeck"); r.Flagged || r.Shipping != 1 {
		t.Fatalf("info-only row: %+v", r)
	}
	if pic.drifting() {
		t.Fatal("an info-only repo must not make the picture drift")
	}
}

func TestPinGap(t *testing.T) {
	f := healthy(t)
	f.Repos[remoteRepo].Tags = []string{"v0.10.1", "v0.10.0"}
	f.Repos[remoteRepo].TagDate = testNow.Add(-30 * time.Hour)
	pic := analyseWith(t, f, 24*time.Hour)
	if !pic.Pin.Behind || pic.Pin.Pin != "v0.10.0" || pic.Pin.Latest != "v0.10.1" || !pic.Pin.Flagged {
		t.Fatalf("pin: %+v", pic.Pin)
	}
	if pic.Pin.Age != 30*time.Hour {
		t.Errorf("age %v, want the age of remote's latest tag (30h)", pic.Pin.Age)
	}

	f.Repos[remoteRepo].TagDate = testNow.Add(-2 * time.Hour)
	if p := analyseWith(t, f, 24*time.Hour); !p.Pin.Behind || p.Pin.Flagged {
		t.Errorf("a gap younger than the threshold is reported but not flagged: %+v", p.Pin)
	}

	f.Repos[relayRepo].GoMod = "require github.com/Flockdeck/flockdeck-remote v0.10.1\n"
	if p := analyseWith(t, f, time.Hour); p.Pin.Behind || p.drifting() {
		t.Errorf("relay on remote's latest has no gap: %+v", p.Pin)
	}

	f.Repos[relayRepo].GoMod = "require github.com/Flockdeck/flockdeck-remote v0.10.2-0.20260920101010-abcdef123456\n"
	if p := analyseWith(t, f, time.Hour); !p.Pin.NonRelease || !p.Pin.Behind {
		t.Errorf("a pseudo-version pin is not a release: %+v", p.Pin)
	}

	f.Repos[relayRepo].GoMod = "module x\n"
	if _, err := analyse(context.Background(), f.client(), testNow, time.Hour); err == nil {
		t.Error("a go.mod with no remote requirement should fail loudly")
	}
}

func TestManyCommitsAreCappedAndCountedAsShipping(t *testing.T) {
	f := healthy(t)
	var cs []fakeCommit
	for i := 0; i < 250; i++ {
		cs = append(cs, fakeCommit{SHA: fmt.Sprintf("s%03d", i), When: testNow.Add(-time.Duration(300-i) * time.Hour), Files: []string{"a_test.go"}})
	}
	f.Repos[relayRepo].Commits = cs
	relay := rowFor(analyseWith(t, f, 24*time.Hour), relayRepo)
	// 100 inspected (all test-only, ignored) + 150 uninspected, assumed shipping.
	if relay.Total != 250 || relay.Shipping != 150 || !relay.Approximate {
		t.Fatalf("got total %d shipping %d approx %v", relay.Total, relay.Shipping, relay.Approximate)
	}
	if relay.OldestShipping.SHA != "s100" {
		t.Errorf("oldest = %s, want the first uninspected commit s100", relay.OldestShipping.SHA)
	}
}

func TestUnreadableRepoAbortsWithAClearMessage(t *testing.T) {
	f := healthy(t)
	f.Deny["flockdeck-billing"] = 404 // what a token without access to a private repo gets
	_, err := analyse(context.Background(), f.client(), testNow, 24*time.Hour)
	if err == nil {
		t.Fatal("an unreadable repo must fail the run, not be skipped")
	}
	for _, want := range []string{"flockdeck-billing", "DEPLOY_DRIFT_TOKEN", "Contents: read"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

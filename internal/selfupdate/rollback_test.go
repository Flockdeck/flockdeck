package selfupdate

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"
)

// Fetch is what lets a rollback name a specific version rather than always
// "latest": it reads that version's manifest from the site directly, without
// going through latest.json at all, the same way latestFromSite reads
// whatever latest.json names.
func TestFetchReadsANamedVersionFromTheSiteDirectly(t *testing.T) {
	r := published(t, "the new program")
	log := logged(t)
	r.addVersion(t, "v9.9.8", true, false)

	rel, err := Fetch(context.Background(), "v9.9.8")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if rel.Version != "v9.9.8" || rel.Notes != "notes for v9.9.8" {
		t.Errorf("Fetch(v9.9.8) = %s %q, want the older release's own manifest", rel.Version, rel.Notes)
	}
	if r.dl.asked("/latest.json") {
		t.Error("Fetch read latest.json, which it has no reason to when the version is named directly")
	}
	if !r.dl.asked("/v9.9.8/manifest.json") {
		t.Error("v9.9.8's manifest was not read from the site")
	}

	p, err := Stage(context.Background(), rel, t.TempDir())
	if err != nil {
		t.Fatalf("Stage of a version named directly: %v", err)
	}
	if got := staged(t, p); got != "the new program" {
		t.Errorf("staged %q", got)
	}
	if l := log(); l != "" {
		t.Errorf("logged %q for a release that is signed", l)
	}
}

// Withdrawn from the site, or from before the site existed, a named version
// is still found on GitHub, held to the release key's signature exactly as
// Latest's GitHub fallback already is.
func TestFetchFallsBackToGitHubForANamedVersion(t *testing.T) {
	r := published(t, "the new program")
	log := logged(t)
	r.addVersion(t, "v9.9.8", false, true)

	rel, err := Fetch(context.Background(), "v9.9.8")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if rel.Version != "v9.9.8" || rel.Notes != "notes for v9.9.8 on GitHub" {
		t.Errorf("Fetch(v9.9.8) = %s %q, want GitHub's release", rel.Version, rel.Notes)
	}
	l := log()
	if !strings.Contains(l, "could not read v9.9.8 from") || !strings.Contains(l, "using GitHub instead") {
		t.Errorf("logged %q, want it to say why GitHub was used", l)
	}
	// Stage's checksums.txt.sig check is exercised for a GitHub-sourced
	// release by TestStageFromGitHubNeedsTheReleaseKeysSignature already;
	// what Fetch adds on top of that path is naming v9.9.8 rather than
	// whatever GitHub calls latest, which the assertions above cover.
}

// A version neither the site nor GitHub has ever heard of is refused, not
// silently given as something else.
func TestFetchOfAVersionThatDoesNotExist(t *testing.T) {
	published(t, "the new program")
	logged(t)
	if _, err := Fetch(context.Background(), "v0.0.1"); err == nil {
		t.Error("Fetch of an unpublished version succeeded")
	}
}

// List is the picker's source: recent releases, with drafts and pre-releases
// left out the same way Latest already treats them, since this names
// versions somebody could reinstall, not candidate builds.
func TestListFiltersDraftsAndPrereleases(t *testing.T) {
	pl := newPlace(t)
	oldAPI := githubAPIURL
	githubAPIURL = pl.srv.URL
	t.Cleanup(func() { githubAPIURL = oldAPI })

	api, err := json.Marshal([]map[string]any{
		{"tag_name": "v1.6.0", "body": "six"},
		{"tag_name": "v1.6.0-rc.1", "body": "a candidate, tagged as one"},
		{"tag_name": "v1.5.0", "body": "five, still a draft", "draft": true},
		{"tag_name": "v1.4.9", "body": "marked a pre-release", "prerelease": true},
		{"tag_name": "v1.4.0", "body": "four"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pl.set("/repos/"+Repo+"/releases", api)

	rels, err := List(context.Background(), 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var got []string
	for _, rel := range rels {
		got = append(got, rel.Version)
	}
	if want := "v1.6.0,v1.4.0"; strings.Join(got, ",") != want {
		t.Errorf("List = %v, want %s", got, want)
	}
}

// The limit given is what GitHub is asked for, and a limit of zero or less
// still asks for something rather than nothing.
func TestListAsksGitHubForNReleases(t *testing.T) {
	pl := newPlace(t)
	oldAPI := githubAPIURL
	githubAPIURL = pl.srv.URL
	t.Cleanup(func() { githubAPIURL = oldAPI })
	pl.set("/repos/"+Repo+"/releases", []byte("[]"))

	if _, err := List(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	if _, err := List(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	reqs := pl.requests()
	if len(reqs) != 2 {
		t.Fatalf("List made %d requests, want 2", len(reqs))
	}
	if got := reqs[0].URL.Query().Get("per_page"); got != "5" {
		t.Errorf("List(ctx, 5) asked per_page=%s", got)
	}
	if got := reqs[1].URL.Query().Get("per_page"); got == "0" || got == "" {
		t.Errorf("List(ctx, 0) asked per_page=%s, want some positive default", got)
	}
}

// Recall is the signal Newer never gives: not "something newer exists" but
// "what you are running is known-bad." It finds the running version on the
// site's signed list and says nothing for a version that is not on it.
func TestRecallFindsTheRunningVersion(t *testing.T) {
	r := published(t, "the new program")
	data, err := json.Marshal(Recalled{Versions: []RecalledVersion{
		{Version: "v9.9.9", Reason: "corrupts saved layouts", Upgrade: "v9.9.10"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	r.dl.set("/recalled.json", data)
	r.dl.set("/recalled.json.sig", Sign(r.key, data))

	rv := Recall(context.Background(), "v9.9.9")
	if rv == nil || rv.Reason != "corrupts saved layouts" || rv.Upgrade != "v9.9.10" {
		t.Fatalf("Recall(v9.9.9) = %+v, want the recall entry", rv)
	}
	if rv := Recall(context.Background(), "v9.9.8"); rv != nil {
		t.Errorf("Recall(v9.9.8) = %+v, want nil: it is not on the list", rv)
	}
}

// Nothing published, the site briefly unreachable, or this build having no
// release key to check the signature against: all of them are silence, the
// same best-effort way Latest's background use already treats a failure,
// never an error that stops whatever asked.
func TestRecallIsBestEffortWhenTheListCannotBeRead(t *testing.T) {
	r := published(t, "the new program")
	log := logged(t)
	if rv := Recall(context.Background(), "v9.9.9"); rv != nil {
		t.Errorf("Recall = %+v with no recalled.json published, want nil", rv)
	}

	r.dl.breakAll()
	if rv := Recall(context.Background(), "v9.9.9"); rv != nil {
		t.Errorf("Recall = %+v with the site out of reach, want nil", rv)
	}
	if l := log(); l != "" {
		t.Errorf("logged %q for an ordinary failure to read the list", l)
	}
}

// A signature that does not check is the one recalled.json failure worth a
// warning: unlike everything else the site serves, there is no GitHub mirror
// to fall back to, so an attacker forging "you are on a bad build" would
// otherwise fail silently instead of being logged the way a bad manifest
// already is.
func TestRecallLogsAWarningForATamperedList(t *testing.T) {
	r := published(t, "the new program")
	log := logged(t)
	data, err := json.Marshal(Recalled{Versions: []RecalledVersion{{Version: "v9.9.9", Reason: "bad"}}})
	if err != nil {
		t.Fatal(err)
	}
	r.dl.set("/recalled.json", data)
	_, other, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	r.dl.set("/recalled.json.sig", Sign(other, data))

	if rv := Recall(context.Background(), "v9.9.9"); rv != nil {
		t.Errorf("Recall = %+v for a list signed by another key, want nil", rv)
	}
	if l := log(); !strings.Contains(l, "warning:") || !strings.Contains(l, "recalled.json") {
		t.Errorf("logged %q, want a warning naming recalled.json", l)
	}
}

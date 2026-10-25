package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
)

// listingReason runs one control command against a fake client and answers the
// Reason of the listing it ends on.
func listingReason(t *testing.T, run func(c *controlClient)) string {
	t.Helper()
	m := lastListing(t, run)
	return m.Reason
}

func lastListing(t *testing.T, run func(c *controlClient)) changesMsg {
	t.Helper()
	c := &controlClient{out: make(chan []byte, 32)}
	run(c)
	for {
		select {
		case raw := <-c.out:
			var m changesMsg
			if err := json.Unmarshal(raw, &m); err == nil && m.Type == "changes" {
				return m
			}
		case <-time.After(30 * time.Second):
			t.Fatal("no listing arrived")
		}
	}
}

// The window keeps a git-settings question open when the listing that comes with
// it says so, and ends the action it has out for any other listing that has no
// reason: so the listing sent with the question must say "gitwarn", whichever
// action raised it.
func TestTheListingSentWithAGitQuestionSaysSo(t *testing.T) {
	isolateGit(t)
	srv, _, repo := newRepoServer(t)

	// A commit stopped by a hook the person has not seen.
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	edit(t, repo, "one\n")
	if got := listingReason(t, func(c *controlClient) { srv.commitChanges(c, repo, "one", false, nil, nil, 0, gitAnswer{}) }); got != "gitwarn" {
		t.Errorf("the listing sent with a commit's question says %q; want gitwarn", got)
	}

	// A fetch stopped by a program the person has not seen.
	was := scanPrograms
	t.Cleanup(func() { scanPrograms = was })
	scanPrograms = func(string) (gitx.Report, error) {
		return gitx.Report{Repo: repo + "/.git", Items: []gitx.Program{{Kind: "setting", Name: "core.sshcommand", Value: "x", Where: "/r/.git/config"}}}, nil
	}
	if got := listingReason(t, func(c *controlClient) { srv.runRemote(c, "fetch", repo, gitAnswer{}) }); got != "gitwarn" {
		t.Errorf("the listing sent with a fetch's question says %q; want gitwarn", got)
	}
}

// A commit that goes through and then asks about its push says the commit was
// made, so the typed message goes, and keeps the question open with "gitwarn".
func TestAPushQuestionAfterACommitSaysTheCommitWasMade(t *testing.T) {
	isolateGit(t)
	srv, _, repo := newRepoServer(t)
	edit(t, repo, "one\n")
	was := scanPrograms
	t.Cleanup(func() { scanPrograms = was })
	calls := 0
	scanPrograms = func(string) (gitx.Report, error) {
		calls++
		r := gitx.Report{Repo: repo + "/.git"}
		// scanPrograms is given only a directory, so it cannot tell which action asked; the call count stands in for it.
		// The first two looks (before the commit and just before its hooks) are clean; the one before the push is not.
		if calls > 2 {
			r.Items = []gitx.Program{{Kind: "setting", Name: "core.sshcommand", Value: "x", Where: "/r/.git/config"}}
		}
		return r, nil
	}
	m := lastListing(t, func(c *controlClient) { srv.commitChanges(c, repo, "one", true, nil, nil, 0, gitAnswer{}) })
	if m.Reason != "gitwarn" || !m.Committed {
		t.Errorf("the listing after a commit and a push question says reason %q, committed %v; want gitwarn and true", m.Reason, m.Committed)
	}
}

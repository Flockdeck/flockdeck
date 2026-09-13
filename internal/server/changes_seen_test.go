package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAReviewListingSaysWhyItWasSent covers the window's record of what
// somebody has looked at. The panel reads the tree again by itself whenever
// the pane counts move, so a file an agent added joined the list -- and the
// commit -- with nobody having looked at it. The window keeps the list it was
// shown when it opened or was refreshed, and needs to know which listings
// those are; a commit refused because the tree moved answers with the tree
// as it is now, which becomes the list to check, and a stamp taken at listing
// time catches a listed file written to again.
func TestAReviewListingSaysWhyItWasSent(t *testing.T) {
	srv, _, repo := newRepoServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\nreviewed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var ch changesMsg
	sendCmd(t, conn, command{Cmd: "changes", Path: repo})
	readUntil(t, conn, "changes", &ch)
	if ch.Reason != "asked" {
		t.Errorf("the listing of a panel being opened says %q; want asked", ch.Reason)
	}
	seen := ch.Files
	if len(seen) != 1 || seen[0].Stamp == "" {
		t.Fatalf("the listing does not stamp its file: %+v", seen)
	}

	// Decoded afresh each time: a listing with no reason leaves the field out,
	// and would otherwise keep the last one's.
	sendCmd(t, conn, command{Cmd: "changes", Path: repo, Follow: true})
	ch = changesMsg{}
	readUntil(t, conn, "changes", &ch)
	if ch.Reason != "" {
		t.Errorf("a listing the panel asked for by itself says %q; want nothing", ch.Reason)
	}

	// Written to again after it was listed, a line longer.
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\nreviewed\nand then some\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	commit := func() noticeMsg {
		t.Helper()
		files := []string{}
		stamps := map[string]string{}
		for _, f := range seen {
			files = append(files, f.Path)
			stamps[f.Path] = f.Stamp
		}
		sendCmd(t, conn, command{Cmd: "commit", Path: repo, Text: "reviewed", Files: files, Stamps: stamps})
		var note noticeMsg
		readUntil(t, conn, "notice", &note)
		readUntil(t, conn, "changes", &ch)
		return note
	}
	if note := commit(); !note.Error || !strings.Contains(note.Text, "1 file changed since the list was read") {
		t.Fatalf("a commit of a file written to after it was listed: notice %+v, want a refusal", note)
	}
	if ch.Reason != "refused" {
		t.Errorf("the listing after a refused commit says %q; want refused", ch.Reason)
	}

	seen = ch.Files
	if note := commit(); note.Error {
		t.Fatalf("committing the tree as listed after the refusal: %s", note.Text)
	}
	if ch.Reason != "committed" {
		t.Errorf("the listing after a commit says %q; want committed", ch.Reason)
	}
}

// TestTheOmittedNoticeGoesOnlyWithAListingSomebodyAskedFor covers a checkout
// with more changed files than the list holds. Every listing said so in a
// notice, and the window shows one notice at a time, so the one after a
// failed commit, push or pull replaced the error saying why it had failed.
func TestTheOmittedNoticeGoesOnlyWithAListingSomebodyAskedFor(t *testing.T) {
	srv, _ := newTestServer(t)
	dir := t.TempDir()
	was := readChanges
	t.Cleanup(func() { readChanges = was })
	readChanges = func(dir string) changesMsg {
		return changesMsg{Type: "changes", Cwd: dir, Files: []changeView{{Path: "a.txt"}}, Omitted: 5}
	}
	// notices collects what the window is sent until a listing and the moment
	// after it, and returns the notices.
	notices := func(c *controlClient) []noticeMsg {
		t.Helper()
		var got []noticeMsg
		listed := false
		for wait := 10 * time.Second; ; {
			select {
			case raw := <-c.out:
				var probe struct {
					Type string `json:"type"`
				}
				_ = json.Unmarshal(raw, &probe)
				switch probe.Type {
				case "notice":
					var n noticeMsg
					_ = json.Unmarshal(raw, &n)
					got = append(got, n)
				case "changes":
					listed, wait = true, 500*time.Millisecond
				}
			case <-time.After(wait):
				if !listed {
					t.Fatal("no listing arrived")
				}
				return got
			}
		}
	}
	showing := func(ns []noticeMsg) bool {
		for _, n := range ns {
			if strings.Contains(n.Text, "showing 1 of 6") {
				return true
			}
		}
		return false
	}

	c := &controlClient{out: make(chan []byte, 16)}
	srv.commitChanges(c, dir, "   ", false, nil, nil, 0)
	got := notices(c)
	if showing(got) {
		t.Errorf("after a commit that failed, the window was sent %+v; the notice about files left out replaces the error", got)
	}
	if len(got) == 0 || !got[0].Error {
		t.Errorf("the failed commit's error did not arrive: %+v", got)
	}

	srv.listChanges(c, dir, true)
	if got := notices(c); showing(got) {
		t.Errorf("a listing the panel asked for by itself was announced: %+v", got)
	}
	srv.listChanges(c, dir, false)
	if got := notices(c); !showing(got) {
		t.Errorf("opening the panel on a list that leaves files out said nothing about them: %+v", got)
	}
}

// TestAReviewOfADetachedOrRebasingCheckoutSaysSo covers a checkout in the
// middle of a rebase, or on no branch at all. The listing named the branch the
// rebase would go back to and nothing else, so the panel offered "no upstream
// yet" and a Push that set one up -- which then failed, there being no branch
// checked out to push.
func TestAReviewOfADetachedOrRebasingCheckoutSaysSo(t *testing.T) {
	srv, _, repo := newRepoServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)
	gitCmd(t, repo, "checkout", "-q", "--detach")

	var ch changesMsg
	sendCmd(t, conn, command{Cmd: "changes", Path: repo})
	readUntil(t, conn, "changes", &ch)
	if !ch.Detached || ch.Head == "" || ch.Operation != "" {
		t.Errorf("a detached checkout is listed as detached %v at %q, operation %q; want detached at its commit",
			ch.Detached, ch.Head, ch.Operation)
	}

	// A rebase stopped part way leaves its state where git reads it back.
	state := filepath.Join(repo, ".git", "rebase-merge")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "head-name"), []byte("refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sendCmd(t, conn, command{Cmd: "changes", Path: repo})
	readUntil(t, conn, "changes", &ch)
	if !ch.Detached || ch.Operation != "rebasing" || ch.Branch != "main" {
		t.Errorf("a rebase of main is listed as branch %q, operation %q, detached %v; want main, rebasing, detached",
			ch.Branch, ch.Operation, ch.Detached)
	}
}

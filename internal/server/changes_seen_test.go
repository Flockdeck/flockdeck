package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

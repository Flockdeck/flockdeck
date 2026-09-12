package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBrowseOffersABoundedListing covers the project picker opened on a folder
// holding thousands of others. Every one was looked into and sent -- on twenty
// thousand that took seconds and megabytes -- for a list nobody picks a project
// from by scrolling. The first are offered by name, and the rest are said to
// be there.
func TestBrowseOffersABoundedListing(t *testing.T) {
	was := maxBrowseEntries
	t.Cleanup(func() { maxBrowseEntries = was })
	maxBrowseEntries = 10

	srv, _ := newTestServer(t)
	dir := t.TempDir()
	for i := 0; i < 25; i++ {
		if err := os.Mkdir(filepath.Join(dir, fmt.Sprintf("pkg-%02d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	sendCmd(t, conn, command{Cmd: "browse", Path: dir})
	var br browseMsg
	readUntil(t, conn, "browse", &br)
	if len(br.Entries) != 10 {
		t.Fatalf("offered %d folders, want the first 10", len(br.Entries))
	}
	if first, last := br.Entries[0].Name, br.Entries[9].Name; first != "pkg-00" || last != "pkg-09" {
		t.Errorf("offered %s … %s, want pkg-00 … pkg-09", first, last)
	}
	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if !strings.Contains(note.Text, "10 of 25") {
		t.Errorf("notice = %q, want it to say 10 of 25 are shown", note.Text)
	}
}

package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// TestAFailingTimedSaveIsReportedOnceUntilOneWorks covers a layout save that
// keeps failing while the app runs. It was heard of only as the app stopped,
// on a terminal the window had hidden; the window is now told, but once, not
// every half minute, and again only if it fails after working. Each failure
// names a temporary file of its own, so telling them apart by what they say
// would report every one.
func TestAFailingTimedSaveIsReportedOnceUntilOneWorks(t *testing.T) {
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	var first stateMsg
	readUntil(t, conn, "state", &first)

	// Save once to learn the name the layout goes under. Every touch of the
	// workspace goes through its own goroutine, as the server's do.
	root := ws.ActiveRoot()
	if err, ok := ask(srv, func() error { return ws.SaveProject(root) }); !ok || err != nil {
		t.Fatalf("save: %v", err)
	}
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	layouts, err := filepath.Glob(filepath.Join(dir, "layout-*.json"))
	if err != nil || len(layouts) != 1 {
		t.Fatalf("layouts saved = %v, %v; want one", layouts, err)
	}
	// A directory standing where the layout goes, which no rename replaces.
	block := func() {
		if err := os.RemoveAll(layouts[0]); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(layouts[0], 0o700); err != nil {
			t.Fatal(err)
		}
	}
	unblock := func() {
		if err := os.Remove(layouts[0]); err != nil {
			t.Fatal(err)
		}
	}
	save := func() {
		if _, ok := ask(srv, func() bool { srv.saveLayouts(); return true }); !ok {
			t.Fatal("the timed save never ran")
		}
	}

	block()
	save()
	save() // failing the same way again is not news
	unblock()
	save() // working again forgets the failure
	block()
	save() // so failing after that is news once more

	var notices []string
	deadline := time.Now().Add(3 * time.Second)
	for {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			break
		}
		var note noticeMsg
		if json.Unmarshal(data, &note) != nil || note.Type != "notice" || !strings.Contains(note.Text, "could not be saved") {
			continue
		}
		if !note.Error {
			t.Errorf("notice %q is not marked as an error", note.Text)
		}
		notices = append(notices, note.Text)
	}
	if len(notices) != 2 {
		t.Errorf("notices = %q, want one for the first failure and one for failing again after a save worked", notices)
	}
}

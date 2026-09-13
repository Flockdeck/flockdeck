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

// TestALayoutMovedAsideByTheTimedSaveIsToldOnce covers a layout that could not
// be read at start, for a reason that has passed by the time of the timed
// save. The project came up on one fresh tab, the save moved the user's layout
// aside to "<name>.unread", and nothing said either had happened. The windows
// are now told, once, where it went.
func TestALayoutMovedAsideByTheTimedSaveIsToldOnce(t *testing.T) {
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	var first stateMsg
	readUntil(t, conn, "state", &first)

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
	saved, err := os.ReadFile(layouts[0])
	if err != nil {
		t.Fatal(err)
	}

	// The read at start fails, with a folder standing where the layout goes,
	// and the trouble has passed by the time of the save.
	if err := os.Remove(layouts[0]); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(layouts[0], 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(root); err == nil {
		t.Fatal("the layout was read through a folder")
	}
	if err := os.Remove(layouts[0]); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layouts[0], saved, 0o600); err != nil {
		t.Fatal(err)
	}

	save := func() {
		if _, ok := ask(srv, func() bool { srv.saveLayouts(); return true }); !ok {
			t.Fatal("the timed save never ran")
		}
	}
	save()
	save() // the file is kept already, so this is not news

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
		if json.Unmarshal(data, &note) != nil || note.Type != "notice" || !strings.Contains(note.Text, root) {
			continue
		}
		notices = append(notices, note.Text)
	}
	if len(notices) != 1 {
		t.Fatalf("notices = %q, want one saying where the layout went", notices)
	}
	if !strings.Contains(notices[0], layouts[0]+".unread") {
		t.Errorf("notice %q does not say where the layout is kept", notices[0])
	}
}

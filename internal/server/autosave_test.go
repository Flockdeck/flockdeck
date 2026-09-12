package server

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// TestLayoutsAreSavedWhileTheAppRuns covers a run that never gets to stop in
// an orderly way. Layouts were written only on the way out, so one killed or
// crashed came back as the layout it had started with, less every tab opened
// and pane split since.
func TestLayoutsAreSavedWhileTheAppRuns(t *testing.T) {
	was := layoutSaveInterval
	layoutSaveInterval = 200 * time.Millisecond
	t.Cleanup(func() { layoutSaveInterval = was })

	_, ws := newTestServer(t)
	root := ws.ActiveRoot()
	for deadline := time.Now().Add(15 * time.Second); ; {
		st, err := store.Load(root)
		if err == nil && st != nil && len(st.Tabs) == 1 && st.Tabs[0].Title == "first" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the layout was not saved while the app ran: %+v, %v", st, err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The list of open projects is the save at the end's to write.
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "session.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the list of open projects was written while the app ran: %v", err)
	}
}

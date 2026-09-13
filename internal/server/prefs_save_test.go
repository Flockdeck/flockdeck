package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// TestASettingThatCannotBeSavedSaysSo covers a preference the disk refused.
// The window applies a setting the moment it is chosen and says so -- "Font
// size 17px" -- and a failure to keep it was dropped here without a word: the
// setting went back at the next start, with nothing to say why.
func TestASettingThatCannotBeSavedSaysSo(t *testing.T) {
	srv, _ := newTestServer(t)
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	// A folder where the file goes, which no save can replace. A file that can't
	// be read is moved aside to prefs.json.unread before a save writes over it
	// (store.keepUnread), and a folder alone would be moved and the setting
	// saved, so a folder that isn't empty holds that name too: the old file can
	// be neither replaced nor moved, and the save is refused.
	for _, name := range []string{"prefs.json", filepath.Join("prefs.json.unread", "kept")} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	conn := dialControl(t, srv)
	nextHello(t, conn)

	sendCmd(t, conn, command{Cmd: "fontSize", Size: 17})
	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if !note.Error || !strings.Contains(note.Text, "could not save") {
		t.Fatalf("a setting that could not be saved was answered %+v; want an error saying so", note)
	}
}

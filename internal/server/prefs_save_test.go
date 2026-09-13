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
	// A folder where the file goes, which no save can replace.
	if err := os.MkdirAll(filepath.Join(dir, "prefs.json"), 0o755); err != nil {
		t.Fatal(err)
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

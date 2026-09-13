package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// TestASettingIsNotSavedOverSettingsThatCouldNotBeRead covers a setting
// changed while the saved ones cannot be read.
//
// A setting is saved by reading the file, making the one change and writing
// it all back, and a read that failed gave the defaults: saved, they stood in
// for every setting the user had. The save is refused instead. The window
// applies a setting the moment it is chosen and says so -- "Font size 17px" --
// so the refusal is said too, or the setting would go back at the next start
// with nothing to say why. It is said to the window that made the change and
// to no other, which has nothing to do about it.
func TestASettingIsNotSavedOverSettingsThatCouldNotBeRead(t *testing.T) {
	srv, _ := newTestServer(t)
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	// A folder where the file goes is the portable way to make its read fail
	// with something other than "does not exist".
	file := filepath.Join(dir, "prefs.json")
	if err := os.MkdirAll(file, 0o755); err != nil {
		t.Fatal(err)
	}
	conn := dialControl(t, srv)
	nextHello(t, conn)
	other := dialControl(t, srv)
	nextHello(t, other)

	sendCmd(t, conn, command{Cmd: "fontSize", Size: 17})
	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if !note.Error || !strings.Contains(note.Text, "could not save") {
		t.Fatalf("a setting that could not be saved was answered %+v; want an error saying so", note)
	}
	if fi, err := os.Stat(file); err != nil || !fi.IsDir() {
		t.Errorf("the settings that could not be read were replaced: %v", err)
	}
	if _, err := os.Stat(file + ".unread"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the settings that could not be read were moved aside: %v", err)
	}

	// The other window's answer to something it asked comes after anything it
	// was sent about the change, so a notice would be ahead of it.
	sendCmd(t, other, command{Cmd: "recents"})
	for deadline := time.Now().Add(20 * time.Second); ; {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the other window's recent projects")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_, data, err := other.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("read control: %v", err)
		}
		var msg noticeMsg
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		if msg.Type == "notice" {
			t.Fatalf("a window that changed nothing was told %q", msg.Text)
		}
		if msg.Type == "recents" {
			break
		}
	}
}

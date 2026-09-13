package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/appwindow"
	"github.com/jmwri/flockdeck/internal/store"
)

// A browser that fails as it starts can say so seconds after start-up, when
// the Windows release has let its terminal go or, started from a shortcut,
// never had one. The note went only to standard error, which was nowhere,
// and the run carried on with no window and nothing to say why. It is written
// to error.log as well.
func TestAWindowThatFailedLaterIsWrittenDown(t *testing.T) {
	isolateState(t)
	note := noteWindowFailed(&appwindow.StartError{Program: "chrome", Code: 1, Stderr: "Missing X server"})
	if !strings.Contains(note, "Missing X server") {
		t.Errorf("the note reads %q, want what the browser said", note)
	}
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "error.log"))
	if err != nil || !strings.Contains(string(data), "the window did not open") || !strings.Contains(string(data), "Missing X server") {
		t.Errorf("error.log = %q, %v; want the window's failure in it", data, err)
	}
}

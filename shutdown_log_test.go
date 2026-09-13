package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// A layout that could not be saved on the way out, a list of open projects
// that could not be kept, a restart that did not start, an update that could
// not be put in place: each was printed to a terminal the window had long
// since let go of, or to none at all for a run started from a shortcut. They
// are written to error.log too, where the troubleshooting page sends people.
func TestAFailureOnTheWayOutIsWrittenDown(t *testing.T) {
	isolateState(t)
	shutdownFailed("could not save layout", errors.New("disk full"))
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "error.log"))
	if err != nil || !strings.Contains(string(data), "could not save layout: disk full") {
		t.Errorf("error.log = %q, %v; want the failure on the way out in it", data, err)
	}
}

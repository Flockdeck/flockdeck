package main

import (
	"os"
	"testing"

	"github.com/jmwri/flockdeck/internal/testiso"
)

// TestMain keeps every test in the package from launching the developer's
// real browser: openBrowser is a no-op success unless a test replaces it with
// its own recorder (see stubBrowser). testiso keeps it off the real config
// directory, the stored keys and the real network (docs/testing.md).
func TestMain(m *testing.M) {
	openBrowser = func(string) error { return nil }
	// A test run from a pane an earlier build started inherits its PERCH_
	// names, which commands read as "this pane is old". Tests that want that
	// set them themselves (oldpane_test.go).
	for _, n := range []string{"API", "TOKEN", "PANE"} {
		os.Unsetenv("PERCH_" + n)
	}
	os.Exit(testiso.Main(m))
}

// stubBrowser replaces openBrowser for one test with a recorder that returns
// err, and gives back the URLs it was asked to open.
func stubBrowser(t *testing.T, err error) *[]string {
	t.Helper()
	var opened []string
	prev := openBrowser
	openBrowser = func(u string) error {
		opened = append(opened, u)
		return err
	}
	t.Cleanup(func() { openBrowser = prev })
	return &opened
}

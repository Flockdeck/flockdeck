package main

import (
	"os"
	"testing"
)

// TestMain keeps every test in the package from launching the developer's
// real browser: openBrowser is a no-op success unless a test replaces it with
// its own recorder (see stubBrowser).
func TestMain(m *testing.M) {
	openBrowser = func(string) error { return nil }
	os.Exit(m.Run())
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

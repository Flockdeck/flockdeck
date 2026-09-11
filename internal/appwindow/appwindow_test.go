package appwindow

import (
	"errors"
	"os"
	"testing"
)

// browserExit makes this test binary stand in for a browser: started with it
// set, it exits at once with the code it names, which is what a Chromium
// launch that hands its window to a running process (0) or fails to start (1)
// looks like from here.
const browserExit = "APPWINDOW_TEST_BROWSER_EXIT"

func TestMain(m *testing.M) {
	switch os.Getenv(browserExit) {
	case "0":
		os.Exit(0)
	case "1":
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// A launch that passes its window to a browser already running on the profile
// exits at once and successfully. The window is open, so that must not read as
// the user closing it: it stopped every instance started while another's
// window was up.
func TestWaitReportsAHandOff(t *testing.T) {
	t.Setenv(browserExit, "0")
	w, err := startAppMode(os.Args[0], "http://127.0.0.1:1/", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Wait(); !errors.Is(err, ErrHandedOff) {
		t.Errorf("Wait = %v, want ErrHandedOff", err)
	}
}

// A browser that fails to start is not a hand-off: there is no window anywhere.
func TestWaitReportsABrowserThatFailed(t *testing.T) {
	t.Setenv(browserExit, "1")
	w, err := startAppMode(os.Args[0], "http://127.0.0.1:1/", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Wait(); err == nil || errors.Is(err, ErrHandedOff) {
		t.Errorf("Wait = %v, want the browser's own failure", err)
	}
}

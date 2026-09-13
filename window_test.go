package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/appwindow"
)

// fakeBrowser makes this test binary stand in for the window's browser: started
// with it set to "fail", it fails as Chromium does on Linux with no display.
const fakeBrowser = "FLOCKDECK_TEST_FAKE_BROWSER"

func init() {
	if os.Getenv(fakeBrowser) == "fail" {
		fmt.Fprintln(os.Stderr, "[1:1:ERROR:ozone_platform_x11.cc(240)] Missing X server or $DISPLAY")
		os.Exit(1)
	}
}

// A browser that fails as it starts -- no display on Linux, a snap-packaged
// Chromium refusing its profile -- is not the user closing the window. It used
// to stop the whole application at once, and without a word. The failure is
// told, with what the browser said, and the application carries on.
func TestAWindowThatNeverOpenedDoesNotStopTheApp(t *testing.T) {
	t.Setenv(fakeBrowser, "fail")
	t.Setenv(appwindow.BrowserEnv, os.Args[0])
	win, err := appwindow.Open("http://127.0.0.1:1/", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stopped := false
	var told error
	watchWindow(win.Wait, func() bool { return false }, func() { stopped = true }, func(err error) { told = err })
	if stopped {
		t.Error("the application was stopped by a window that never opened")
	}
	if told == nil || !strings.Contains(told.Error(), "code 1") || !strings.Contains(told.Error(), "Missing X server") {
		t.Errorf("told %v, want the exit code and what the browser said", told)
	}
	if note := windowFailedNote(told); !strings.Contains(note, appwindow.BrowserEnv) || !strings.Contains(note, "-no-window") {
		t.Errorf("the note reads %q, want it to name %s and -no-window", note, appwindow.BrowserEnv)
	}
}

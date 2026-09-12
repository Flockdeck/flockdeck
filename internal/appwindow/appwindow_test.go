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

// A Mac browser installed without an administrator's rights lives in the
// user's own Applications folder, and has to be found there too — after the
// system's copy, so the machine-wide install still wins.
func TestDarwinCandidatesLookInTheUsersApplications(t *testing.T) {
	got := darwinCandidates("/Users/sam")
	sys := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	own := "/Users/sam/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	at := map[string]int{}
	for i, c := range got {
		at[c] = i + 1
	}
	if at[sys] == 0 || at[own] == 0 || at[own] < at[sys] {
		t.Errorf("candidates = %q, want %q and then %q", got, sys, own)
	}
	if len(darwinCandidates("")) != 4 {
		t.Errorf("with no home directory known, want the four system paths only")
	}
}

// A path with spaces is written in quotes in cmd.exe, and `set` keeps them in
// the value, so the pinned browser has to be read without them.
func TestPinnedBrowserDropsQuotes(t *testing.T) {
	t.Setenv(BrowserEnv, `"C:\Program Files\Chromium\chrome.exe"`)
	if prog, from := pinnedBrowser(); prog != `C:\Program Files\Chromium\chrome.exe` || from != BrowserEnv {
		t.Errorf("pinnedBrowser = %q from %s, want the path without its quotes", prog, from)
	}
	t.Setenv(BrowserEnv, "")
	t.Setenv(legacyBrowserEnv, "chromium")
	if prog, from := pinnedBrowser(); prog != "chromium" || from != legacyBrowserEnv {
		t.Errorf("pinnedBrowser = %q from %s, want the older name's value", prog, from)
	}
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

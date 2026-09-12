package appwindow

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// Chromium opens an app window at exactly the size asked for, so a screen with
// less room than the default has to be given a smaller one, or the window's
// own buttons end up off it.
func TestFitWindow(t *testing.T) {
	cases := []struct{ w, h, wantW, wantH int }{
		{1920, 1032, 1440, 900}, // room to spare: the default
		{1366, 728, 1229, 655},  // a small laptop: nine tenths of it
		{2560, 700, 1440, 630},  // wide but short: only the height gives
		{0, 0, 1440, 900},       // not known: the default
	}
	for _, c := range cases {
		if w, h := fitWindow(c.w, c.h); w != c.wantW || h != c.wantH {
			t.Errorf("fitWindow(%d, %d) = %dx%d, want %dx%d", c.w, c.h, w, h, c.wantW, c.wantH)
		}
	}
}

// A Mac browser installed without an administrator's rights lives in the
// user's own Applications folder, and has to be found there too — after the
// system's copy, so the machine-wide install still wins.
// The browser keeps the window's size and place as it closes but does not open
// the next one there, and the size given at every launch put a window the
// user had resized or moved back at the default every time. What it kept is
// given back.
func TestPlacementArgsGiveBackWhatTheBrowserKept(t *testing.T) {
	const page = "http://127.0.0.1:53063/?t=abc"
	profile := t.TempDir()
	check := func(what string, got []string, want ...string) {
		t.Helper()
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%s: %q, want %q", what, got, want)
		}
	}
	check("a first window", placementArgs(profile, page, 1920, 1032), "--window-size=1440,900")

	keep := func(bounds string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(profile, "Default"), 0o700); err != nil {
			t.Fatal(err)
		}
		prefs := `{"browser":{"app_window_placement":{"127":{"0":{"0":{"1_/":` + bounds + `}}}}}}`
		if err := os.WriteFile(filepath.Join(profile, "Default", "Preferences"), []byte(prefs), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keep(`{"left":200,"top":150,"right":1200,"bottom":850,"maximized":false}`)
	check("a window resized and moved", placementArgs(profile, page, 1920, 1032),
		"--window-size=1000,700", "--window-position=200,150")
	// Each run is on a port of its own, and it is the same window.
	check("the same window on another port", placementArgs(profile, "http://127.0.0.1:61000/?t=def", 1920, 1032),
		"--window-size=1000,700", "--window-position=200,150")
	check("on a screen of unknown size", placementArgs(profile, page, 0, 0), "--window-size=1000,700")
	// Kept on a bigger screen than this one, it would open past the edges.
	check("on a smaller screen", placementArgs(profile, page, 800, 600), "--window-size=720,540")

	// What is kept for a maximised window is the size and place it had
	// before, and Edge ignores --start-maximized for an app window.
	keep(`{"left":100,"top":80,"right":1300,"bottom":880,"maximized":true}`)
	check("a window left maximised", placementArgs(profile, page, 1920, 1032),
		"--window-size=1200,800", "--window-position=100,80")
}

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

// A pinned browser that is there but will not start has to be named as the
// user's own setting, or the failure reads as Flockdeck's.
func TestAPinnedBrowserThatWillNotStartIsNamed(t *testing.T) {
	notABrowser := filepath.Join(t.TempDir(), "notabrowser.exe")
	if err := os.WriteFile(notABrowser, []byte("not a program"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(BrowserEnv, notABrowser)
	_, err := Open("http://127.0.0.1:1/", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), BrowserEnv) {
		t.Errorf("Open = %v, want the failure to name %s", err, BrowserEnv)
	}
}

// A path with spaces is written in quotes in cmd.exe, and `set` keeps them in
// the value, so the pinned browser has to be read without them.
// Chrome, Edge and Brave are on no PATH on Windows or macOS, so pinning one
// by name, as the usage invites, failed to open the window at all. A name is
// looked for where browsers are installed.
func TestPinningABrowserByName(t *testing.T) {
	var where string
	var names []string
	switch runtime.GOOS {
	case "windows":
		base := t.TempDir()
		t.Setenv("ProgramFiles", base)
		t.Setenv("ProgramFiles(x86)", "")
		t.Setenv("LocalAppData", "")
		where = filepath.Join(base, `Microsoft\Edge\Application\msedge.exe`)
		names = []string{"edge", "Edge", "msedge", "msedge.exe"}
	case "darwin":
		home := t.TempDir()
		t.Setenv("HOME", home)
		where = filepath.Join(home, "Applications", "Brave Browser.app", "Contents", "MacOS", "Brave Browser")
		names = []string{"brave", "Brave"}
	default:
		t.Skip("browsers are on PATH here, under their own names")
	}
	if err := os.MkdirAll(filepath.Dir(where), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(where, []byte("a browser"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The one installed where the test put it, or one already installed in the
	// same place under the system's own folders.
	suffix := where[len(filepath.Dir(filepath.Dir(filepath.Dir(where)))):]
	for _, n := range names {
		if got, err := resolvePinned(n); err != nil || !strings.HasSuffix(got, suffix) {
			t.Errorf("resolvePinned(%q) = %q, %v; want the browser installed as …%s", n, got, err, suffix)
		}
	}
	if _, err := resolvePinned("netscape"); err == nil {
		t.Error("a name that is no browser Flockdeck knows was resolved")
	}
	if _, err := resolvePinned(filepath.Join(t.TempDir(), "nope", "chrome.exe")); err == nil {
		t.Error("a path with nothing at it was resolved")
	}
}

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

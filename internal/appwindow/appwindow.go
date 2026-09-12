// Package appwindow opens the user interface in its own window.
//
// A Chromium-based browser in "app mode" gives a chromeless window with no
// tabs, address bar or bookmarks, which reads as a native application window
// while keeping the whole program a single dependency-free binary. If no such
// browser is available the page is opened in the default browser instead.
package appwindow

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/sysproc"
)

// BrowserEnv names a specific browser binary to use, overriding the search.
const BrowserEnv = "FLOCKDECK_BROWSER"

// legacyBrowserEnv is what this setting was called before the program was
// renamed. Unlike the variables a pane is handed, this one the user sets
// themselves — silently ignoring a setting they had already made would look
// like the override had stopped working. Read a release longer, then drop.
const legacyBrowserEnv = "PERCH_BROWSER"

// pinnedBrowser returns the browser the user pinned, and the variable it came
// from so a failure can name the one they actually set rather than the one
// they didn't.
//
// Surrounding quotes are dropped. cmd.exe keeps them in the value of `set
// FLOCKDECK_BROWSER="C:\Program Files\…"`, which is how a path with spaces is
// habitually written there, and a quoted path is not one anything can find.
func pinnedBrowser() (prog, from string) {
	prog, from = os.Getenv(BrowserEnv), BrowserEnv
	if prog == "" {
		prog, from = os.Getenv(legacyBrowserEnv), legacyBrowserEnv
	}
	return strings.Trim(prog, `"`), from
}

// ErrNoBrowser is returned when nothing could be found to display the UI.
var ErrNoBrowser = errors.New("no browser found to display the interface")

// ErrHandedOff is returned by Wait when the browser gave the window to a
// process of its own that was already running, and exited. The window is open,
// but nothing here can tell when it closes.
var ErrHandedOff = errors.New("the window was handed to a browser already running")

// handOffWithin is how soon after starting a successful exit counts as a hand
// off rather than as the window being closed. A Chromium browser runs one
// process per profile, and a second launch on a profile in use passes its
// window to that process and exits at once — which is every launch while
// another instance's window is open, since they share the profile. Read as the
// window closing, that stopped the new instance the moment it started. Nobody
// opens the window and closes it again inside this.
const handOffWithin = 5 * time.Second

// Window is a running UI window.
type Window struct {
	cmd     *exec.Cmd
	started time.Time
	// AppMode reports whether the window is a dedicated app window rather than
	// a tab in the user's ordinary browser.
	AppMode bool
	// Program is the browser that was launched, for diagnostics.
	Program string
}

// Wait blocks until an app-mode window is closed. It returns immediately for a
// tab opened in the default browser, whose lifetime cannot be observed, and
// returns ErrHandedOff for a window another browser process took over.
func (w *Window) Wait() error {
	if w == nil || w.cmd == nil || !w.AppMode {
		return nil
	}
	err := w.cmd.Wait()
	if err == nil && time.Since(w.started) < handOffWithin {
		return ErrHandedOff
	}
	return err
}

// Close terminates an app-mode window.
func (w *Window) Close() {
	if w != nil && w.cmd != nil && w.cmd.Process != nil {
		_ = w.cmd.Process.Kill()
	}
}

// Open displays url in a window. profileDir holds the browser profile used for
// app mode, keeping it separate from the user's own browsing profile so the
// window opens clean and does not disturb their session.
func Open(url, profileDir string) (*Window, error) {
	if prog, from := pinnedBrowser(); prog != "" {
		path, err := exec.LookPath(prog)
		if err != nil {
			return nil, fmt.Errorf("%s=%q: %w", from, prog, err)
		}
		// One that is found but will not start is named too: without it,
		// nothing in the message says the choice of browser was the user's
		// own setting rather than something Flockdeck got wrong.
		w, err := startAppMode(path, url, profileDir)
		if err != nil {
			return nil, fmt.Errorf("%s=%q: %w", from, prog, err)
		}
		return w, nil
	}
	if path := findBrowser(); path != "" {
		if w, err := startAppMode(path, url, profileDir); err == nil {
			return w, nil
		}
	}
	// Better an ordinary tab than no interface at all.
	if err := openDefaultBrowser(url); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoBrowser, err)
	}
	return &Window{AppMode: false, Program: "default browser"}, nil
}

// defaultWidth and defaultHeight are the window's size where the screen has
// room for it.
const defaultWidth, defaultHeight = 1440, 900

// fitWindow is the window's size on a screen whose work area is w by h, or on
// one of unknown size when either is zero: the default where it fits, and
// otherwise nine tenths of what there is.
//
// Chromium opens an app window at exactly the size it is asked for, however
// much larger than the screen that is, so on a 1366x768 laptop the default put
// the window's own close button off the right of the screen and its bottom
// rows below it.
func fitWindow(w, h int) (int, int) {
	if w <= 0 || h <= 0 {
		return defaultWidth, defaultHeight
	}
	return min(defaultWidth, w*9/10), min(defaultHeight, h*9/10)
}

// windowArgs are the browser arguments that open pageURL as an app window on
// profileDir, on a screen whose work area is workW by workH, or of unknown
// size when either is zero.
func windowArgs(pageURL, profileDir string, workW, workH int) []string {
	args := []string{
		"--app=" + pageURL,
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-features=Translate,MediaRouter",
	}
	return append(args, placementArgs(profileDir, pageURL, workW, workH)...)
}

// placementArgs say how big the window opens, and where.
//
// The browser keeps an app window's size and place as it closes, but does not
// open the next one there: Edge opens it at a default of its own, and the
// size given at every launch put a window the user had resized or moved back
// at the default every time. So what it kept is read here and given back:
// the size where the screen has room for it, and the place where the whole
// window lies on the screen. A first window, or one kept on a screen with more
// room than this one, gets fitWindow's size. A window left maximised comes
// back at the size and place it had before, which is what is kept for it;
// Edge ignores --start-maximized for an app window.
func placementArgs(profileDir, pageURL string, workW, workH int) []string {
	p, ok := savedPlacement(profileDir, pageURL)
	if !ok || (workW > 0 && workH > 0 && (p.w > workW || p.h > workH)) {
		w, h := fitWindow(workW, workH)
		return []string{fmt.Sprintf("--window-size=%d,%d", w, h)}
	}
	args := []string{fmt.Sprintf("--window-size=%d,%d", p.w, p.h)}
	if workW > 0 && workH > 0 && p.x >= 0 && p.y >= 0 && p.x+p.w <= workW && p.y+p.h <= workH {
		args = append(args, fmt.Sprintf("--window-position=%d,%d", p.x, p.y))
	}
	return args
}

// placement is a window's size and place as the browser kept them.
type placement struct{ x, y, w, h int }

// savedPlacement reads what the browser kept of the window at pageURL from an
// earlier run on profileDir.
//
// Chromium keeps an app window's bounds under a name made of the URL's host
// and path, leaving out the port, so they carry over from run to run although
// each run is on a port of its own. Its preference names are split at dots:
// 127.0.0.1_/ is kept as 127 > 0 > 0 > 1_/.
func savedPlacement(profileDir, pageURL string) (placement, bool) {
	u, err := url.Parse(pageURL)
	if err != nil || u.Hostname() == "" {
		return placement{}, false
	}
	data, err := os.ReadFile(filepath.Join(profileDir, "Default", "Preferences"))
	if err != nil {
		return placement{}, false
	}
	var node any
	if json.Unmarshal(data, &node) != nil {
		return placement{}, false
	}
	page := u.EscapedPath()
	if page == "" {
		page = "/"
	}
	keys := append([]string{"browser", "app_window_placement"}, strings.Split(u.Hostname()+"_"+page, ".")...)
	for _, key := range keys {
		m, ok := node.(map[string]any)
		if !ok {
			return placement{}, false
		}
		if node, ok = m[key]; !ok {
			return placement{}, false
		}
	}
	bounds, ok := node.(map[string]any)
	if !ok {
		return placement{}, false
	}
	num := func(k string) int { v, _ := bounds[k].(float64); return int(v) }
	p := placement{x: num("left"), y: num("top"), w: num("right") - num("left"), h: num("bottom") - num("top")}
	return p, p.w > 0 && p.h > 0
}

// startAppMode launches a chromeless window pointed at url.
func startAppMode(path, url, profileDir string) (*Window, error) {
	w, h := workArea()
	cmd := exec.Command(path, windowArgs(url, profileDir, w, h)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", path, err)
	}
	return &Window{cmd: cmd, started: time.Now(), AppMode: true, Program: path}, nil
}

// candidates lists the browsers to try, in preference order, for this platform.
func candidates() []string {
	switch runtime.GOOS {
	case "windows":
		var out []string
		for _, base := range []string{
			os.Getenv("ProgramFiles"),
			os.Getenv("ProgramFiles(x86)"),
			os.Getenv("LocalAppData"),
		} {
			if base == "" {
				continue
			}
			out = append(out,
				filepath.Join(base, `Google\Chrome\Application\chrome.exe`),
				filepath.Join(base, `Microsoft\Edge\Application\msedge.exe`),
				filepath.Join(base, `BraveSoftware\Brave-Browser\Application\brave.exe`),
				filepath.Join(base, `Chromium\Application\chrome.exe`),
			)
		}
		return append(out, "chrome.exe", "msedge.exe")

	case "darwin":
		home, _ := os.UserHomeDir()
		return darwinCandidates(home)

	default:
		return []string{
			"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
			"microsoft-edge", "brave-browser", "vivaldi",
		}
	}
}

// darwinCandidates lists the macOS browsers in preference order, each in the
// system's Applications folder and then in the user's own. A browser installed
// by somebody without an administrator's rights goes in ~/Applications, and
// looking only in /Applications gave them an ordinary tab instead of the
// application window.
func darwinCandidates(home string) []string {
	bundles := []string{
		"Google Chrome.app/Contents/MacOS/Google Chrome",
		"Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		"Brave Browser.app/Contents/MacOS/Brave Browser",
		"Chromium.app/Contents/MacOS/Chromium",
	}
	var out []string
	for _, b := range bundles {
		out = append(out, "/Applications/"+b)
		if home != "" {
			out = append(out, home+"/Applications/"+b)
		}
	}
	return out
}

// findBrowser returns the first available browser, or "".
func findBrowser() string {
	for _, c := range candidates() {
		if strings.ContainsAny(c, `/\`) {
			if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
				return c
			}
			continue
		}
		if path, err := exec.LookPath(c); err == nil {
			return path
		}
	}
	return ""
}

// openDefaultBrowser hands the URL to the desktop's own handler.
func openDefaultBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// `start` needs a title argument before the URL, and cmd.exe treats &
		// specially, so the URL is quoted.
		cmd = exec.Command("cmd", "/c", "start", "", url)
	case "darwin":
		cmd = exec.Command("/usr/bin/open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	// On Windows the handler is cmd, a console program, and a windowless
	// Flockdeck would otherwise flash a terminal up just to pass the address on.
	sysproc.NoWindow(cmd)
	return cmd.Start()
}

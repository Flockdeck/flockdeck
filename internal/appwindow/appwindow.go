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
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jmwri/flockdeck/internal/sysproc"
)

// BrowserEnv names the browser to use, overriding the search: a path, a
// program on PATH, or one of browserNames.
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

// StartError is returned by Wait when the browser failed so soon after it was
// started that its window cannot have opened: on Linux with no display to
// open it on, say, or a snap-packaged Chromium refused its profile folder.
type StartError struct {
	// Program is the browser that was started, and Code what it exited with.
	Program string
	Code    int
	// Stderr is the last few lines the browser printed, which is where it
	// says why.
	Stderr string
}

func (e *StartError) Error() string {
	msg := fmt.Sprintf("the browser %s exited with code %d as it started, before its window opened", e.Program, e.Code)
	if e.Stderr != "" {
		msg += "; it said:\n  " + strings.ReplaceAll(e.Stderr, "\n", "\n  ")
	}
	return msg
}

// closeGrace is how long Close gives the browser to close its window itself
// before it is killed.
const closeGrace = 2 * time.Second

// Window is a running UI window.
type Window struct {
	cmd     *exec.Cmd
	started time.Time
	// done is closed once the browser process has exited, with waitErr and
	// exited set: one goroutine waits on the process, so that Wait and Close
	// can both learn when it has gone.
	done    chan struct{}
	waitErr error
	exited  time.Time
	// stderr keeps the end of what the browser prints, for a StartError.
	stderr *tail
	// closing is set by Close, so that a browser it stopped is not taken
	// for one that failed to start.
	closing atomic.Bool
	// AppMode reports whether the window is a dedicated app window rather than
	// a tab in the user's ordinary browser.
	AppMode bool
	// Program is the browser that was launched, for diagnostics.
	Program string

	// redirectFile is where a one-time link was written into a local
	// redirect page for the browser to read, rather than carry on its
	// command line (see writeRedirectFile), and "" where none was: no
	// private place could be found for this browser, and it was started at
	// the link directly. cancelCleanup stops redirectFile's own backstop
	// timer, once Close removes it itself.
	redirectFile  string
	cancelCleanup func()
}

// RedirectFile is where the one-time link this window was opened at was
// written into a local redirect file for the browser to read, or "" where
// none was and the browser was started at the link itself. A caller that can
// say precisely when that link stops working -- see server.SetLinkFile --
// should remove the file itself at that moment, rather than leave it to this
// package's own backstop timer.
func (w *Window) RedirectFile() string {
	if w == nil {
		return ""
	}
	return w.redirectFile
}

// arm records that file is this window's redirect file, if it has one, and
// starts its backstop cleanup timer.
func (w *Window) arm(file string) {
	if file == "" {
		return
	}
	w.redirectFile = file
	w.cancelCleanup = scheduleCleanup(file, redirectFileLife)
}

// removeRedirectFile cancels this window's backstop timer and removes its
// redirect file itself: closing the window is as good a moment as the file
// is ever going to get, and there is no reason left to wait for the timer.
func (w *Window) removeRedirectFile() {
	if w.cancelCleanup != nil {
		w.cancelCleanup()
		w.cancelCleanup = nil
	}
	if w.redirectFile != "" {
		_ = os.Remove(w.redirectFile)
		w.redirectFile = ""
	}
}

// Wait blocks until an app-mode window is closed. It returns immediately for a
// tab opened in the default browser, whose lifetime cannot be observed,
// returns ErrHandedOff for a window another browser process took over, and a
// *StartError for a browser that failed before its window could have opened.
//
// Such a browser used to come back as its own exit status, which reads the
// same as the window being closed, and the application stopped with it -- at
// once, and without a word, since what the browser had said went nowhere.
func (w *Window) Wait() error {
	if w == nil || w.cmd == nil || !w.AppMode {
		return nil
	}
	<-w.done
	quick := w.exited.Sub(w.started) < handOffWithin
	var exit *exec.ExitError
	switch {
	case w.waitErr == nil && quick:
		return ErrHandedOff
	case quick && !w.closing.Load() && errors.As(w.waitErr, &exit):
		return &StartError{Program: w.Program, Code: exit.ExitCode(), Stderr: w.stderr.lastLines(stderrLines)}
	}
	return w.waitErr
}

// Close closes an app-mode window.
//
// The browser is asked first, with SIGTERM, which Chromium takes as being
// told to close: it saves the window's size and place as it goes, which is
// what the next window opens at (see placementArgs). Killed outright, as it
// always was, it saved nothing, and could count the run as a crash. Only one
// that has not gone after closeGrace is killed. A Windows process cannot be
// sent SIGTERM, so there it is killed, as before.
func (w *Window) Close() {
	if w == nil {
		return
	}
	w.removeRedirectFile()
	if w.cmd == nil || w.cmd.Process == nil {
		return
	}
	w.closing.Store(true)
	select {
	case <-w.done:
		return
	default:
	}
	if w.cmd.Process.Signal(syscall.SIGTERM) == nil {
		timer := time.NewTimer(closeGrace)
		defer timer.Stop()
		select {
		case <-w.done:
			return
		case <-timer.C:
		}
	}
	_ = w.cmd.Process.Kill()
}

// Open displays target in a window. profileDir holds the browser profile
// used for app mode, keeping it separate from the user's own browsing
// profile so the window opens clean and does not disturb their session.
//
// target is not necessarily what the browser is started at: where a private
// place could be found for whichever browser this turns out to use, it is
// wrapped in a local redirect file first (see writeRedirectFile), so that
// target -- a one-time link, in practice -- never sits on that browser's own
// command line, for as long as it runs, where any other user of the machine
// could read it (R3.7.2). The returned Window's RedirectFile names that file,
// for a caller able to remove it the moment target itself stops working.
func Open(target, profileDir string) (*Window, error) {
	if prog, from := pinnedBrowser(); prog != "" {
		path, err := resolvePinned(prog)
		if err != nil {
			return nil, fmt.Errorf("%s=%q: %w", from, prog, err)
		}
		wrapped, file := wrapForLaunch(target, path, classifyBrowser(path))
		// One that is found but will not start is named too: without it,
		// nothing in the message says the choice of browser was the user's
		// own setting rather than something Flockdeck got wrong.
		w, err := startAppMode(path, wrapped, profileDir)
		if err != nil {
			removeIfAny(file)
			return nil, fmt.Errorf("%s=%q: %w", from, prog, err)
		}
		w.arm(file)
		return w, nil
	}
	if path := findBrowser(); path != "" {
		wrapped, file := wrapForLaunch(target, path, classifyBrowser(path))
		if w, err := startAppMode(path, wrapped, profileDir); err == nil {
			w.arm(file)
			return w, nil
		}
		removeIfAny(file)
	}
	// Better an ordinary tab than no interface at all.
	wrapped, file := wrapForLaunch(target, "", kindDefault)
	if err := openDefaultBrowser(wrapped); err != nil {
		removeIfAny(file)
		return nil, fmt.Errorf("%w: %v", ErrNoBrowser, err)
	}
	win := &Window{AppMode: false, Program: "default browser"}
	win.arm(file)
	return win, nil
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
	home, _ := os.UserHomeDir()
	if dir := profileFor(path, profileDir, home); dir != profileDir {
		if err := os.MkdirAll(dir, 0o700); err == nil {
			profileDir = dir
		}
	}
	w, h := workArea()
	cmd := exec.Command(path, windowArgs(url, profileDir, w, h)...)
	stderr := &tail{max: stderrKeep}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, stderr
	// What the browser prints is read through a pipe, which the processes it
	// starts are handed too, and one of those can outlive it. Wait would wait
	// for them all to let go; this lets it give up on them a moment after the
	// browser itself has gone.
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", path, err)
	}
	win := &Window{cmd: cmd, started: time.Now(), done: make(chan struct{}), stderr: stderr, AppMode: true, Program: path}
	go func() {
		err := cmd.Wait()
		// ErrWaitDelay means the browser exited successfully and only a child
		// of its own was still holding the pipe, which is no failure of the
		// browser's.
		if errors.Is(err, exec.ErrWaitDelay) {
			err = nil
		}
		win.waitErr = err
		win.exited = time.Now()
		close(win.done)
	}()
	return win, nil
}

// snapName is the snap package's name for a browser at path -- chromium,
// brave, and so on -- and whether path is a snap at all. A snap is laid out
// as /snap/<name>/<revision>/... or, on the bin shim PATH puts first,
// /snap/bin/<name>.
func snapName(path string) (name string, ok bool) {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/snap/") {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(p, "/snap/"), "/")
	name = parts[0]
	if name == "bin" && len(parts) > 1 {
		name = parts[1]
	}
	if name == "" || name == "bin" {
		return "", false
	}
	return name, true
}

// profileFor is the profile folder the browser at path is started on:
// profileDir, except for a browser installed as a snap. Snap confines one to
// its own corner of the home folder and will not let it use a hidden folder in
// it, which is where Flockdeck keeps its state on Linux, so the window failed
// to open at all. It is given a folder where snap lets it write instead:
// ~/snap/<name>/common/flockdeck-window.
func profileFor(path, profileDir, home string) string {
	if home == "" {
		return profileDir
	}
	name, ok := snapName(path)
	if !ok {
		return profileDir
	}
	return filepath.Join(home, "snap", name, "common", "flockdeck-window")
}

// stderrKeep is how much of the end of what the browser prints is kept, and
// stderrLines how many lines of it a StartError gives.
const (
	stderrKeep  = 4096
	stderrLines = 5
)

// tail keeps the last max bytes written to it.
type tail struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

// lastLines is the last n lines kept that have something on them.
func (t *tail) lastLines(n int) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var lines []string
	for _, l := range strings.Split(string(t.buf), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
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
				// Vivaldi installs for the one user, under LocalAppData, as
				// often as for everyone. It was looked for on Linux alone, so
				// FLOCKDECK_BROWSER=vivaldi, which the usage offers, found
				// nothing here and the window did not open at all.
				filepath.Join(base, `Vivaldi\Application\vivaldi.exe`),
			)
		}
		return append(out, "chrome.exe", "msedge.exe")

	case "darwin":
		home, _ := os.UserHomeDir()
		return darwinCandidates(home)

	default:
		home, _ := os.UserHomeDir()
		return linuxCandidates(home)
	}
}

// linuxCandidates lists the Linux browsers in preference order: those on PATH,
// under the names their packages give them -- snap's Brave is plain `brave`
// -- and then those installed with Flatpak, which puts nothing on PATH but
// exports a command under a bin folder of its own, for the whole machine and
// for the one user. Without them a Flatpak Chrome, Chromium, Edge or Brave
// gave an ordinary tab instead of the application window.
func linuxCandidates(home string) []string {
	out := []string{
		"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
		"microsoft-edge", "brave-browser", "brave", "vivaldi",
	}
	for _, app := range []string{"com.google.Chrome", "org.chromium.Chromium", "com.microsoft.Edge", "com.brave.Browser"} {
		out = append(out, "/var/lib/flatpak/exports/bin/"+app)
		if home != "" {
			out = append(out, home+"/.local/share/flatpak/exports/bin/"+app)
		}
	}
	return out
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
		"Vivaldi.app/Contents/MacOS/Vivaldi",
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
		if path := available(c); path != "" {
			return path
		}
	}
	return ""
}

// available returns where the candidate c is when it is installed, or "".
func available(c string) string {
	if strings.ContainsAny(c, `/\`) {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
		return ""
	}
	if path, err := exec.LookPath(c); err == nil {
		return path
	}
	return ""
}

// browserNames are the names a browser may be pinned by, each with the pieces
// of a candidate's path, lower case and with forward slashes, that are that
// browser.
var browserNames = map[string][]string{
	"chrome":   {"google/chrome/", "google chrome.app", "google-chrome", "com.google.chrome"},
	"edge":     {"microsoft/edge/", "microsoft edge.app", "microsoft-edge", "com.microsoft.edge"},
	"brave":    {"brave"},
	"chromium": {"chromium"},
	"vivaldi":  {"vivaldi"},
}

// resolvePinned finds the browser a pinned setting names: a path, or a program
// on PATH, as it always was, or else one of browserNames, looked for where
// Flockdeck looks for browsers anyway.
//
// Chrome, Edge and Brave are on no PATH on Windows or macOS, so the setting
// the usage invites, FLOCKDECK_BROWSER=chrome, failed to start the window at
// all, with the browser installed where the search would have found it.
func resolvePinned(prog string) (string, error) {
	path, err := exec.LookPath(prog)
	if err == nil || strings.ContainsAny(prog, `/\`) {
		return path, err
	}
	name := strings.TrimSuffix(strings.ToLower(prog), ".exe")
	if name == "msedge" {
		name = "edge"
	}
	pieces, ok := browserNames[name]
	if !ok {
		return "", err
	}
	for _, c := range candidates() {
		lc := strings.ToLower(filepath.ToSlash(c))
		for _, piece := range pieces {
			if strings.Contains(lc, piece) {
				if found := available(c); found != "" {
					return found, nil
				}
			}
		}
	}
	return "", fmt.Errorf("%w, and no %s is installed where browsers are looked for either", err, prog)
}

// OpenDefault opens target in the user's default browser, as Open does when
// no app-mode browser will start. The tab it opens cannot be watched, so
// unlike Open's own use of this fallback there is no Window to remove a
// redirect file the moment its link stops working -- this relies on that
// file's own backstop timer alone (see redirectFileLife).
func OpenDefault(target string) error {
	wrapped, file := wrapForLaunch(target, "", kindDefault)
	if err := openDefaultBrowser(wrapped); err != nil {
		removeIfAny(file)
		return err
	}
	scheduleCleanup(file, redirectFileLife)
	return nil
}

// openDefaultBrowser hands the URL to the desktop's own handler.
func openDefaultBrowser(url string) error {
	name, args := defaultBrowserCommand(runtime.GOOS, url)
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	// rundll32 opens no console of its own, but a console program started from
	// a windowless Flockdeck would flash a terminal up just to pass the address
	// on, and saying so costs nothing.
	sysproc.NoWindow(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	// xdg-open, open and rundll32 hand the address on and exit, and nothing
	// waited for them: on Linux and macOS every fall back to the default
	// browser left a zombie behind for as long as Flockdeck ran. Waiting is
	// what reaps one; letting the process go, on Unix, does not.
	go func() { _ = cmd.Wait() }()
	return nil
}

// defaultBrowserCommand is the program, and its arguments, that hands url to
// the desktop's own handler on goos.
//
// On Windows that used to be `cmd /c start "" url`, and cmd.exe reads & and %
// in what it is given for itself: an address with a second query parameter
// would have been cut off at the &, and the rest run as a command of its own.
// Nothing quoted it -- Go quotes an argument for the program's own parsing,
// not for cmd.exe. rundll32 hands the URL to its handler as it stands, with no
// shell in between.
func defaultBrowserCommand(goos, url string) (string, []string) {
	switch goos {
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	case "darwin":
		return "/usr/bin/open", []string{url}
	default:
		return "xdg-open", []string{url}
	}
}

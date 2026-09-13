package appwindow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A browser installed as a snap or by Flatpak is classified as such, so its
// redirect file is written where each can actually reach it; anything else
// is treated as an ordinary install.
func TestClassifyBrowser(t *testing.T) {
	cases := []struct {
		path string
		want browserKind
	}{
		{"/snap/bin/chromium", kindSnap},
		{"/snap/brave/123/usr/bin/brave", kindSnap},
		{"/var/lib/flatpak/exports/bin/com.google.Chrome", kindFlatpak},
		{"/home/u/.local/share/flatpak/exports/bin/org.chromium.Chromium", kindFlatpak},
		{"/usr/bin/google-chrome", kindNormal},
		{`C:\Program Files\Google\Chrome\Application\chrome.exe`, kindNormal},
		{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", kindNormal},
	}
	for _, c := range cases {
		if got := classifyBrowser(c.path); got != c.want {
			t.Errorf("classifyBrowser(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// redirectDir picks a directory only this user can use, per goos and browser
// kind, without needing to run on each platform to check it: a Flatpak
// browser's sandbox is not known to reach anywhere of ours, a snap is given
// the same folder its own profile already lives in, Windows and macOS each
// have one private place of their own, and Linux prefers the desktop's own
// per-user runtime folder, falling back to a hidden one under $HOME.
func TestRedirectDirChoosesAPrivatePlacePerGoosAndKind(t *testing.T) {
	full := dirEnv{home: "/home/u", xdgRuntimeDir: "/run/user/1000", localAppData: `C:\Users\u\AppData\Local`, tmpDir: "/var/folders/xy/T"}

	for _, k := range []browserKind{kindNormal, kindSnap, kindDefault} {
		if _, ok := redirectDir("windows", k, "", full); !ok {
			t.Errorf("windows, kind %v: no directory found", k)
		}
		if _, ok := redirectDir("darwin", k, "", full); !ok {
			t.Errorf("darwin, kind %v: no directory found", k)
		}
	}
	// redirectDir joins with the host's own separator, so every comparison
	// below goes by the slash-normalised form of what it returns -- this
	// runs on Windows as readily as on the goos it is asked to reason about.
	slash := func(dir string, ok bool) (string, bool) { return filepath.ToSlash(dir), ok }

	if dir, ok := slash(redirectDir("windows", kindNormal, "", full)); !ok || !strings.HasPrefix(dir, filepath.ToSlash(full.localAppData)) {
		t.Errorf("windows = %q, %v; want it under LocalAppData", dir, ok)
	}
	if dir, ok := slash(redirectDir("darwin", kindNormal, "", full)); !ok || !strings.HasPrefix(dir, full.tmpDir) {
		t.Errorf("darwin = %q, %v; want it under TMPDIR", dir, ok)
	}
	if dir, ok := slash(redirectDir("linux", kindNormal, "", full)); !ok || !strings.HasPrefix(dir, full.xdgRuntimeDir) {
		t.Errorf("linux, XDG_RUNTIME_DIR set = %q, %v; want it there", dir, ok)
	}
	noRuntime := full
	noRuntime.xdgRuntimeDir = ""
	if dir, ok := slash(redirectDir("linux", kindNormal, "", noRuntime)); !ok || !strings.HasPrefix(dir, noRuntime.home) {
		t.Errorf("linux, no XDG_RUNTIME_DIR = %q, %v; want a folder under $HOME", dir, ok)
	}
	if dir, ok := slash(redirectDir("linux", kindSnap, "/snap/bin/chromium", full)); !ok || !strings.Contains(dir, "snap/chromium/common") {
		t.Errorf("linux snap = %q, %v; want the snap-writable common folder", dir, ok)
	}
	// A snap browser cannot use $XDG_RUNTIME_DIR directly: it only ever sees
	// its own corner of it, under a name this does not know, so it is kept
	// out of the ordinary Linux branch even though one is set.
	if dir, _ := slash(redirectDir("linux", kindSnap, "/snap/bin/chromium", full)); strings.HasPrefix(dir, full.xdgRuntimeDir) {
		t.Errorf("a snap browser was given the plain XDG_RUNTIME_DIR: %q", dir)
	}

	for _, goos := range []string{"windows", "darwin", "linux"} {
		if _, ok := redirectDir(goos, kindFlatpak, "/var/lib/flatpak/exports/bin/com.google.Chrome", full); ok {
			t.Errorf("%s: a Flatpak browser was given a directory, want none known", goos)
		}
	}

	// Nothing to go on at all: no directory, not a panic or a guess.
	if _, ok := redirectDir("windows", kindNormal, "", dirEnv{}); ok {
		t.Error("windows with no LocalAppData found a directory")
	}
	if _, ok := redirectDir("darwin", kindNormal, "", dirEnv{}); ok {
		t.Error("darwin with no TMPDIR found a directory")
	}
	if _, ok := redirectDir("linux", kindNormal, "", dirEnv{}); ok {
		t.Error("linux with neither XDG_RUNTIME_DIR nor HOME found a directory")
	}
	if _, ok := redirectDir("linux", kindSnap, "/snap/bin/chromium", dirEnv{xdgRuntimeDir: "/run/user/1000"}); ok {
		t.Error("linux snap with no HOME found a directory")
	}
}

// fileURLFor is the reverse of what a browser reading a file:// URL does: a
// Windows path gets a leading slash before its drive letter, and a Unix path,
// already starting with one, is left alone.
func TestFileURLFor(t *testing.T) {
	if got, want := fileURLFor(`C:\Users\u\flockdeck.html`), "file:///C:/Users/u/flockdeck.html"; got != want {
		t.Errorf("fileURLFor(windows path) = %q, want %q", got, want)
	}
	if got, want := fileURLFor("/home/u/flockdeck.html"), "file:///home/u/flockdeck.html"; got != want {
		t.Errorf("fileURLFor(unix path) = %q, want %q", got, want)
	}
}

// The redirect file sends the browser straight on to target, both by a meta
// refresh, for a browser that runs no script, and by location.replace, so a
// reload of the page itself does not use up the one-time link a second time
// while somebody is looking at it.
func TestRedirectHTMLRedirectsToTarget(t *testing.T) {
	const target = `http://127.0.0.1:53063/?w=a1b2&x=<script>&y="quote"`
	page := redirectHTML(target)
	if !strings.Contains(page, `http-equiv="refresh"`) {
		t.Error("no meta refresh in the redirect page")
	}
	if !strings.Contains(page, "location.replace(") {
		t.Error("no script redirect in the redirect page")
	}
	// The target is not user input -- it is built by server.WindowURL -- but
	// the page still must not let it break out of the script or the
	// attribute it sits in.
	if strings.Contains(page, "<script>&") || strings.Contains(page, `"quote"`) {
		t.Errorf("target was not escaped into the page: %s", page)
	}
}

// writeRedirectFile puts the file only where redirectDir found somewhere,
// with the directory and the file both locked down to this user (see
// lockdown_other.go and lockdown_windows.go), and falls back cleanly -- no
// file, no panic -- where redirectDir found nowhere.
func TestWriteRedirectFileWritesAPrivateFile(t *testing.T) {
	base := t.TempDir()
	t.Setenv("LocalAppData", base)
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(base, "run"))
	if err := os.MkdirAll(filepath.Join(base, "run"), 0o700); err != nil {
		t.Fatal(err)
	}

	const target = "http://127.0.0.1:1/?w=abc"
	fileURL, path, ok := writeRedirectFile(target, "", kindNormal)
	if !ok {
		t.Fatal("writeRedirectFile did not find a directory")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if !strings.Contains(string(data), target) {
		t.Errorf("the redirect file does not carry the target: %s", data)
	}
	if got := fileURLFor(path); got != fileURL {
		t.Errorf("fileURL = %q, want fileURLFor(path) = %q", fileURL, got)
	}
	if filepath.Base(path) == "" || !strings.HasPrefix(filepath.Base(path), "flockdeck-") {
		t.Errorf("path = %q, want an unguessable flockdeck- name", path)
	}

	// A Flatpak browser is given nowhere to write to at all.
	if _, _, ok := writeRedirectFile(target, "/var/lib/flatpak/exports/bin/com.google.Chrome", kindFlatpak); ok {
		t.Error("a Flatpak browser was given a redirect file")
	}
}

// scheduleCleanup removes the file it was given after the delay, and not
// before -- and a cancelled one does not remove it at all, for Close's own
// use of this once a window has already been dealt with.
func TestScheduleCleanupRemovesTheFileAfterItsDelay(t *testing.T) {
	dir := t.TempDir()

	removed := filepath.Join(dir, "removed.html")
	if err := os.WriteFile(removed, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	scheduleCleanup(removed, 20*time.Millisecond)
	if _, err := os.Stat(removed); os.IsNotExist(err) {
		t.Error("the file was removed before its delay was up")
	}
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(removed); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the file was never removed")
		}
	}

	cancelled := filepath.Join(dir, "cancelled.html")
	if err := os.WriteFile(cancelled, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cancel := scheduleCleanup(cancelled, 20*time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(cancelled); err != nil {
		t.Error("a cancelled cleanup still removed the file")
	}
}

// A Window's redirect file is removed as soon as Close is called on it,
// whether or not it ever had a process of its own to close -- a tab in the
// default browser has none, and still had a file written for it.
func TestWindowCloseRemovesItsRedirectFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "flockdeck-x.html")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	w := &Window{AppMode: false}
	w.arm(path)
	if got := w.RedirectFile(); got != path {
		t.Fatalf("RedirectFile() = %q, want %q", got, path)
	}
	w.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("Close did not remove the redirect file: %v", err)
	}
	if w.RedirectFile() != "" {
		t.Error("RedirectFile still names a path after Close")
	}
}

// A nil Window's RedirectFile is "", the same as one that was never armed,
// so a caller need not check for nil first.
func TestRedirectFileOnNilWindow(t *testing.T) {
	var w *Window
	if got := w.RedirectFile(); got != "" {
		t.Errorf("RedirectFile() on nil = %q, want \"\"", got)
	}
}

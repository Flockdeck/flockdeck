// redirect.go keeps the one-time link WindowURL hands out off the browser's
// command line, where any other user of the machine could read it and,
// within its short life, redeem it before the person who started it does
// (R3.7.2). A small HTML page that redirects to the link is written into a
// directory only this user can use, and the browser is started at that file
// instead. The file is named unguessably and removed as soon as it has done
// its job -- see Window.arm and scheduleCleanup.
package appwindow

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// browserKind is what a redirect file's directory is chosen for: some
// browsers cannot read a place another can write to freely.
type browserKind int

const (
	// kindNormal is an ordinary install, found on PATH or in its usual
	// folder, or pinned by FLOCKDECK_BROWSER to one.
	kindNormal browserKind = iota
	// kindSnap is confined to its own corner of the home folder -- the same
	// one profileFor already gives its browser profile to.
	kindSnap
	// kindFlatpak is sandboxed by bubblewrap, with no folder of ours known to
	// be both private and within what it was given access to.
	kindFlatpak
	// kindDefault is the desktop's own handler for the URL, opened by
	// openDefaultBrowser: which browser that turns out to be is not known
	// here, so it is treated as kindNormal's directory choice, on the
	// evidence that most default browsers are ordinary installs. A default
	// browser that is itself a snap or Flatpak -- Ubuntu's own Firefox, since
	// 22.04 -- may not be able to read it; see the notes this shipped with.
	kindDefault
)

// classifyBrowser reports what kind of browser is at path, for redirectDir.
func classifyBrowser(path string) browserKind {
	p := filepath.ToSlash(path)
	switch {
	case strings.HasPrefix(p, "/snap/"):
		return kindSnap
	case strings.Contains(p, "/flatpak/exports/bin/"):
		return kindFlatpak
	default:
		return kindNormal
	}
}

// dirEnv is what redirectDir reads of the machine to choose a directory,
// passed in rather than read directly so the choice can be tested for every
// goos without running on each one.
type dirEnv struct {
	// home is the user's home folder ($HOME, or os.UserHomeDir's Windows
	// equivalent).
	home string
	// xdgRuntimeDir is $XDG_RUNTIME_DIR: a per-user, mode-0700 folder on a
	// tmpfs, torn down at logout, that an ordinary Linux desktop already sets
	// up for exactly this kind of thing.
	xdgRuntimeDir string
	// localAppData is %LocalAppData%, private to the Windows account by the
	// profile's own ACLs.
	localAppData string
	// tmpDir is os.TempDir(): on macOS, a per-user folder under
	// /var/folders created mode 0700 by the OS itself. Elsewhere it is not
	// used -- on Linux it is ordinarily /tmp, shared by everybody on the
	// machine, which is exactly what this is trying not to write into.
	tmpDir string
}

// currentDirEnv reads dirEnv for the machine this is actually running on.
func currentDirEnv() dirEnv {
	home, _ := os.UserHomeDir()
	return dirEnv{
		home:          home,
		xdgRuntimeDir: os.Getenv("XDG_RUNTIME_DIR"),
		localAppData:  os.Getenv("LocalAppData"),
		tmpDir:        os.TempDir(),
	}
}

// redirectFolder is the name of the folder itself, kept apart from
// whichever private place it is put in.
const redirectFolder = "flockdeck-winlink"

// redirectDir is the directory a redirect file for a browser of kind k,
// found at browserPath, should be written into on goos -- and whether one
// could be worked out at all. Every path it returns is private to this user:
// mode 0700 and 0600 are still set explicitly on what is written there (see
// writeRedirectFile), since a folder's mode is not always what its own
// creator asked for once a parent folder or an umask has had a say.
//
// A Flatpak browser is sandboxed by bubblewrap into its own view of the
// filesystem, and nothing here is known to be both private to this user and
// within what a Flatpak's default permissions give it -- unlike Snap's
// confinement, which is worked around below the same way profileFor already
// works around it for a browser's own profile folder. So there is no
// redirectDir for one: the caller falls back to the link itself, same as
// before this existed.
func redirectDir(goos string, k browserKind, browserPath string, env dirEnv) (string, bool) {
	if k == kindFlatpak {
		return "", false
	}
	switch goos {
	case "windows":
		if env.localAppData == "" {
			return "", false
		}
		return filepath.Join(env.localAppData, "Flockdeck", redirectFolder), true
	case "darwin":
		if env.tmpDir == "" {
			return "", false
		}
		return filepath.Join(env.tmpDir, redirectFolder), true
	default: // linux, and whatever else this is asked to run on
		if k == kindSnap {
			if env.home == "" {
				return "", false
			}
			name, ok := snapName(browserPath)
			if !ok {
				return "", false
			}
			return filepath.Join(env.home, "snap", name, "common", redirectFolder), true
		}
		if env.xdgRuntimeDir != "" {
			return filepath.Join(env.xdgRuntimeDir, redirectFolder), true
		}
		// No XDG_RUNTIME_DIR -- an unusual desktop, or none at all. A hidden
		// folder under $HOME is private (mode 0700, set below) and readable
		// by an ordinary browser; only Snap's confinement, handled above,
		// cannot reach one.
		if env.home == "" {
			return "", false
		}
		return filepath.Join(env.home, ".local", "state", "flockdeck", redirectFolder), true
	}
}

// writeRedirectFile writes an unguessably named HTML file that redirects to
// target, in a directory only this user can use for a browser of kind k
// found at browserPath, and returns a file:// URL to open it at and the path
// it was written to. ok is false where no such directory could be found, or
// writing to it failed, in which case the caller should fall back to
// starting the browser at target directly.
func writeRedirectFile(target, browserPath string, k browserKind) (fileURL, path string, ok bool) {
	dir, found := redirectDir(runtime.GOOS, k, browserPath, currentDirEnv())
	if !found || !filepath.IsAbs(dir) {
		return "", "", false
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", false
	}
	if err := lockDown(dir, 0o700); err != nil {
		return "", "", false
	}
	name := "flockdeck-" + rand.Text() + ".html"
	path = filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(redirectHTML(target)), 0o600); err != nil {
		return "", "", false
	}
	if err := lockDown(path, 0o600); err != nil {
		_ = os.Remove(path)
		return "", "", false
	}
	return fileURLFor(path), path, true
}

// redirectHTML is the page writeRedirectFile writes: a redirect to target
// and nothing else, since nobody is meant to read it -- only a browser,
// immediately, and only once.
func redirectHTML(target string) string {
	attr := html.EscapeString(target)
	js, err := json.Marshal(target)
	if err != nil {
		// target is always a plain http:// URL built by WindowURL; a string
		// that will not marshal to JSON does not happen in practice, but the
		// meta refresh above still gets there without it.
		js = []byte(`""`)
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="utf-8">
<meta http-equiv="refresh" content="0;url=%s">
<title>Opening Flockdeck...</title></head>
<body>
<p>Opening Flockdeck... If nothing happens, <a href="%s">click here</a>.</p>
<script>location.replace(%s);</script>
</body></html>
`, attr, attr, js)
}

// fileURLFor is the file:// URL for the absolute path p.
func fileURLFor(p string) string {
	slashed := filepath.ToSlash(p)
	if !strings.HasPrefix(slashed, "/") {
		// A Windows path, "C:/Users/...": file URLs put a slash before the
		// drive letter.
		slashed = "/" + slashed
	}
	u := url.URL{Scheme: "file", Path: slashed}
	return u.String()
}

// redirectFileLife is how long a redirect file is kept before its own
// backstop timer removes it, for whichever caller has no better moment to
// hand it: a second launch attaching to a running instance (its own process
// exits as soon as the browser has been started, well before the window has
// necessarily loaded it) and the desktop's default-browser handler (whose
// tab cannot be watched at all). It is well past windowLinkLife, the one
// minute the link itself is good for, so that by the time this fires the
// link has always already stopped working on its own account -- this is
// only about not leaving the file itself lying around.
//
// The primary window -- the one showWindow opens directly, with a *Server*
// of its own to ask -- does better than this: see SetLinkFile's use in
// main.go, which removes the file the moment the link is redeemed or runs
// out, precisely rather than after this padding.
//
// A variable, not a constant, so a test need not wait out the full five
// minutes to see a file it left behind removed.
var redirectFileLife = 5 * time.Minute

// scheduleCleanup arranges for path to be removed after d, and returns a
// func that cancels it. It does nothing, and returns a no-op cancel, for an
// empty path -- the caller when no redirect file was written at all.
func scheduleCleanup(path string, d time.Duration) func() {
	if path == "" {
		return func() {}
	}
	t := time.AfterFunc(d, func() { _ = os.Remove(path) })
	return func() { t.Stop() }
}

// removeIfAny removes path if it names one.
func removeIfAny(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}

// wrapForLaunch is the URL to actually start a browser of kind k, found at
// browserPath (empty for the desktop's own default handler), at: target
// wrapped in a private redirect file where one could be written for it, or
// target itself where none could -- a Flatpak browser, or a machine with
// none of the private places above. file is where it was written, "" when
// it was not.
func wrapForLaunch(target, browserPath string, k browserKind) (wrapped, file string) {
	fileURL, path, ok := writeRedirectFile(target, browserPath, k)
	if !ok {
		return target, ""
	}
	return fileURL, path
}

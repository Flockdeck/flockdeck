//go:build cgo

// Package appwindow opens Flockdeck's user interface in its own native
// window.
//
// Built only with cgo available: Wails' Linux backend (WebKitGTK) needs it,
// and a build without it -- the self-hosted Docker image, deliberately
// CGO_ENABLED=0 for a small static binary -- gets appwindow_nocgo.go
// instead, whose Open always fails. That is already the ordinary path for a
// headless instance: -no-window and -detach never call Open at all (see
// showWindow in main.go), so nothing here is reached in the one build this
// actually excludes it from.
//
// It used to do that by spawning an installed Chromium-based browser as a
// separate process in "--app" mode: a chromeless window with no tabs, address
// bar or bookmarks that reads as a native application window. The window that
// opened, though, was never really Flockdeck's own -- it was owned by
// chrome.exe or msedge.exe, whichever browser answered, and Windows keys its
// taskbar identity, icon and pinning behaviour to the process that owns a
// window. Pinning "Flockdeck" to the taskbar pinned Chrome.
//
// This package now uses Wails (github.com/wailsapp/wails/v3) to embed the
// platform's own webview -- WebView2 on Windows, WebKit on macOS, WebKitGTK
// on Linux -- directly inside flockdeck.exe, so the window this opens is
// genuinely Flockdeck's own process. It is pointed at the same local HTTP
// server the rest of Flockdeck already runs (see server.WindowURL), rather
// than at assets Wails serves itself: the server is already the single
// source of truth for the interface, including for remote.flockdeck.ai
// sessions over the relay, and the window this opens is just one more client
// of it.
package appwindow

import (
	"errors"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Config is the application-level configuration Wails needs once per
// process: its name and icon, which is what identifies the window to the
// taskbar, the alt-tab switcher, and window manager.
type Config struct {
	// Name is the application's name, used as the window's title and in its
	// default about box.
	Name string
	// Description appears in the default about box.
	Description string
	// Icon is the application's icon as PNG bytes. It is what a window gets
	// as its own icon on Windows when the running binary's own resources
	// don't carry one at the specific resource ID Wails looks for first (see
	// webui.Icon), and what macOS and Linux use for the window/dock icon.
	Icon []byte
}

// defaultWidth and defaultHeight are the window's starting size, chosen the
// same as the browser-hosted window this replaces: enough to be useful on an
// ordinary laptop screen without hanging off the edge of a small one. Unlike
// the browser this used to spawn, Wails does not need to be told how to keep
// an oversized window on screen -- see WindowCentered below.
const defaultWidth, defaultHeight = 1440, 900

// Window is Flockdeck's single native window. Only one may exist per
// process: Wails' application object is a process-wide singleton, which
// suits Flockdeck's own model of one window-owning process per window --
// see attach, in main.go, for the second case (a launch that joins an
// instance already running opens its own window in its own process, exactly
// as this package's predecessor did by spawning a whole separate browser).
type Window struct {
	app *application.App
	// done is closed once Run has returned, so Close does not ask an
	// application that has already torn itself down to quit again.
	done chan struct{}
}

// errAlreadyOpen guards against a programming error: Wails' application
// object is a process-wide singleton (application.New returns the existing
// one on a second call), so a second Open in the same process would silently
// hand back a window for the first target, not open one for its own.
var errAlreadyOpen = errors.New("appwindow: Open was already called in this process")

// Open creates Flockdeck's window, pointed at target, and arranges for it to
// be shown once Run is called. It does not itself block -- Run does that,
// running the native event loop -- so that a caller can finish its own
// start-up (recording the running instance, starting background watchers)
// before handing control to it.
//
// Open, like Run, must be called from the same goroutine the program's main
// function runs on and never from inside a spawned goroutine: Wails locks
// the OS thread it is first used from (see runtime.LockOSThread in its
// pkg/application/init_desktop.go), and on Windows the native message loop
// can only be pumped from that same thread.
func Open(cfg Config, target string) (*Window, error) {
	if application.Get() != nil {
		return nil, errAlreadyOpen
	}

	app := application.New(application.Options{
		Name:        cfg.Name,
		Description: cfg.Description,
		Icon:        cfg.Icon,
		Mac: application.MacOptions{
			// Flockdeck opens exactly one window per process (see the
			// Window doc above), so the window closing is the whole
			// application ending, on macOS as everywhere else.
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  cfg.Name,
		Width:  defaultWidth,
		Height: defaultHeight,
		URL:    target,
		// Centered rather than placed by hand: Wails sizes and positions
		// this against the actual screen the window opens on, which is what
		// the browser-spawning predecessor's own fitWindow/placementArgs
		// used to do by hand, reading window-manager-specific state (a
		// Chromium profile's Preferences file) to approximate it.
		InitialPosition: application.WindowCentered,
		// A plain dark fill rather than white, so the moment before the
		// page has painted reads as part of the application rather than as
		// a blank flash; the page itself supplies the real background the
		// instant it loads.
		BackgroundColour: application.NewRGB(0x1e, 0x1e, 0x1e),
	})

	return &Window{app: app, done: make(chan struct{})}, nil
}

// Run starts the native event loop and blocks until the window closes --
// whether the user closed it, or Close was called -- returning whatever
// error the platform layer reports. It must be called from the same
// goroutine Open was (see Open's doc).
func (w *Window) Run() error {
	if w == nil {
		return nil
	}
	err := w.app.Run()
	close(w.done)
	return err
}

// Close asks the window to close, which is what unblocks a concurrent call
// to Run. It is safe to call more than once, and safe to call after Run has
// already returned -- both happen in the ordinary shutdown path, where
// something else (a signal, the UI's own Quit command) can race the user
// closing the window by hand.
func (w *Window) Close() {
	if w == nil {
		return
	}
	select {
	case <-w.done:
		return
	default:
	}
	w.app.Quit()
}

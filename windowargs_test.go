package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/appwindow"
	"github.com/jmwri/flockdeck/internal/server"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// browserArgs makes this test binary stand in for the window's browser: started
// with it naming a file, it writes the arguments it was given there and exits,
// as a launch that hands its window to a browser already running does.
const browserArgs = "FLOCKDECK_TEST_BROWSER_ARGS"

func init() {
	if out := os.Getenv(browserArgs); out != "" {
		// Written aside and renamed into place, so that the test, which may be
		// looking for it already, never reads it half written.
		if os.WriteFile(out+".part", []byte(strings.Join(os.Args[1:], "\n")), 0o600) == nil {
			_ = os.Rename(out+".part", out)
		}
		os.Exit(0)
	}
}

// The address a window's browser is started with stays on its command line for
// as long as the window is open, where any other user of the machine can read
// it, and it used to carry the token that drives every agent. Neither the
// window this run opens nor one a second launch opens onto it is given it.
func TestTheWindowsBrowserIsNotGivenTheToken(t *testing.T) {
	state := t.TempDir()
	for _, v := range []string{"APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "HOME"} {
		t.Setenv(v, state)
	}
	t.Setenv(appwindow.BrowserEnv, os.Args[0])

	ws, err := workspace.New(workspace.Options{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ws.Close)
	srv, err := server.New(ws)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	argsOf := func(what string, open func()) string {
		t.Helper()
		out := filepath.Join(t.TempDir(), "args")
		t.Setenv(browserArgs, out)
		open()
		// A second launch does not wait for the browser it starts, so what the
		// stand-in writes may not be there yet.
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			data, err := os.ReadFile(out)
			if err == nil {
				return string(data)
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: the stand-in browser was not started: %v", what, err)
			}
		}
	}
	check := func(what, args string) {
		t.Helper()
		if strings.Contains(args, srv.Token()) {
			t.Errorf("%s: the browser's command line carries the token", what)
		}
		if !strings.Contains(args, "--app="+srv.BaseURL()+"/?") {
			t.Errorf("%s: the browser was not opened onto the instance", what)
		}
	}

	check("this run's window", argsOf("this run's window", func() {
		win, err := showWindow(options{}, true, srv, func() {})
		if err != nil {
			t.Fatal(err)
		}
		_ = win.Wait()
	}))
	check("a second launch's window", argsOf("a second launch's window", func() {
		if err := attach(&store.Instance{URL: srv.BaseURL(), Token: srv.Token()}, srv.BaseURL(), "", false); err != nil {
			t.Fatal(err)
		}
	}))
}

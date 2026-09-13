package main

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
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
// it. It used to carry the token that drives every agent, and now carries
// neither that nor even the one-time link that replaced it (R3.7.2): the link
// is written into a local redirect file instead, and the browser started at
// that. Neither the window this run opens nor one a second launch opens onto
// it puts either on the browser's command line.
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
		if strings.Contains(args, srv.BaseURL()) {
			t.Errorf("%s: the browser's command line carries the instance's link: %s", what, args)
		}
		appArg := ""
		for _, line := range strings.Split(args, "\n") {
			if rest, ok := strings.CutPrefix(line, "--app="); ok {
				appArg = rest
			}
		}
		if !strings.HasPrefix(appArg, "file://") {
			t.Fatalf("%s: --app=%s, want a local redirect file rather than the instance's own link", what, appArg)
		}
		path := filePathFromFileURL(t, appArg)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: reading the redirect file at %s: %v", what, path, err)
		}
		if !strings.Contains(string(data), srv.BaseURL()+"/?") {
			t.Errorf("%s: the redirect file does not point at the instance: %s", what, data)
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

// filePathFromFileURL is the reverse of appwindow's fileURLFor, for a test
// that only has the browser's own arguments to read.
func filePathFromFileURL(t *testing.T, fileURL string) string {
	t.Helper()
	u, err := url.Parse(fileURL)
	if err != nil {
		t.Fatalf("parsing %s: %v", fileURL, err)
	}
	p := u.Path
	if runtime.GOOS == "windows" && len(p) > 2 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

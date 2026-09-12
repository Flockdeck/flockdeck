//go:build windows

package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aymanbagabas/go-pty"
)

// The Windows release is linked for the GUI subsystem, and a program linked
// that way is given no console: typed in a terminal, `flockdeck -version` and
// every error printed nothing. This builds the program the way the release
// does and runs it from cmd.exe in a pseudo-console, which is a real console,
// as a terminal's and a shell pane's are.
func TestReleaseBuildPrintsToTheTerminal(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the program")
	}
	exe := filepath.Join(t.TempDir(), "flockdeck.exe")
	build := exec.Command("go", "build", "-ldflags", "-X main.version=v9.9.9 -H=windowsgui", "-o", exe, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	for _, tc := range []struct{ arg, want string }{
		{"-version", "flockdeck v9.9.9"},           // standard output
		{"bogus", `unrecognised argument "bogus"`}, // standard error
	} {
		if got, ok := inTerminal(t, tc.want, exe, tc.arg); !ok {
			t.Errorf("flockdeck %s in a terminal showed %q, want %q in it", tc.arg, got, tc.want)
		}
	}
}

var escapes = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]|\x1b\][^\x07]*\x07`)

// inTerminal runs exe from cmd.exe in a pseudo-console and reports what the
// console showed, and whether want was in it.
func inTerminal(t *testing.T, want, exe string, args ...string) (string, bool) {
	t.Helper()
	p, err := pty.New()
	if err != nil {
		t.Fatal(err)
	}
	// Closed once only: on Windows a second Close reaches memory the first
	// has already freed.
	defer p.Close()
	c := p.Command("cmd.exe", append([]string{"/c", exe}, args...)...)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Process.Kill() }()

	var mu sync.Mutex
	var buf bytes.Buffer
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := p.Read(b)
			mu.Lock()
			buf.Write(b[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	shown := func() string {
		mu.Lock()
		defer mu.Unlock()
		return escapes.ReplaceAllString(buf.String(), "")
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if strings.Contains(shown(), want) {
			return shown(), true
		}
	}
	return shown(), false
}

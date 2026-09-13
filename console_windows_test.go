//go:build windows

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/aymanbagabas/go-pty"
	"github.com/jmwri/flockdeck/internal/store"
)

// The Windows release is linked for the GUI subsystem, and a program linked
// that way is given no console: typed in a terminal, `flockdeck -version` and
// every error printed nothing. This builds the program the way the release
// does and runs it from cmd.exe in a pseudo-console, which is a real console,
// as a terminal's and a shell pane's are.
func TestReleaseBuildPrintsToTheTerminal(t *testing.T) {
	exe := releaseBuild(t)
	for _, tc := range []struct{ arg, want string }{
		{"-version", "flockdeck v9.9.9"},           // standard output
		{"bogus", `unrecognised argument "bogus"`}, // standard error
	} {
		if got, ok := inTerminal(t, tc.want, exe, tc.arg); !ok {
			t.Errorf("flockdeck %s in a terminal showed %q, want %q in it", tc.arg, got, tc.want)
		}
	}
}

// A detached run goes on behind the terminal it was started from, and what it
// prints on the way, its address and how to stop it, is all it has to say.
// That reaches the terminal, and the terminal closing afterwards does not end
// the run.
func TestReleaseBuildDetachesFromTheTerminal(t *testing.T) {
	exe := releaseBuild(t)
	state := t.TempDir()
	t.Setenv("APPDATA", state)
	t.Setenv("LOCALAPPDATA", state)
	t.Setenv(updateEnv, "off")

	got, ok := inTerminal(t, "Running detached", exe, "-solo", "-detach", "-shell", "-C", t.TempDir())
	holdRecordedInstance(t)
	if !ok {
		t.Errorf("flockdeck -detach in a terminal showed %q, want its address and how to stop it", got)
	}
	// The terminal has been closed by now.
	out, err := exec.Command(exe, "-quit").CombinedOutput()
	if err != nil || strings.Contains(string(out), "nothing is running") {
		t.Errorf("-quit once the terminal had closed: %v, %q; want the detached run still there to stop", err, out)
	}
}

// -detach with -no-window as well kept the terminal's console, since only a
// run with a window let it go, and closing the terminal ended the run and
// every agent in it: detached, and gone with the terminal all the same.
func TestReleaseBuildDetachedWithoutAWindowOutlivesItsTerminal(t *testing.T) {
	exe := releaseBuild(t)
	state := t.TempDir()
	t.Setenv("APPDATA", state)
	t.Setenv("LOCALAPPDATA", state)
	t.Setenv(updateEnv, "off")

	got, ok := inTerminal(t, "Running detached", exe, "-solo", "-detach", "-no-window", "-shell", "-C", t.TempDir())
	holdRecordedInstance(t)
	if !ok {
		t.Errorf("flockdeck -detach -no-window in a terminal showed %q, want its address and how to stop it", got)
	}
	// The terminal has been closed by now.
	out, err := exec.Command(exe, "-quit").CombinedOutput()
	if err != nil || strings.Contains(string(out), "nothing is running") {
		t.Errorf("-quit once the terminal had closed: %v, %q; want the detached run still there to stop", err, out)
	}
}

// A -no-window run from a terminal offered Ctrl+C to stop it, and the key
// never reaches the release, which borrows the terminal's console. It says
// to run `flockdeck -quit`, the way that works.
func TestReleaseBuildOffersAStopThatWorks(t *testing.T) {
	exe := releaseBuild(t)
	state := t.TempDir()
	t.Setenv("APPDATA", state)
	t.Setenv("LOCALAPPDATA", state)
	t.Setenv(updateEnv, "off")

	got, ok := inTerminal(t, "to stop.", exe, "-solo", "-no-window", "-shell", "-C", t.TempDir())
	holdRecordedInstance(t)
	if !ok || strings.Contains(got, "Ctrl+C") || !strings.Contains(got, "flockdeck -quit") {
		t.Errorf("flockdeck -no-window in a terminal showed %q, want it to say to run `flockdeck -quit`, and nothing of Ctrl+C", got)
	}
}

// The console borrowed for a terminal launch was one handle wrapped in a File
// for standard output and another for standard error, and closed by hand as
// well when it was let go. Each File closes its handle when it is collected,
// so the number was closed three times, the last two whenever the collector
// ran -- by which time it was most likely a handle something else had been
// given since. This lends an ordinary file as both streams, lets it go, and
// holds on to the next handle given out under the same number: that one has to
// survive the collector.
func TestALentConsoleIsClosedOnceAndOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	open := func(name string) syscall.Handle {
		t.Helper()
		p, err := syscall.UTF16PtrFromString(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE,
			syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.CREATE_ALWAYS, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	lent := open("console")
	wasOut, wasErr := os.Stdout, os.Stderr
	borrow(lent, true, true)
	if os.Stdout != os.Stderr {
		t.Error("standard output and error were lent a File each for one handle")
	}
	giveBack()
	if os.Stdout != wasOut || os.Stderr != wasErr {
		t.Fatal("letting the console go did not put the standard streams back")
	}

	// Windows hands out a freed handle's number again, and soon; the files
	// opened on the way to it are held until the end, so none is freed again
	// to be handed out in its place.
	var reused syscall.Handle
	var others []syscall.Handle
	defer func() {
		for _, h := range others {
			_ = syscall.CloseHandle(h)
		}
	}()
	for i := 0; i < 256; i++ {
		h := open(fmt.Sprintf("next-%d", i))
		if h == lent {
			reused = h
			break
		}
		others = append(others, h)
	}
	if reused == 0 {
		t.Skip("no handle was given out under the lent one's number")
	}
	defer syscall.CloseHandle(reused)

	for i := 0; i < 10; i++ {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
	var written uint32
	if err := syscall.WriteFile(reused, []byte("still mine"), &written, nil); err != nil {
		t.Errorf("a handle given out after the console was let go was closed under its new owner: %v", err)
	}
}

// holdRecordedInstance takes hold of the instance a test started, from its
// record, so that the test's cleanup waits for it to stop and ends it after
// 20 s: only ever that process, whatever the test goes on to find.
func holdRecordedInstance(t *testing.T) {
	t.Helper()
	inst, _ := store.LoadInstance()
	if inst == nil {
		return
	}
	p, err := os.FindProcess(inst.PID)
	if err != nil {
		return
	}
	t.Cleanup(func() {
		stopped := make(chan struct{})
		go func() { _, _ = p.Wait(); close(stopped) }()
		select {
		case <-stopped:
		case <-time.After(20 * time.Second):
			_ = p.Kill()
			<-stopped
		}
	})
}

// releaseBuild builds the program as the release does, for the GUI subsystem.
func releaseBuild(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the program")
	}
	exe := filepath.Join(t.TempDir(), "flockdeck.exe")
	build := exec.Command("go", "build", "-ldflags", "-X main.version=v9.9.9 -H=windowsgui", "-o", exe, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return exe
}

var escapes = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]|\x1b\][^\x07]*\x07`)

// inTerminal runs exe from cmd.exe in a pseudo-console until want is shown or
// half a minute has passed, closes the console, and reports what it showed.
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

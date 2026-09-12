//go:build linux || darwin

package main

import (
	"bytes"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// -detach gives the terminal back. It used to hold the prompt for as long as
// the run lasted, and to end with the terminal, the opposite of its promise.
// Now the command returns once start-up is over, having printed the address
// and how to stop the run, and the run is still there to be stopped.
func TestDetachGivesTheTerminalBack(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the program")
	}
	exe := filepath.Join(t.TempDir(), "flockdeck")
	build := exec.Command("go", "build", "-o", exe, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv(updateEnv, "off")

	var out bytes.Buffer
	cmd := exec.Command(exe, "-solo", "-detach", "-shell", "-C", t.TempDir())
	cmd.Stdout, cmd.Stderr = &out, &out
	returned := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { returned <- cmd.Wait() }()
	var err error
	select {
	case err = <-returned:
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		<-returned
	}
	// Whatever happened, the run this started does not outlive the test.
	if inst, _ := store.LoadInstance(); inst != nil {
		t.Cleanup(func() { stopProcess(inst.PID) })
	}
	if err != nil {
		t.Fatalf("flockdeck -detach: %v, having printed %q; want it to return once started", err, out.String())
	}
	if !strings.Contains(out.String(), "Running detached") {
		t.Errorf("flockdeck -detach printed %q, want its address and how to stop it", out.String())
	}

	quit, err := exec.Command(exe, "-quit").CombinedOutput()
	if err != nil || !strings.Contains(string(quit), "stopped") {
		t.Errorf("-quit after -detach returned: %v, %q; want the detached run there to stop", err, quit)
	}
}

// stopProcess waits up to 20 s for pid to end, and then ends it.
func stopProcess(pid int) {
	gone := func() bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if gone() {
			return
		}
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

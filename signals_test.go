//go:build !windows

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// SIGHUP, which a terminal sends as it closes, and SIGTERM, which a logout or
// `kill` sends, each ask for the orderly stop Ctrl+C does. Left to the runtime
// they ended the program on the spot, with nothing saved and every agent left
// to its fate.
//
// Signals go to a copy of this test binary, which sets up the same watch run
// does and says when the stop it was handed is called.
func TestSignalsAskForTheOrderlyStop(t *testing.T) {
	if mode := os.Getenv("FLOCKDECK_TEST_SIGNALS"); mode != "" {
		signalHelper(mode == "detached")
	}

	for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGTERM} {
		cmd, r := startSignalHelper(t, "attached")
		if err := cmd.Process.Signal(sig); err != nil {
			t.Fatal(err)
		}
		rest, _ := io.ReadAll(r)
		err := cmd.Wait()
		if err != nil || !strings.Contains(string(rest), "stopping in order") {
			t.Errorf("%v: the copy ended with %v, having said %q; want the orderly stop", sig, err, rest)
		}
	}
}

// A run detached from the window promised its agents would carry on, so the
// terminal it was started from closing does not stop it; SIGTERM still does,
// in order.
func TestDetachedRunOutlivesItsTerminal(t *testing.T) {
	cmd, _ := startSignalHelper(t, "detached")
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-exited:
		t.Fatalf("a detached run ended when its terminal closed: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Errorf("a detached run ended on SIGTERM with %v; want the orderly stop", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SIGTERM did not stop a detached run")
	}
}

// signalHelper is the copy's side: the watch run sets up, and an exit status
// of 0 once the orderly stop is asked for.
func signalHelper(detached bool) {
	stopped := make(chan struct{})
	watchSignals(func() { close(stopped) }, func() { os.Exit(3) }, func() bool { return detached })
	fmt.Println("ready")
	select {
	case <-stopped:
		fmt.Println("stopping in order")
		os.Exit(0)
	case <-time.After(30 * time.Second):
		os.Exit(4)
	}
}

// startSignalHelper starts a copy of this test binary as signalHelper and
// returns once its watch is in place. The copy does not outlive the test.
func startSignalHelper(t *testing.T, mode string) (*exec.Cmd, *bufio.Reader) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSignalsAskForTheOrderlyStop$")
	cmd.Env = append(os.Environ(), "FLOCKDECK_TEST_SIGNALS="+mode)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	timer := time.AfterFunc(30*time.Second, func() { _ = cmd.Process.Kill() })
	t.Cleanup(func() {
		timer.Stop()
		_ = cmd.Process.Kill()
	})
	r := bufio.NewReader(out)
	if line, err := r.ReadString('\n'); line != "ready\n" {
		t.Fatalf("the copy said %q, %v before it was signalled", line, err)
	}
	return cmd, r
}

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
	if os.Getenv("FLOCKDECK_TEST_SIGNALS") == "1" {
		stopped := make(chan struct{})
		watchSignals(func() { close(stopped) }, func() { os.Exit(3) })
		fmt.Println("ready")
		select {
		case <-stopped:
			fmt.Println("stopping in order")
			os.Exit(0)
		case <-time.After(30 * time.Second):
			os.Exit(4)
		}
	}

	for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGTERM} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSignalsAskForTheOrderlyStop$")
		cmd.Env = append(os.Environ(), "FLOCKDECK_TEST_SIGNALS=1")
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		// Whatever happens below, the copy does not outlive the test.
		timer := time.AfterFunc(30*time.Second, func() { _ = cmd.Process.Kill() })

		r := bufio.NewReader(out)
		if line, err := r.ReadString('\n'); line != "ready\n" {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			timer.Stop()
			t.Fatalf("%v: the copy said %q, %v before it was signalled", sig, line, err)
		}
		if err := cmd.Process.Signal(sig); err != nil {
			t.Fatal(err)
		}
		rest, _ := io.ReadAll(r)
		err = cmd.Wait()
		timer.Stop()
		if err != nil || !strings.Contains(string(rest), "stopping in order") {
			t.Errorf("%v: the copy ended with %v, having said %q; want the orderly stop", sig, err, rest)
		}
	}
}

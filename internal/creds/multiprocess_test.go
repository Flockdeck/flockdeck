package creds

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	envWriter = "FLOCKDECK_CREDS_TEST_WRITER" // the writer's number, when this binary is one
	envStart  = "FLOCKDECK_CREDS_TEST_START"  // unix nanoseconds the writers wait for, so they start together
)

const (
	writers       = 8
	keysPerWriter = 5
)

// runWriter is what the test binary does when it is started as a writer: wait
// for the start time, save keysPerWriter keys under its own ids, and exit 0 only
// if every save succeeded. It never runs the suite, and starts no process.
func runWriter() int {
	n := os.Getenv(envWriter)
	if start, err := strconv.ParseInt(os.Getenv(envStart), 10, 64); err == nil {
		for time.Now().UnixNano() < start {
		}
	}
	for j := range keysPerWriter {
		if err := Set(fmt.Sprintf("w%s-%d", n, j), fmt.Sprintf("sk-test-%s-%d", n, j)); err != nil {
			fmt.Fprintln(os.Stderr, "writer", n, err)
			return 1
		}
	}
	return 0
}

// Keys saved by separate processes at the same moment, as `flockdeck keys set`
// run in several terminals and the window saving a key are, are all kept. Each
// save reads the store, adds its key and writes the whole store back, so
// without a lock across processes one wrote back a store without another's key.
// The writers are this test binary run again; TestMain turns each straight
// into a writer, which starts nothing, and depth.Guard stops a chain of them.
func TestKeysSetFromManyProcessesAreAllKept(t *testing.T) {
	isolateConfig(t)
	exe, err := os.Executable()
	if err != nil {
		t.Skip("cannot find the test binary:", err)
	}
	start := time.Now().Add(time.Second).UnixNano()
	cmds := make([]*exec.Cmd, writers)
	outs := make([]*strings.Builder, writers)
	for i := range cmds {
		outs[i] = &strings.Builder{}
		// The environment is this test's, which isolateConfig pointed at a
		// temporary directory, plus what makes the child a writer.
		cmd := exec.Command(exe)
		cmd.Env = append(os.Environ(), envWriter+"="+strconv.Itoa(i), envStart+"="+strconv.FormatInt(start, 10))
		cmd.Stdout, cmd.Stderr = outs[i], outs[i]
		if err := cmd.Start(); err != nil {
			t.Fatalf("start writer %d: %v", i, err)
		}
		cmds[i] = cmd
	}
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Errorf("writer %d: %v: %s", i, err, outs[i])
		}
	}
	names, err := Names()
	if err != nil || len(names) != writers*keysPerWriter {
		t.Errorf("the store holds %d keys (%v), want all %d", len(names), err, writers*keysPerWriter)
	}
}

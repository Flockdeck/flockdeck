package tool

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"testing"
	"time"
)

// A command that leaves something running in the background with its output
// still attached -- a dev server, a watcher, a daemon -- has finished as far as
// anybody asked, and the tool must come back rather than wait on the pipe for
// as long as the background program lives.
func TestACommandThatLeavesAChildBehindStillReturns(t *testing.T) {
	line := `sh -c "sleep 20 & echo started"`
	if runtime.GOOS == "windows" {
		line = `cmd /c start /b ping -n 20 127.0.0.1`
	}
	// Not t.TempDir: the child left behind is still working in the directory
	// when the test ends, which is the point, and Windows will not remove a
	// directory a running program is in.
	dir, err := os.MkdirTemp("", "flockdeck-wait-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	set, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	run, _ := set.Lookup("run_command")
	args, _ := json.Marshal(map[string]any{"command": line, "timeout_seconds": 2})

	start := time.Now()
	if _, err := run.Run(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 12*time.Second {
		t.Errorf("run_command took %s, held up by a child that outlived the command", took.Round(time.Second))
	}
}

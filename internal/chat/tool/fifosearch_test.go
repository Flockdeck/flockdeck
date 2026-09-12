package tool

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A named pipe in the tree is stepped over by a search rather than opened and
// waited on, and a search pointed at one is refused.
func TestASearchDoesNotWaitOnANamedPipe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("named pipes do not live in the file tree on Windows")
	}
	root := newRoot(t)
	write(t, root, "a.txt", "needle\n")
	if err := exec.Command("mkfifo", filepath.Join(root.Dir(), "pipe")).Run(); err != nil {
		t.Skipf("cannot make a named pipe here: %v", err)
	}

	type result struct {
		out string
		err error
	}
	search := func(args map[string]any) result {
		done := make(chan result, 1)
		go func() {
			out, err := call(t, &grepTool{root: root}, args)
			done <- result{out, err}
		}()
		select {
		case r := <-done:
			return r
		case <-time.After(10 * time.Second):
			t.Fatalf("grep %v waited on the pipe", args)
			return result{}
		}
	}

	if r := search(map[string]any{"pattern": "needle"}); r.err != nil || !strings.Contains(r.out, "a.txt:1: needle") {
		t.Errorf("searching the tree: %q, %v", r.out, r.err)
	}
	if r := search(map[string]any{"pattern": "needle", "path": "pipe"}); r.err == nil || !strings.Contains(r.err.Error(), "not a regular file") {
		t.Errorf("searching the pipe itself: %q, %v", r.out, r.err)
	}
}

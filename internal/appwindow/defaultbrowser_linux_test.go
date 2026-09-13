//go:build linux

package appwindow

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// zombieChildren counts the children of this process that have exited and
// not been waited for.
func zombieChildren() int {
	me := strconv.Itoa(os.Getpid())
	entries, _ := os.ReadDir("/proc")
	n := 0
	for _, e := range entries {
		raw, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		s := string(raw)
		f := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
		if len(f) > 1 && f[0] == "Z" && f[1] == me {
			n++
		}
	}
	return n
}

// xdg-open hands the address on and exits, and nothing waited for it, so
// every fall back to the default browser left a zombie behind for as long as
// Flockdeck ran.
func TestTheDefaultBrowserHandlerIsWaitedFor(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "xdg-open"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	for i := 0; i < 3; i++ {
		if err := openDefaultBrowser("http://127.0.0.1:1/"); err != nil {
			t.Fatal(err)
		}
	}
	for deadline := time.Now().Add(5 * time.Second); zombieChildren() > 0; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%d handlers of the default browser are left as zombies", zombieChildren())
		}
	}
}

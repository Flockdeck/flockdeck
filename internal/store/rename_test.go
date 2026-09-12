package store

import (
	"os"
	"runtime"
	"testing"
	"time"
)

// A save that cannot succeed has to fail at once where nothing ever holds a
// file against a rename. Waiting the contention budget out cost a second a
// save, and the shutdown saves every open project in turn.
func TestAFailedSaveIsNotWaitedOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows answers a rename over a directory with access denied, which is also what a held file gets, so it is waited out there")
	}
	isolateConfig(t)
	p, err := path("/repo/a")
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatalf("block: %v", err)
	}

	start := time.Now()
	if err := Save("/repo/a", &State{}); err == nil {
		t.Fatal("saved over a directory")
	}
	if elapsed := time.Since(start); elapsed > contentionBudget/2 {
		t.Errorf("a save that could not succeed took %s to fail", elapsed)
	}
}

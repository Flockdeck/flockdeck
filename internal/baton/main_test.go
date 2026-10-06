package baton

import (
	"os"
	"testing"

	"github.com/jmwri/flockdeck/internal/testiso"
)

// TestMain keeps these tests off the real config directory, the stored API
// keys and the real network. See docs/testing.md.
func TestMain(m *testing.M) { os.Exit(testiso.Main(m)) }

// quietCleanups makes a test that prunes start with no overflow cleanup running and
// leave none running. The cleanup is single-flight and goes on in a goroutine after
// Prune has returned, so one an earlier test left running makes this test's Prune
// start none (and its files are never looked at), and the swaps of overflowRemove
// would be read by it. TempDir is called first so that its removal is registered
// before this wait, and so happens after it.
func quietCleanups(t *testing.T) {
	t.Helper()
	t.TempDir()
	WaitOverflowCleanup()
	t.Cleanup(WaitOverflowCleanup)
}

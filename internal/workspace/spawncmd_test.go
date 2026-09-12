package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestAgentsAreToldToRunThisBuildNotAnotherOnPath covers a build run from its
// own folder on a machine with another copy installed. Any `flockdeck` on PATH
// was taken to be this one, so every agent was handed the installed copy,
// whose spawn need not know the flags this build's briefing describes.
func TestAgentsAreToldToRunThisBuildNotAnotherOnPath(t *testing.T) {
	bin := t.TempDir()
	name := "flockdeck"
	if runtime.GOOS == "windows" {
		name = "flockdeck.bat" // found through PATHEXT, and enough to be run
	}
	installed := filepath.Join(bin, name)
	if err := os.WriteFile(installed, []byte("exit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	self := filepath.Join(t.TempDir(), "flockdeck-dev.exe")
	if err := os.WriteFile(self, []byte("not really a binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := spawnCommand(self); got != self {
		t.Errorf("spawn command = %q, want this build %q rather than the copy on PATH", got, self)
	}
	// The copy on PATH that is this binary is still run by name.
	if got := spawnCommand(installed); got != "flockdeck" {
		t.Errorf("spawn command = %q, want the name for the copy PATH already finds", got)
	}
}

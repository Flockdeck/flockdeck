//go:build !windows

package workspace

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A pipe where the settings file should be is refused without being opened: opening one
// blocks until somebody writes to it.
func TestAClaudeSettingsFileThatIsAPipeIsRefusedNotOpened(t *testing.T) {
	isolateEnv(t)
	repo, claude := repoWith(t)
	if err := syscall.Mkfifo(filepath.Join(claude, "settings.json"), 0o600); err != nil {
		t.Skipf("no pipe: %v", err)
	}
	done := make(chan struct{})
	go func() {
		unknownBecause(t, repo, "is not a regular file")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reading the settings blocked on a pipe")
	}
}

// A link to a device is refused whatever size it reports: /dev/zero never ends.
func TestAClaudeSettingsLinkToADeviceIsRefused(t *testing.T) {
	isolateEnv(t)
	repo, claude := repoWith(t)
	if err := os.Symlink("/dev/zero", filepath.Join(claude, "settings.json")); err != nil {
		t.Skipf("no link: %v", err)
	}
	done := make(chan struct{})
	go func() {
		unknownBecause(t, repo, "is not a regular file")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reading the settings did not stop at a device")
	}
}

// The unlistable managed folder, off Windows, when this is not root.
func TestAnUnreadableManagedDropInFolderIsUnknownUnlessRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root lists any folder")
	}
	TestAManagedDropInFolderThatCannotBeListedMakesTheCompanyUnknown(t)
}

// A file where a folder should be is a place with no settings: ENOTDIR is not an error here.
func TestAClaudeFolderThatIsAFileIsAbsentOffWindows(t *testing.T) {
	TestAClaudeFolderThatIsAFileHasNoSettings(t)
}

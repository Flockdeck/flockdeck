//go:build windows

package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// The Windows equivalent of a pipe or /dev/zero: a link to the NUL device is not a regular
// file and is refused. Making the link needs a privilege that is not always there.
func TestAClaudeSettingsLinkToTheNulDeviceIsRefused(t *testing.T) {
	isolateEnv(t)
	repo, claude := repoWith(t)
	if err := os.Symlink(`\.\NUL`, filepath.Join(claude, "settings.json")); err != nil {
		t.Skipf("no link to a device: %v", err)
	}
	unknownBecause(t, repo, "settings.json")
}

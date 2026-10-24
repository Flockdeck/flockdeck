package help

import (
	"os"
	"testing"
)

// TestMain keeps these tests off the real config directory, as the other
// packages of this module do.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "flockdeck-test-home-")
	if err != nil {
		panic(err)
	}
	for _, k := range []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		os.Setenv(k, home)
	}
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

package webui

import (
	"os"
	"testing"
)

// TestMain keeps these tests off the real config directory. See cmd/uitests for
// the front end's own tests, which do the same.
func TestMain(m *testing.M) { os.Exit(isolated(m)) }

func isolated(m *testing.M) int {
	home, err := os.MkdirTemp("", "flockdeck-test-home-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(home)
	for _, k := range []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		os.Setenv(k, home)
	}
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	return m.Run()
}

package iso

import (
	"os"
	"testing"
)

// redirect must not leave a developer's CLAUDE_CONFIG_DIR in place, or tests
// that read Claude Code's files would use the real directory.
func TestRedirectClearsClaudeConfigDir(t *testing.T) {
	// t.Setenv registers each variable to be put back when the test ends,
	// since redirect uses os.Setenv.
	for _, k := range []string{"APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH"} {
		t.Setenv(k, os.Getenv(k))
	}
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	redirect(t.TempDir())

	if v, ok := os.LookupEnv("CLAUDE_CONFIG_DIR"); ok {
		t.Errorf("CLAUDE_CONFIG_DIR = %q after redirect, want unset", v)
	}
}

// A test run by an agent inside a Flockdeck pane inherits the pane's identity.
// clearPaneEnv takes all of it out, under both prefixes, and leaves other
// FLOCKDECK_ variables alone (a CI job sets some for its tests).
func TestClearPaneEnvUnsetsWhatAPaneIsGiven(t *testing.T) {
	for _, name := range paneEnv {
		t.Setenv("FLOCKDECK_"+name, "x")
		t.Setenv("PERCH_"+name, "x")
	}
	t.Setenv("FLOCKDECK_TEST_S3_ENDPOINT", "http://127.0.0.1:1")

	clearPaneEnv()

	for _, name := range paneEnv {
		for _, prefix := range []string{"FLOCKDECK_", "PERCH_"} {
			if v, ok := os.LookupEnv(prefix + name); ok {
				t.Errorf("%s%s = %q after clearPaneEnv, want unset", prefix, name, v)
			}
		}
	}
	if os.Getenv("FLOCKDECK_TEST_S3_ENDPOINT") == "" {
		t.Error("a variable that is not a pane's was cleared")
	}
}

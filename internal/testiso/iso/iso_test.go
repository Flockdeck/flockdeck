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

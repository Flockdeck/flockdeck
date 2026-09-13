package gitx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestMain keeps the tests away from the configuration of the person running
// them. Their global git configuration -- a diff tool, autocrlf, a default
// branch, a credential helper that opens a window -- was read by every git the
// tests started, so a test could pass on one machine and fail on the next for
// reasons in neither the code nor the test. Git is given an empty system
// configuration and a global one holding only an identity, which a clone needs
// to commit with and newRepo otherwise sets for each repository it makes.
func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	dir, err := os.MkdirTemp("", "gitx-config-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitx tests:", err)
		return 1
	}
	defer os.RemoveAll(dir)
	global := filepath.Join(dir, "gitconfig")
	config := "[user]\n\tname = Test\n\temail = test@example.com\n[commit]\n\tgpgsign = false\n"
	if err := os.WriteFile(global, []byte(config), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "gitx tests:", err)
		return 1
	}
	os.Setenv("GIT_CONFIG_GLOBAL", global)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return m.Run()
}

// TestGitReadsOnlyTheTestsConfiguration checks TestMain's arrangement from the
// outside: outside any repository, the only configuration file git reads is the
// global one the tests made.
func TestGitReadsOnlyTheTestsConfiguration(t *testing.T) {
	if !Available() {
		t.Skip("git is not installed")
	}
	cmd := exec.Command("git", "config", "--list", "--show-origin")
	cmd.Dir = t.TempDir()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git config --list: %v", err)
	}
	want := filepath.ToSlash(os.Getenv("GIT_CONFIG_GLOBAL"))
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		origin, _, _ := strings.Cut(line, "\t")
		path, ok := strings.CutPrefix(origin, "file:")
		if !ok {
			continue
		}
		// A path with backslashes in it, which is every Windows path, is quoted.
		if unquoted, err := strconv.Unquote(path); err == nil {
			path = unquoted
		}
		if !strings.EqualFold(filepath.ToSlash(path), want) {
			t.Errorf("git read %q, a configuration the tests did not make", line)
		}
	}
}

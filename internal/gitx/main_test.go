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
	// GIT_CONFIG_GLOBAL does not move everything git finds from the home
	// folder: the global ignore file is read from $XDG_CONFIG_HOME/git/ignore,
	// or ~/.config/git/ignore without it, whatever the configuration says. So
	// the home folder is the temporary one too, under every name git looks for
	// it by.
	for _, name := range []string{"XDG_CONFIG_HOME", "HOME", "USERPROFILE"} {
		os.Setenv(name, dir)
	}
	// And a git started by the tests finds its repository from its working
	// folder, not from a hook or a shell that pointed these somewhere else.
	for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_COMMON_DIR"} {
		os.Unsetenv(name)
	}
	return m.Run()
}

// TestGitReadsTheTestsOwnGlobalIgnore checks the part of TestMain's arrangement
// GIT_CONFIG_GLOBAL does not cover. Git reads a global ignore file from the
// home folder whatever the configuration says, so the ignore file of the person
// running the tests decided which files a test's repository had changed. The
// one git reads has to be the one beside the tests' own configuration.
func TestGitReadsTheTestsOwnGlobalIgnore(t *testing.T) {
	if !Available() {
		t.Skip("git is not installed")
	}
	ignore := filepath.Join(filepath.Dir(os.Getenv("GIT_CONFIG_GLOBAL")), "git", "ignore")
	if err := os.MkdirAll(filepath.Dir(ignore), 0o755); err != nil {
		t.Fatal(err)
	}
	// A name no other test uses, so writing it beside them changes nothing.
	if err := os.WriteFile(ignore, []byte("flockdeck-global-ignore-probe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(ignore) })

	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	cmd := exec.Command("git", "check-ignore", "-q", "flockdeck-global-ignore-probe")
	cmd.Dir = repo
	if err := cmd.Run(); err != nil {
		t.Errorf("git did not read the tests' own global ignore file (%v): it is reading another, from the home folder of whoever runs the tests", err)
	}
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

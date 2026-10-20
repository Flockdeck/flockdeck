package iso

import (
	"os"
	"os/exec"
	"strings"
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

// This package's own tests run under Main, as every other package's do, so the
// child test below can start the same binary and see what Main leaves.
func TestMain(m *testing.M) { os.Exit(Main(m)) }

// probeEnv marks the test binary the test below starts, so that it runs the
// probe and the parent test steps aside.
const probeEnv = "FLOCKDECK_ISO_PROBE_CHILD"

// wantCleared is the list of pane variables, written out here and not read from
// paneEnv, so that a name dropped from paneEnv is caught. It must match
// workspace.paneEnv (which sets API, TOKEN, PANE, PANE_NAME and PROJECT under
// both prefixes, and AGENT and MODEL under FLOCKDECK_ only) and
// hooks.LaunchEnv. A new variable given to panes goes in both lists.
var wantCleared = []string{"API", "TOKEN", "PANE", "PANE_NAME", "PROJECT", "ROLE", "AGENT", "MODEL", "LAUNCH"}

// Main itself, not clearPaneEnv alone, has to clear the pane's identity before
// any test runs. The test starts this binary with every pane variable set and
// the real-config marker unset (so the child takes the branch that clears), and
// the child reports what it can still see. It does not depend on the
// environment of whoever runs it: a clean CI machine sets none of these.
func TestMainClearsPaneIdentityBeforeTests(t *testing.T) {
	if os.Getenv(probeEnv) != "" {
		return
	}
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name == realConfigEnv || name == realHomeEnv || strings.HasPrefix(name, "FLOCKDECK_") || strings.HasPrefix(name, "PERCH_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, probeEnv+"=1")
	for _, name := range wantCleared {
		env = append(env, "FLOCKDECK_"+name+"=x", "PERCH_"+name+"=x")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestProbePaneIdentity$", "-test.v")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the child saw pane variables: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "PASS: TestProbePaneIdentity") {
		t.Fatalf("the probe did not run:\n%s", out)
	}
}

// TestProbePaneIdentity is the child's half. Run on its own it checks nothing.
func TestProbePaneIdentity(t *testing.T) {
	if os.Getenv(probeEnv) == "" {
		t.Skip("run by TestMainClearsPaneIdentityBeforeTests")
	}
	for _, name := range wantCleared {
		for _, prefix := range []string{"FLOCKDECK_", "PERCH_"} {
			if v, ok := os.LookupEnv(prefix + name); ok {
				t.Errorf("%s%s = %q is still set after Main", prefix, name, v)
			}
		}
	}
}

package iso

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// A test run by an agent inside a Flockdeck pane inherits the pane's identity,
// and a developer may have the program's own switches set. clearOwnEnv takes all
// of them out under both prefixes, and leaves the kept ones (a CI job sets some
// for its tests).
func TestClearOwnEnvUnsetsWhatTheProgramReads(t *testing.T) {
	cleared := []string{
		"FLOCKDECK_PANE", "PERCH_PANE", "FLOCKDECK_TOKEN", "FLOCKDECK_SIGNING_KEY", "FLOCKDECK_RELAY",
		"FLOCKDECK_UPDATE", "PERCH_BASE_URL", "FLOCKDECK_SOMETHING_ADDED_LATER", "CODEX_HOME", "GIT_CONFIG_COUNT",
	}
	kept := []string{"FLOCKDECK_LIVE_AGENT_TESTS", "FLOCKDECK_TEST_S3_ENDPOINT", "FLOCKDECK_ACPTEST_MARKER", realConfigEnv}
	for _, name := range append(append([]string(nil), cleared...), kept...) {
		t.Setenv(name, "x")
	}

	clearOwnEnv()

	for _, name := range cleared {
		if v, ok := os.LookupEnv(name); ok {
			t.Errorf("%s = %q after clearOwnEnv, want unset", name, v)
		}
	}
	for _, name := range kept {
		if os.Getenv(name) == "" {
			t.Errorf("%s was cleared, want kept", name)
		}
	}
}

// Every FLOCKDECK_ variable a test reads from the environment, and does not set
// itself in the same file, is one a person or a CI job sets from outside, and so
// has to be among those Main keeps: cleared, the test would skip, or run
// without what it was told, and pass.
func TestEveryVariableATestReadsFromOutsideIsKept(t *testing.T) {
	read := regexp.MustCompile(`(?:Getenv|LookupEnv)\("(FLOCKDECK_[A-Z0-9_]+)"\)`)
	root := filepath.Join("..", "..", "..")
	seen := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(b)
		for _, m := range read.FindAllStringSubmatch(src, -1) {
			name := m[1]
			seen++
			if kept(name) || strings.Contains(src, `Setenv("`+name+`"`) || strings.Contains(src, `"`+name+`=`) {
				continue
			}
			t.Errorf("%s reads %s from the environment; add it to keptEnv in iso.go, or set it in the test", path, name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen == 0 {
		t.Fatal("no test file reading a FLOCKDECK_ variable was found, so the walk is looking in the wrong place")
	}
}

// This package's own tests run under Main, as every other package's do, so the
// child test below can start the same binary and see what Main leaves.
func TestMain(m *testing.M) { os.Exit(Main(m)) }

// probeEnv marks the test binary the test below starts, so that it runs the
// probe and the parent test steps aside. It has no FLOCKDECK_ prefix, which Main
// would clear.
const probeEnv = "ISO_PROBE_CHILD"

// The names the child is given and must not see, and the ones it must.
var (
	probeCleared = []string{
		"FLOCKDECK_API", "PERCH_API", "FLOCKDECK_TOKEN", "PERCH_TOKEN", "FLOCKDECK_PANE", "PERCH_PANE",
		"FLOCKDECK_PANE_NAME", "FLOCKDECK_PROJECT", "FLOCKDECK_ROLE", "FLOCKDECK_AGENT", "FLOCKDECK_MODEL",
		"FLOCKDECK_LAUNCH", "FLOCKDECK_SIGNING_KEY", "FLOCKDECK_RELAY", "FLOCKDECK_UPDATE", "PERCH_SOMETHING_NEW",
		"CODEX_HOME", "GIT_CONFIG_COUNT",
	}
	probeKept = []string{"FLOCKDECK_LIVE_AGENT_TESTS", "FLOCKDECK_TEST_SOMETHING", "FLOCKDECK_ACPTEST_MARKER"}
)

// Main itself, not clearOwnEnv alone, has to clear the program's variables
// before any test runs. The test starts this binary with them all set and the
// real-config marker unset (so the child takes the branch that clears), and the
// child reports what it can still see. It does not depend on the environment of
// whoever runs it: a clean CI machine sets none of these.
func TestMainClearsTheProgramsVariablesBeforeTests(t *testing.T) {
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
	for _, name := range append(append([]string(nil), probeCleared...), probeKept...) {
		env = append(env, name+"=x")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestProbeEnvironment$", "-test.v")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the child saw what it should not: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "PASS: TestProbeEnvironment") {
		t.Fatalf("the probe did not run:\n%s", out)
	}
}

// TestProbeEnvironment is the child's half. Run on its own it checks nothing.
func TestProbeEnvironment(t *testing.T) {
	if os.Getenv(probeEnv) == "" {
		t.Skip("run by TestMainClearsTheProgramsVariablesBeforeTests")
	}
	for _, name := range probeCleared {
		if v, ok := os.LookupEnv(name); ok {
			t.Errorf("%s = %q is still set after Main", name, v)
		}
	}
	for _, name := range probeKept {
		if os.Getenv(name) != "x" {
			t.Errorf("%s was cleared by Main, want kept", name)
		}
	}
	// The pair Main sets itself must survive its own sweep.
	for _, name := range []string{realConfigEnv, realHomeEnv} {
		if _, ok := os.LookupEnv(name); !ok {
			t.Errorf("%s is not set after Main", name)
		}
	}
}

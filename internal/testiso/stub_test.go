package testiso

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/testiso/iso"
)

// Every agent the catalog launches by name has a stub, and nothing else does, so
// an agent added to the catalog without one fails here and not on a developer's
// machine, where the real one would start.
func TestEveryCatalogAgentHasAStub(t *testing.T) {
	var want []string
	for _, spec := range agent.Builtins() {
		if spec.Runner == agent.RunnerCLI && spec.Exe != "" {
			want = append(want, spec.Exe)
		}
	}
	slices.Sort(want)
	got := slices.Clone(iso.StubNames)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("iso.StubNames = %v, the catalog's CLI agents are %v; update StubNames in internal/testiso/iso/stub.go", got, want)
	}
}

// A plain test run resolves each agent's name to its stub, not to whatever is
// installed. With the live agent tests switched on, the real binaries stay.
func TestAgentNamesResolveToStubs(t *testing.T) {
	if os.Getenv("FLOCKDECK_LIVE_AGENT_TESTS") != "" {
		t.Skip("the live agent tests run against the real binaries")
	}
	for _, name := range iso.StubNames {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Errorf("%s does not resolve to anything: %v", name, err)
			continue
		}
		if !iso.IsStub(path) {
			t.Errorf("%s resolves to %s, not a stub", name, path)
		}
		if home, _ := os.UserHomeDir(); !strings.HasPrefix(path, home) {
			t.Errorf("%s resolves to %s, outside the test home %s", name, path, home)
		}
	}
}

// The stub answers like an installed program, logs what it was asked when
// told to, and ends when its input does.
func TestStubAnswersAndLogs(t *testing.T) {
	if os.Getenv("FLOCKDECK_LIVE_AGENT_TESTS") != "" {
		t.Skip("the live agent tests run against the real binaries")
	}
	path, err := exec.LookPath("claude")
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "stub.log")
	t.Setenv("ISO_STUB_LOG", log)

	out, err := exec.Command(path, "--version").Output()
	if err != nil || !strings.Contains(string(out), "stub") {
		t.Fatalf("claude --version = %q, %v, want a line naming the stub", out, err)
	}

	cmd := exec.Command(path, "--session-id", "abc")
	cmd.Stdin = strings.NewReader("")
	if err := cmd.Run(); err != nil {
		t.Fatalf("the stub did not end with its input: %v", err)
	}

	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`claude args=["--version"]`, `claude args=["--session-id" "abc"]`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the log lacks %q:\n%s", want, b)
		}
	}
}

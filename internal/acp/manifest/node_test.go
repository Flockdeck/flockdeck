package manifest

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func probe(path string, lookErr error, out string, runErr error) Probe {
	return Probe{
		LookPath: func(string) (string, error) { return path, lookErr },
		Output:   func(context.Context, string, ...string) (string, error) { return out, runErr },
	}
}

func adapter(t *testing.T, id string) Adapter {
	t.Helper()
	a, ok := Default().Lookup(id)
	if !ok {
		t.Fatalf("no adapter %s", id)
	}
	return a
}

func TestNodeMissingIsATypedErrorWithTheFix(t *testing.T) {
	err := CheckNode(context.Background(), adapter(t, "codex"), probe("", errors.New("not found"), "", nil))
	var nr *NodeRequiredError
	if !errors.As(err, &nr) {
		t.Fatalf("err = %v, want *NodeRequiredError", err)
	}
	if nr.Problem != NodeMissing || nr.Adapter != "codex" || nr.Need != "20.0.0" {
		t.Errorf("err = %+v", nr)
	}
	if !strings.Contains(nr.Fix, "https://nodejs.org") || !strings.Contains(nr.Fix, "20") || !strings.Contains(nr.Fix, "does not include") {
		t.Errorf("fix = %q", nr.Fix)
	}
	if !strings.Contains(nr.Error(), "not found on PATH") {
		t.Errorf("message = %q", nr.Error())
	}
}

func TestNodeTooOld(t *testing.T) {
	err := CheckNode(context.Background(), adapter(t, "gemini"), probe("/usr/bin/node", nil, "v18.19.1\n", nil))
	var nr *NodeRequiredError
	if !errors.As(err, &nr) || nr.Problem != NodeTooOld || nr.Found != "18.19.1" {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(nr.Error(), "found 18.19.1") {
		t.Errorf("message = %q", nr.Error())
	}
}

func TestNodeThatDoesNotRunOrAnswersNonsense(t *testing.T) {
	for name, p := range map[string]Probe{
		"fails":   probe("/usr/bin/node", nil, "", errors.New("exec format error")),
		"garbage": probe("/usr/bin/node", nil, "hello", nil),
	} {
		var nr *NodeRequiredError
		if err := CheckNode(context.Background(), adapter(t, "codex"), p); !errors.As(err, &nr) || nr.Problem != NodeBroken || nr.Detail == "" {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestNodeIsFineAtOrAboveTheFloor(t *testing.T) {
	for _, v := range []string{"v20.0.0", "v20.11.1", "v24.20.0\n", "v25.0.0-nightly20261019"} {
		if err := CheckNode(context.Background(), adapter(t, "codex"), probe("/usr/bin/node", nil, v, nil)); err != nil {
			t.Errorf("node %q: %v", v, err)
		}
	}
}

func TestAnAdapterThatNeedsNoNodeIsNotChecked(t *testing.T) {
	called := false
	p := Probe{
		LookPath: func(string) (string, error) { called = true; return "", errors.New("no") },
		Output:   func(context.Context, string, ...string) (string, error) { called = true; return "", nil },
	}
	if err := CheckNode(context.Background(), adapter(t, "opencode"), p); err != nil || called {
		t.Errorf("err = %v, probe called = %v", err, called)
	}
}

// With nothing on PATH the real probe reports node as missing, not a crash.
func TestTheSystemProbeFindsNoNodeOnAnEmptyPath(t *testing.T) {
	t.Setenv("PATH", "")
	err := CheckNode(context.Background(), adapter(t, "codex"), SystemProbe())
	var nr *NodeRequiredError
	if !errors.As(err, &nr) || nr.Problem != NodeMissing {
		t.Fatalf("err = %v", err)
	}
}

func TestMinimalEnvKeepsOnlyPath(t *testing.T) {
	got := minimalEnvFrom([]string{"PATH=/bin", "ANTHROPIC_API_KEY=sk-1", "HOME=/home/x", "OPENAI_API_KEY=k"})
	joined := strings.Join(got, "|")
	if strings.Contains(joined, "KEY") || strings.Contains(joined, "HOME") || !strings.Contains(joined, "PATH=/bin") {
		t.Errorf("env = %v", got)
	}
}

func TestProbeOutputIsCappedAndShownSafely(t *testing.T) {
	var c capBuffer
	for i := 0; i < 100; i++ {
		_, _ = c.Write([]byte(strings.Repeat("x", 1000)))
	}
	if len(c.String()) != maxProbeOutput {
		t.Errorf("kept %d bytes", len(c.String()))
	}
	got := safeText("bad \x1b[31mred\x1b[0m\r\nline\u009b é"+strings.Repeat("é", 300), 50)
	if strings.ContainsAny(got, "\x1b\r\n\u009b") || len(got) > 54 || !utf8.ValidString(got) {
		t.Errorf("safeText = %q", got)
	}
}

// SystemProbe keeps at most 4 KiB of what a program prints, however much it
// prints. The program is the test binary run again as a fake node.
func TestSystemProbeCapsWhatTheProgramPrints(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	old := probeEnv
	probeEnv = func() []string { return append(minimalEnv(), fakeEnv+"=bigout") }
	defer func() { probeEnv = old }()
	out, err := SystemProbe().Output(context.Background(), exe, "--version")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 || len(out) > maxProbeOutput {
		t.Errorf("kept %d bytes, want 1 to %d", len(out), maxProbeOutput)
	}
}

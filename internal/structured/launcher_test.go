package structured

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp/manifest"
	"github.com/jmwri/flockdeck/internal/agent"
)

// ownSpec is a structured entry for Flockdeck's own adapter, which the manifest
// starts as `flockdeck acp-adapter claude`.
func ownSpec() agent.Spec {
	return agent.Spec{ID: "claude-acp", Runner: agent.RunnerACP, ACP: agent.ACPSpec{Adapter: "claude"}}
}

func envHas(env []string, name string) (string, bool) {
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

// Flockdeck's own adapter is the running program, not whatever `flockdeck` PATH
// finds first.
func TestOwnAdapterIsStartedByTheRunningExecutablesPath(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "fd-running")
	if err := os.WriteFile(exe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	looked := false
	r := &Runtime{ctx: context.Background(), opts: Options{
		Spec:       ownSpec(),
		Environ:    []string{},
		Executable: func() (string, error) { return exe, nil },
		Probe: &manifest.Probe{LookPath: func(string) (string, error) {
			looked = true
			return filepath.Join(t.TempDir(), "other-flockdeck"), nil
		}},
	}}
	cmd, setup, err := r.resolve()
	if err != nil || setup != nil {
		t.Fatalf("resolve: %v, %v", setup, err)
	}
	if cmd.Path != want {
		t.Errorf("Path = %q, want %q", cmd.Path, want)
	}
	if looked {
		t.Error("PATH was searched for flockdeck")
	}
	if len(cmd.Args) != 2 || cmd.Args[0] != "acp-adapter" || cmd.Args[1] != "claude" {
		t.Errorf("Args = %v", cmd.Args)
	}
}

// If the running program cannot be found the agent is unavailable, and PATH is
// not tried in its place.
func TestOwnAdapterWithNoExecutableIsUnavailable(t *testing.T) {
	r := &Runtime{ctx: context.Background(), opts: Options{
		Spec:       ownSpec(),
		Environ:    []string{},
		Executable: func() (string, error) { return "", errors.New("/secret/place: no such thing") },
		Probe: &manifest.Probe{LookPath: func(string) (string, error) {
			t.Error("PATH was searched")
			return "x", nil
		}},
	}}
	_, setup, err := r.resolve()
	if setup != nil || err == nil || err.Error() != "the agent is unavailable" {
		t.Errorf("resolve = %v, %v", setup, err)
	}
}

func TestOwnAdapterGetsHomeVariablesAndNoFlockdeckOnes(t *testing.T) {
	r := &Runtime{ctx: context.Background(), opts: Options{
		Spec: ownSpec(),
		Environ: []string{"HOME=/home/u", "USERPROFILE=/u/profile", "AppData=/u/appdata",
			"FLOCKDECK_TOKEN=secret", "flockdeck_api=x", "PERCH_TOKEN=s", "AWS_SECRET_ACCESS_KEY=k"},
		Executable: func() (string, error) { return os.Executable() },
	}}
	cmd, _, err := r.resolve()
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"HOME": "/home/u", "USERPROFILE": "/u/profile", "APPDATA": "/u/appdata"} {
		if got, ok := envHas(cmd.env, name); !ok || got != want {
			t.Errorf("%s = %q, %v; want %q", name, got, ok, want)
		}
	}
	for _, name := range []string{"FLOCKDECK_TOKEN", "flockdeck_api", "PERCH_TOKEN", "AWS_SECRET_ACCESS_KEY"} {
		if _, ok := envHas(cmd.env, name); ok {
			t.Errorf("%s reached the adapter", name)
		}
	}
}

// Only the adapters Flockdeck builds are given the home variables without the
// entry asking for them.
func TestOtherAdaptersGetNoHomeVariables(t *testing.T) {
	r := &Runtime{ctx: context.Background(), opts: Options{
		Spec:    agent.Spec{ID: "x", Runner: agent.RunnerACP, ACP: agent.ACPSpec{Adapter: "opencode"}},
		Environ: []string{"HOME=/home/u", "USERPROFILE=/u/profile", "APPDATA=/a"},
		Probe: &manifest.Probe{LookPath: func(string) (string, error) {
			return filepath.Join(t.TempDir(), "opencode"), nil
		}},
	}}
	cmd, _, err := r.resolve()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"HOME", "USERPROFILE", "APPDATA"} {
		if _, ok := envHas(cmd.env, name); ok {
			t.Errorf("%s was passed to a third-party adapter that did not list it", name)
		}
	}
}

package structured

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/acp/manifest"
	"github.com/jmwri/flockdeck/internal/acp/permprobe"
	"github.com/jmwri/flockdeck/internal/agent"
)

// These tests start no agent: they resolve what would be started, with the
// permission check's program replaced.

func resolveWith(t *testing.T, adapter string, run permprobe.Runner) (*Runtime, Command, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cwd := acptest.SessionDir(t)
	r := &Runtime{
		opts: Options{
			Spec: agent.Spec{ID: "fake", Runner: agent.RunnerACP,
				ACP: agent.ACPSpec{Adapter: adapter, PassEnv: []string{"OPENCODE_CONFIG_CONTENT"}}},
			Cwd:     cwd,
			Environ: []string{`OPENCODE_CONFIG_CONTENT={"permission":"allow"}`},
			Probe: &manifest.Probe{LookPath: func(string) (string, error) { return exe, nil },
				Output: func(context.Context, string, ...string) (string, error) { return "v22.0.0", nil }},
			PermissionRunner: run,
		},
		ctx:  context.Background(),
		subs: map[int]chan struct{}{},
	}
	cmd, _, err := r.resolve()
	if err != nil {
		t.Fatal(err)
	}
	return r, cmd, cwd
}

func rulesJSON(rules ...permprobe.Rule) []byte {
	b, _ := json.Marshal(map[string]any{"name": "build", "permission": rules})
	return b
}

func rule(tool, pattern, action string) permprobe.Rule {
	return permprobe.Rule{Permission: tool, Pattern: pattern, Action: action}
}

func settingOf(env []string) (string, int) {
	var v string
	n := 0
	for _, kv := range env {
		if s, ok := strings.CutPrefix(kv, "OPENCODE_CONFIG_CONTENT="); ok {
			v, n = s, n+1
		}
	}
	return v, n
}

func TestTheLaunchCheckKeepsTheUsersDenialsAndClearsTheWarning(t *testing.T) {
	var dirs []string
	var seen [][]string
	run := func(_ context.Context, path string, args []string, dir string, env []string) ([]byte, error) {
		dirs = append(dirs, dir)
		seen = append(seen, env)
		if v, n := settingOf(env); n == 1 {
			// With the setting, the agent resolves what it says.
			var s struct {
				Permission map[string]json.RawMessage `json:"permission"`
			}
			if err := json.Unmarshal([]byte(v), &s); err != nil {
				t.Errorf("the setting is not JSON: %v", err)
			}
			return rulesJSON(rule("*", "*", "allow"), rule("bash", "*", "ask"), rule("bash", "rm *", "deny"), rule("edit", "*", "ask")), nil
		}
		return rulesJSON(rule("*", "*", "allow"), rule("bash", "rm *", "deny"), rule("bash", "*", "allow"), rule("edit", "*", "allow")), nil
	}
	r, cmd, cwd := resolveWith(t, "opencode", run)
	if len(dirs) != 2 || dirs[0] != cwd || dirs[1] != cwd {
		t.Fatalf("the check ran in %v, want the pane's directory %s twice", dirs, cwd)
	}
	if _, n := settingOf(seen[0]); n != 0 {
		t.Error("the first run had the setting; it should see the user's own")
	}
	got, n := settingOf(cmd.env)
	if n != 1 {
		t.Fatalf("%d settings in the agent's environment", n)
	}
	if !strings.Contains(got, `"rm *":"deny"`) || !strings.Contains(got, `"edit":"ask"`) || strings.Contains(got, `"allow"`) {
		t.Errorf("the setting is %s", got)
	}
	if r.Unasked() {
		t.Errorf("the pane is flagged unasked: %s", r.PermissionNote())
	}
}

func TestTheLaunchCheckWarnsWhenAProjectsAgentStillAllows(t *testing.T) {
	run := func(_ context.Context, _ string, _ []string, _ string, env []string) ([]byte, error) {
		if _, n := settingOf(env); n == 1 {
			return rulesJSON(rule("bash", "*", "ask"), rule("edit", "*", "ask"), rule("bash", "*", "allow")), nil
		}
		return rulesJSON(rule("*", "*", "allow")), nil
	}
	r, _, _ := resolveWith(t, "opencode", run)
	if !r.Unasked() {
		t.Error("a shell that still allows was not flagged")
	}
}

func TestTheLaunchCheckFallsBackToTheManifestSettingAndWarns(t *testing.T) {
	run := func(context.Context, string, []string, string, []string) ([]byte, error) {
		return nil, errors.New("exit status 1")
	}
	r, cmd, _ := resolveWith(t, "opencode", run)
	got, n := settingOf(cmd.env)
	if n != 1 || got != `{"permission":{"bash":"ask","edit":"ask"}}` {
		t.Errorf("setting = %q (%d)", got, n)
	}
	if !r.Unasked() {
		t.Error("an unreadable check cleared the warning")
	}
}

func TestAnAdapterWithoutACheckIsNotAsked(t *testing.T) {
	called := false
	run := func(context.Context, string, []string, string, []string) ([]byte, error) {
		called = true
		return nil, errors.New("not wanted")
	}
	r, cmd, _ := resolveWith(t, "gemini", run)
	if called {
		t.Error("the check ran for an adapter that has none")
	}
	if got, n := settingOf(cmd.env); n != 1 || got != `{"permission":"allow"}` {
		t.Errorf("the user's own value changed: %q (%d)", got, n)
	}
	if r.Unasked() {
		t.Error("gemini is flagged")
	}
}

func TestUntilTheCheckHasRunTheManifestsWordStands(t *testing.T) {
	r := &Runtime{opts: Options{Spec: agent.Spec{ID: "fake", Runner: agent.RunnerACP, ACP: agent.ACPSpec{Adapter: "opencode"}}}}
	if !r.Unasked() {
		t.Error("opencode is not flagged before the check")
	}
}

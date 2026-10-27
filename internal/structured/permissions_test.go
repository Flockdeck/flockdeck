package structured

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/acp/manifest"
	"github.com/jmwri/flockdeck/internal/acp/permprobe"
	"github.com/jmwri/flockdeck/internal/agent"
)

// These tests start no agent: they resolve what would be started, with the
// permission check's program replaced.

func newPermRuntime(t *testing.T, ctx context.Context, adapter string, run permprobe.Runner) (*Runtime, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cwd := acptest.SessionDir(t)
	return &Runtime{
		opts: Options{
			Spec: agent.Spec{ID: "fake", Runner: agent.RunnerACP,
				ACP: agent.ACPSpec{Adapter: adapter, PassEnv: []string{"OPENCODE_CONFIG_CONTENT"}}},
			Cwd:              cwd,
			Environ:          []string{`OPENCODE_CONFIG_CONTENT={"permission":"allow"}`},
			Probe:            &manifest.Probe{LookPath: func(string) (string, error) { return exe, nil }},
			PermissionRunner: run,
		},
		ctx:  ctx,
		subs: map[int]chan struct{}{},
	}, cwd
}

func resolveWith(t *testing.T, adapter string, run permprobe.Runner) (*Runtime, Command, string) {
	t.Helper()
	r, cwd := newPermRuntime(t, context.Background(), adapter, run)
	cmd, _, err := r.resolve()
	if err != nil {
		t.Fatal(err)
	}
	return r, cmd, cwd
}

// listing is what the agent list prints for the agents given.
func listing(agents map[string][]permprobe.Rule) []byte {
	var b strings.Builder
	for _, name := range []string{"build", "general", "x"} {
		rules, ok := agents[name]
		if !ok {
			continue
		}
		j, _ := json.MarshalIndent(rules, "  ", "  ")
		b.WriteString(name + " (primary)\n  " + string(j) + "\n")
	}
	return []byte(b.String())
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

var asking = []permprobe.Rule{rule("*", "*", "allow"), rule("bash", "*", "ask"), rule("edit", "*", "ask")}

func TestTheLaunchCheckKeepsTheUsersDenialsAndClearsTheWarning(t *testing.T) {
	var dirs []string
	var seen [][]string
	run := func(_ context.Context, path string, args []string, dir string, env []string) ([]byte, error) {
		dirs = append(dirs, dir)
		seen = append(seen, env)
		if _, n := settingOf(env); n == 1 {
			// With the setting every agent asks and the denial stays.
			rules := append(append([]permprobe.Rule(nil), asking...), rule("bash", "rm *", "deny"))
			return listing(map[string][]permprobe.Rule{"build": rules, "general": rules, "x": rules}), nil
		}
		rules := []permprobe.Rule{rule("*", "*", "allow"), rule("bash", "rm *", "deny"), rule("bash", "*", "allow"), rule("edit", "*", "allow")}
		return listing(map[string][]permprobe.Rule{"build": rules, "general": rules}), nil
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

func TestTheLaunchCheckWarnsWhenAnyAgentStillAllows(t *testing.T) {
	for name, agents := range map[string]map[string][]permprobe.Rule{
		"the build agent":   {"build": append(append([]permprobe.Rule(nil), asking...), rule("bash", "*", "allow")), "general": asking},
		"a default agent":   {"build": asking, "x": append(append([]permprobe.Rule(nil), asking...), rule("bash", "*", "allow"))},
		"a subagent's edit": {"build": asking, "general": append(append([]permprobe.Rule(nil), asking...), rule("edit", "*", "allow"))},
	} {
		run := func(_ context.Context, _ string, _ []string, _ string, env []string) ([]byte, error) {
			if _, n := settingOf(env); n == 1 {
				return listing(agents), nil
			}
			return listing(map[string][]permprobe.Rule{"build": {rule("*", "*", "allow")}}), nil
		}
		r, _, _ := resolveWith(t, "opencode", run)
		if !r.Unasked() {
			t.Errorf("%s: a tool that still allows was not flagged (%s)", name, r.PermissionNote())
		}
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

// Closing a pane must not wait for the check: the runtime's context reaches it.
func TestTheLaunchCheckStopsWhenThePaneIsClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	run := func(c context.Context, _ string, _ []string, _ string, _ []string) ([]byte, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-c.Done()
		return nil, c.Err()
	}
	r, _ := newPermRuntime(t, ctx, "opencode", run)
	done := make(chan struct{})
	go func() {
		r.resolve()
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the check never started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("resolve went on waiting for the check after the context ended")
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

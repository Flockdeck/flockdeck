package session

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
)

// claudeSpec is the Claude entry from design/multi-agent.md, held here so that
// the tests below pin the argv against the contract rather than against
// whatever the catalog happens to say today. The catalog is written elsewhere;
// what must not drift is what a Claude pane is actually run with.
func claudeLaunchSpec() agent.Spec {
	return agent.Spec{
		ID: "claude", Name: "Claude Code", Runner: agent.RunnerCLI, Exe: "claude",
		Args: []agent.Arg{
			agent.Group("session", "--session-id", "{{session}}"),
			agent.Group("settings", "--settings", "{{settings}}"),
			agent.Group("model", "--model", "{{model}}"),
			agent.Lit("{{prompt}}"),
		},
		ResumeArgs: []agent.Arg{
			agent.Group("session", "--resume", "{{session}}"),
			agent.Group("settings", "--settings", "{{settings}}"),
			agent.Group("model", "--model", "{{model}}"),
		},
		Caps: agent.Caps{Hooks: true, Resume: true, Transcript: true, Trust: true, Context: agent.ContextHook},
		Models: []agent.Model{
			{ID: "", Name: "Default", Note: "whatever the CLI is set to"},
			{ID: "opus", Name: "Opus", Note: "most capable"},
			{ID: "sonnet", Name: "Sonnet", Note: "the everyday one"},
			{ID: "haiku", Name: "Haiku", Note: "fastest"},
		},
		StripEnv: []string{"CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_ENTRYPOINT",
			"CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_SSE_PORT", "CLAUDE_CODE_DONT_INHERIT_ENV"},
		Install: "https://claude.com/claude-code",
	}
}

const testSession = "11111111-2222-3333-4444-555555555555"

// TestArgvFromSpecMatchesClaude is the promise that nothing regresses for
// somebody who only ever runs Claude. A pane launched from the Spec must be
// run with the argument list ClaudeArgs produced before Specs existed, argument
// for argument: one more or one fewer and the CLI either refuses to start or
// starts a conversation that cannot be resumed, which loses the pane's history
// on the next restart.
func TestArgvFromSpecMatchesClaude(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		resume bool
	}{
		{name: "fresh with no task"},
		{name: "fresh with a task", prompt: "fix the parser"},
		{name: "a task that looks like a flag", prompt: "--verbose is the wrong flag; fix the parser"},
		{name: "resumed", resume: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg, err := Launch{
				Spec:        claudeLaunchSpec(),
				ID:          testSession,
				Prompt:      tc.prompt,
				Resume:      tc.resume,
				SelfExe:     "/bin/flockdeck",
				SettingsDir: dir,
				Endpoint:    "http://127.0.0.1:1/hook",
				Token:       "tok",
			}.Config()
			if err != nil {
				t.Fatalf("config: %v", err)
			}

			var extra []string
			if tc.prompt != "" && !tc.resume {
				extra = []string{tc.prompt}
			}
			settings := filepath.Join(dir, testSession+".settings.json")
			want := ClaudeArgs(testSession, settings, tc.resume, extra)
			if !slices.Equal(cfg.Argv, want) {
				t.Errorf("argv  = %q\nwant = %q", cfg.Argv, want)
			}
			if _, err := os.Stat(settings); err != nil {
				t.Errorf("the settings file naming the hooks was not written: %v", err)
			}
			if cfg.Kind != KindAgent {
				t.Errorf("kind = %v, want an agent pane", cfg.Kind)
			}
			// The Spec travels with the pane, because what Flockdeck believes about
			// it afterwards -- how its status is known, and what to read out of
			// its output -- is written there and nowhere else.
			if cfg.Spec.ID != "claude" {
				t.Errorf("spec = %q, want the agent the pane runs", cfg.Spec.ID)
			}
		})
	}
}

// TestModelIsAskedForOnlyWhenThereIsOne covers the field that did not exist
// before: a Claude pane with no model chosen must look exactly as it did, and
// one with a model must carry the flag and its value together.
func TestModelIsAskedForOnlyWhenThereIsOne(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		dflt    string
		want    string
		wantNot string
	}{
		{name: "nothing chosen", wantNot: "--model"},
		{name: "chosen", model: "opus", want: "--model opus"},
		{name: "the spec's default", dflt: "sonnet", want: "--model sonnet"},
		{name: "the choice beats the default", model: "haiku", dflt: "sonnet", want: "--model haiku"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := claudeLaunchSpec()
			spec.DefaultModel = tc.dflt
			cfg, err := Launch{Spec: spec, Model: tc.model, ID: testSession, SettingsDir: t.TempDir()}.Config()
			if err != nil {
				t.Fatalf("config: %v", err)
			}
			got := strings.Join(cfg.Argv, " ")
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Errorf("argv = %q, want it to contain %q", got, tc.want)
			}
			if tc.wantNot != "" && strings.Contains(got, tc.wantNot) {
				t.Errorf("argv = %q, want no %q in it", got, tc.wantNot)
			}
		})
	}
}

// TestSettingsOnlyForAnAgentHandedOne keeps the state directory from filling up
// with files nothing reads. An agent that reports its lifecycle without being
// handed a settings file -- `flockdeck chat`, told where to report in its
// environment -- must not have one written for it.
func TestSettingsOnlyForAnAgentHandedOne(t *testing.T) {
	tests := []struct {
		name string
		spec agent.Spec
		want bool
	}{
		{
			name: "hooks and a settings argument",
			spec: claudeLaunchSpec(),
			want: true,
		},
		{
			name: "hooks it reports by itself",
			spec: agent.Spec{
				ID: "anthropic", Runner: agent.RunnerAPI,
				Args: []agent.Arg{agent.Group("session", "--session", "{{session}}")},
				Caps: agent.Caps{Hooks: true, Resume: true},
			},
		},
		{
			name: "no hooks at all",
			spec: agent.Spec{ID: "codex", Runner: agent.RunnerCLI, Exe: "codex"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			got, err := Settings(tc.spec, dir, testSession, "/bin/flockdeck", "http://127.0.0.1:1/hook", "tok")
			if err != nil {
				t.Fatalf("settings: %v", err)
			}
			if (got != "") != tc.want {
				t.Errorf("settings path = %q, want one written: %v", got, tc.want)
			}
			entries, _ := os.ReadDir(dir)
			if (len(entries) > 0) != tc.want {
				t.Errorf("%d files left in the settings directory, want one written: %v", len(entries), tc.want)
			}
		})
	}
}

// TestAPIRunnerRunsFlockdeckItself covers the runner that has no CLI to find: the
// pane runs Flockdeck's own binary, which only the caller can name.
func TestAPIRunnerRunsFlockdeckItself(t *testing.T) {
	spec := agent.Spec{
		ID: "openai", Runner: agent.RunnerAPI,
		Args: []agent.Arg{
			agent.Lit("--agent"), agent.Lit("openai"),
			agent.Group("model", "--model", "{{model}}"),
			agent.Group("session", "--session", "{{session}}"),
		},
	}
	cfg, err := Launch{Spec: spec, Model: "gpt-5", ID: testSession, SelfExe: "/opt/flockdeck"}.Config()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	want := []string{"/opt/flockdeck", "chat", "--agent", "openai", "--model", "gpt-5", "--session", testSession}
	if !slices.Equal(cfg.Argv, want) {
		t.Errorf("argv  = %q\nwant = %q", cfg.Argv, want)
	}

	if _, err := (Launch{Spec: spec, ID: testSession}).Config(); err == nil {
		t.Error("an API pane with no binary to run should say so rather than starting nothing")
	}
}

// TestLookSaysWhereAnAgentComesFrom covers the message somebody sees when they
// pick an agent their machine does not have. The install line is the Spec's,
// because only the catalog knows where Codex comes from.
func TestLookSaysWhereAnAgentComesFrom(t *testing.T) {
	_, err := Look(agent.Spec{
		ID: "codex", Name: "Codex", Runner: agent.RunnerCLI,
		Exe: "flockdeck-no-such-agent-on-path", Install: "npm i -g @openai/codex",
	})
	if err == nil {
		t.Fatal("a missing CLI should be reported")
	}
	for _, want := range []string{"Codex", "npm i -g @openai/codex"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}

	// An API agent runs Flockdeck itself, so there is nothing on PATH to find and
	// nothing to complain about.
	if _, err := Look(agent.Spec{ID: "anthropic", Runner: agent.RunnerAPI}); err != nil {
		t.Errorf("an API agent needs nothing installed: %v", err)
	}
}

// TestStripEnvComesFromTheSpecs is the other half of a pane being a clean
// top-level session: the markers of the session Flockdeck was launched from go from
// every pane, whichever agent is about to run in it, because the agent that
// spawned Flockdeck has nothing to do with the one in the pane.
func TestStripEnvComesFromTheSpecs(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CODEX_SANDBOX", "1")
	t.Setenv("FLOCKDECK_TEST_KEPT", "yes")

	specs := []agent.Spec{
		claudeLaunchSpec(),
		{ID: "codex", Runner: agent.RunnerCLI, Exe: "codex", StripEnv: []string{"CODEX_SANDBOX"}},
	}
	union := StripEnvUnion(specs)
	if !slices.Contains(union, "CLAUDECODE") || !slices.Contains(union, "CODEX_SANDBOX") {
		t.Fatalf("union = %q, want every Spec's markers in it", union)
	}

	cfg, err := Launch{Spec: specs[1], ID: testSession, StripEnv: union, Env: []string{"FLOCKDECK_PANE=" + testSession}}.Config()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	for _, gone := range []string{"CLAUDECODE=", "CODEX_SANDBOX="} {
		if slices.ContainsFunc(cfg.Env, func(kv string) bool { return strings.HasPrefix(kv, gone) }) {
			t.Errorf("%s survived into a Codex pane", gone)
		}
	}
	if !slices.Contains(cfg.Env, "FLOCKDECK_TEST_KEPT=yes") {
		t.Error("the rest of the environment should be passed through untouched")
	}
	if !slices.Contains(cfg.Env, "FLOCKDECK_PANE="+testSession) {
		t.Error("the pane's own variables should be added")
	}
	// Which agent is in the pane is worth knowing inside it: a prompt or a
	// script there can say so without asking Flockdeck.
	if !slices.Contains(cfg.Env, "FLOCKDECK_AGENT=codex") {
		t.Errorf("env = %q, want the agent named in it", cfg.Env)
	}

	// A caller with no catalog to hand strips what Flockdeck always stripped, so a
	// pane started before the catalog is read is no worse off than it was.
	if slices.Contains(Env(), "CLAUDECODE=1") {
		t.Error("Env with no list given should still strip the markers it always did")
	}
}

// TestTheBuiltInMarkersAreAlwaysStripped covers an agents.json entry for claude
// that gives a stripEnv of its own. It replaces the built-in list rather than
// adding to it, so the catalog's union no longer names CLAUDECODE, and a pane
// handed that union believed it was a nested child session again.
func TestTheBuiltInMarkersAreAlwaysStripped(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	t.Setenv("MY_TOOL_MARKER", "1")

	env := EnvStripping([]string{"MY_TOOL_MARKER"})
	for _, gone := range []string{"CLAUDECODE=", "CLAUDE_CODE_ENTRYPOINT=", "MY_TOOL_MARKER="} {
		if slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, gone) }) {
			t.Errorf("%s survived a list that named only MY_TOOL_MARKER", gone)
		}
	}
}

// TestSpecEnvCannotShadowFlockdecksOwn covers a catalog entry -- a file the user may
// edit -- naming one of the variables a pane calls back on. The first copy of a
// name in an environment block is the one that counts, so the Spec's would win
// by being written first, and the pane would report its lifecycle nowhere.
func TestSpecEnvCannotShadowFlockdecksOwn(t *testing.T) {
	spec := agent.Spec{ID: "local", Runner: agent.RunnerCLI, Exe: "ollama", Env: []string{"FLOCKDECK_API=http://evil", "OLLAMA_HOST=127.0.0.1"}}
	cfg, err := Launch{Spec: spec, ID: testSession, Env: []string{"FLOCKDECK_API=http://127.0.0.1:9/hook"}}.Config()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	var api []string
	for _, kv := range cfg.Env {
		if strings.HasPrefix(kv, "FLOCKDECK_API=") {
			api = append(api, kv)
		}
	}
	if len(api) != 1 || api[0] != "FLOCKDECK_API=http://127.0.0.1:9/hook" {
		t.Errorf("FLOCKDECK_API = %q, want only Flockdeck's own", api)
	}
	if !slices.Contains(cfg.Env, "OLLAMA_HOST=127.0.0.1") {
		t.Error("the Spec's own additions should still be there")
	}
}

// TestTrustIsOnlyAskedOfAnAgentWithATrustQuestion covers the capability gate in
// front of Claude Code's configuration. An agent that never shows a trust
// dialog must not be reported as stopped by one, and arranging trust for it must
// not write to another agent's configuration.
func TestTrustIsOnlyAskedOfAnAgentWithATrustQuestion(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, map[string]any{"projects": map[string]any{}})
	cwd := filepath.Join(dir, "repo")

	hookless := agent.Spec{ID: "codex"}
	if !TrustedFor(hookless, cwd) {
		t.Error("an agent with no trust question is never stopped by one")
	}
	if err := InheritTrustFor(hookless, cwd, filepath.Join(dir, "worktree")); err != nil {
		t.Errorf("arranging trust for an agent that asks nothing should be a no-op: %v", err)
	}

	if TrustedFor(claudeLaunchSpec(), cwd) {
		t.Error("Claude's own question has not been answered for this directory")
	}
	if err := InheritTrustFor(claudeLaunchSpec(), cwd, filepath.Join(dir, "worktree")); err == nil {
		t.Error("trust must never be invented for a directory that does not have it")
	}
}

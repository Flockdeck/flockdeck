package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/perch/internal/agent"
)

// stateDir points this test's state at a directory of its own, the way the
// store's own tests do. The catalog reads agents.json out of it, and writing a
// default into the real one would edit whatever the person running the tests
// had set up.
func stateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)         // Windows
	t.Setenv("XDG_CONFIG_HOME", dir) // Linux
	t.Setenv("HOME", dir)            // macOS and fallback
	return dir
}

// writeAgents puts an agents.json where the catalog will find it.
func writeAgents(t *testing.T, body string) {
	t.Helper()
	dir := stateDir(t)
	path := filepath.Join(dir, "perch")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("make the state directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, agentsFileName), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", agentsFileName, err)
	}
}

// A user's entry is decoded onto the built-in it names rather than replacing
// it, so writing one field leaves the other thirty alone. That is the whole
// point of the file: somebody who wants Claude on Sonnet should not have to
// restate its arguments, its capabilities and its environment to say so.
func TestUserEntriesMergeFieldByFieldOverTheBuiltIns(t *testing.T) {
	builtin := []agent.Spec{{
		ID: "claude", Name: "Claude Code", Runner: agent.RunnerCLI, Exe: "claude",
		Args:   []agent.Arg{agent.Lit("--go")},
		Models: []agent.Model{{ID: "opus"}, {ID: "sonnet"}},
		Caps:   agent.Caps{Hooks: true, Resume: true},
	}}

	cases := []struct {
		name    string
		entry   string
		check   func(t *testing.T, got []agent.Spec)
		wantLen int
	}{{
		name:    "one field set, the rest kept",
		entry:   `{"id":"claude","defaultModel":"sonnet"}`,
		wantLen: 1,
		check: func(t *testing.T, got []agent.Spec) {
			if got[0].DefaultModel != "sonnet" {
				t.Errorf("defaultModel = %q, want sonnet", got[0].DefaultModel)
			}
			if got[0].Exe != "claude" || len(got[0].Args) != 1 || !got[0].Caps.Hooks {
				t.Errorf("the built-in was replaced rather than merged onto: %+v", got[0])
			}
		},
	}, {
		name:    "a list that is written is replaced whole",
		entry:   `{"id":"claude","models":[{"id":"haiku"}]}`,
		wantLen: 1,
		check: func(t *testing.T, got []agent.Spec) {
			if len(got[0].Models) != 1 || got[0].Models[0].ID != "haiku" {
				t.Errorf("models = %+v, want only haiku", got[0].Models)
			}
		},
	}, {
		name:    "an unknown id is a new agent",
		entry:   `{"id":"local","runner":"api","api":{"wire":"openai","baseURL":"http://127.0.0.1:11434/v1"}}`,
		wantLen: 2,
		check: func(t *testing.T, got []agent.Spec) {
			if got[1].ID != "local" || got[1].Runner != agent.RunnerAPI {
				t.Errorf("second entry = %+v, want the local api agent", got[1])
			}
			// An agent with no name of its own is still something the picker
			// has to draw a row for.
			if got[1].Name != "local" {
				t.Errorf("name = %q, want the id standing in for it", got[1].Name)
			}
		},
	}, {
		name:    "an entry with no id is ignored",
		entry:   `{"name":"nameless"}`,
		wantLen: 1,
		check:   func(t *testing.T, got []agent.Spec) {},
	}, {
		name:    "hidden is carried through for the caller to act on",
		entry:   `{"id":"claude","hidden":true}`,
		wantLen: 1,
		check: func(t *testing.T, got []agent.Spec) {
			if !got[0].Hidden {
				t.Error("hidden was not merged over the built-in")
			}
		},
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mergeAgents(builtin, []json.RawMessage{json.RawMessage(c.entry)})
			if len(got) != c.wantLen {
				t.Fatalf("got %d agents, want %d: %+v", len(got), c.wantLen, got)
			}
			c.check(t, got)
			if builtin[0].DefaultModel != "" || len(builtin[0].Models) != 2 {
				t.Error("the built-in table was modified in place")
			}
		})
	}
}

// Availability decides whether an agent is offered or greyed, and getting it
// wrong in either direction is a pane that will not start or an agent nobody
// is told about.
func TestAvailabilityAsksTheMachine(t *testing.T) {
	t.Setenv("PERCH_TEST_KEY", "sk-not-a-real-key")

	cases := []struct {
		name string
		spec agent.Spec
		want bool
	}{
		{"a CLI on PATH", agent.Spec{Runner: agent.RunnerCLI, Exe: "go"}, true},
		{"a CLI that is not installed", agent.Spec{Runner: agent.RunnerCLI, Exe: "perch-not-a-real-program"}, false},
		{"a CLI with no program named", agent.Spec{Runner: agent.RunnerCLI}, false},
		{"an API with a key in the environment",
			agent.Spec{Runner: agent.RunnerAPI, API: agent.APISpec{KeyEnv: []string{"PERCH_TEST_KEY"}}}, true},
		{"an API with no key anywhere",
			agent.Spec{Runner: agent.RunnerAPI, API: agent.APISpec{KeyEnv: []string{"PERCH_TEST_UNSET_KEY"}}}, false},
		{"a model server on loopback, which asks for none",
			agent.Spec{Runner: agent.RunnerAPI, API: agent.APISpec{BaseURL: "http://127.0.0.1:11434/v1"}}, true},
		{"the same by name",
			agent.Spec{Runner: agent.RunnerAPI, API: agent.APISpec{BaseURL: "http://localhost:1234/v1"}}, true},
		{"an endpoint somewhere else",
			agent.Spec{Runner: agent.RunnerAPI, API: agent.APISpec{BaseURL: "https://api.example.com/v1"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := availableAgent(c.spec); got != c.want {
				t.Errorf("availableAgent = %v, want %v", got, c.want)
			}
		})
	}
}

// A project's default is written under the path as this run spells it, and
// looked up the way every other path in this application is compared — so a
// project reached through a differently cased drive letter is still the same
// project.
func TestAProjectDefaultIsFoundHoweverThePathIsSpelled(t *testing.T) {
	f := agentsFile{Projects: map[string]agentChoice{
		filepath.Join("C:", "code", "api"): {Agent: "codex", Model: "gpt-5"},
	}}

	cases := []struct {
		name  string
		root  string
		want  string
		found bool
	}{
		{"as written", filepath.Join("C:", "code", "api"), "codex", true},
		{"with a trailing separator", filepath.Join("C:", "code", "api") + string(filepath.Separator), "codex", true},
		{"another project", filepath.Join("C:", "code", "web"), "", false},
		{"no project at all", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := f.projectDefault(c.root)
			if ok != c.found {
				t.Fatalf("found = %v, want %v", ok, c.found)
			}
			if got.Agent != c.want {
				t.Errorf("agent = %q, want %q", got.Agent, c.want)
			}
		})
	}
}

// The file is the user's, and may well have agents of their own in it. Setting
// a default has to leave everything else exactly as it was found, or the first
// use of the picker quietly throws away a hand-written entry.
func TestSettingADefaultLeavesTheRestOfTheFileAlone(t *testing.T) {
	writeAgents(t, `{
  "version": 1,
  "agents": [{ "id": "local", "name": "Local llama" }]
}`)
	root := filepath.Join("C:", "code", "api")
	if err := setAgentDefault(root, agentChoice{Agent: "claude", Model: "sonnet"}); err != nil {
		t.Fatalf("set the default: %v", err)
	}

	f, err := readAgentsFile()
	if err != nil {
		t.Fatalf("read it back: %v", err)
	}
	if len(f.Agents) != 1 || !strings.Contains(string(f.Agents[0]), "Local llama") {
		t.Errorf("the hand-written agent did not survive: %s", f.Agents)
	}
	got, ok := f.projectDefault(root)
	if !ok || got.Agent != "claude" || got.Model != "sonnet" {
		t.Errorf("project default = %+v (found %v), want claude on sonnet", got, ok)
	}

	// Setting it again replaces the entry rather than adding a second one, so
	// the file cannot end up holding two answers for one project.
	if err := setAgentDefault(root, agentChoice{Agent: "claude", Model: "opus"}); err != nil {
		t.Fatalf("set it again: %v", err)
	}
	f, err = readAgentsFile()
	if err != nil {
		t.Fatalf("read it back: %v", err)
	}
	if len(f.Projects) != 1 {
		t.Errorf("projects = %+v, want one entry", f.Projects)
	}
	if got, _ := f.projectDefault(root); got.Model != "opus" {
		t.Errorf("model = %q, want opus", got.Model)
	}
}

// A damaged file is a line in the picker and nothing more. Refusing to offer
// any agent at all because a preferences file has a comma in the wrong place
// would leave somebody unable to start a pane.
func TestADamagedFileStillOffersTheBuiltIns(t *testing.T) {
	writeAgents(t, "{ not json")

	got := buildCatalog(filepath.Join("C:", "code", "api"))
	if got.Err == "" {
		t.Error("nothing was said about the damaged file")
	}
	if len(got.Items) == 0 {
		t.Fatal("no agents were offered at all")
	}
	if got.Items[0].ID != agentIDClaude {
		t.Errorf("first agent = %q, want the built-in %q", got.Items[0].ID, agentIDClaude)
	}
	if got.Default.Agent != agentIDClaude {
		t.Errorf("default agent = %q, want %q", got.Default.Agent, agentIDClaude)
	}
}

// An agent hidden by the user is kept out of the picker rather than removed
// from the catalog, and a project's own default reaches the window alongside
// the overall one so the picker can mark the right row.
func TestTheCatalogTheWindowIsSent(t *testing.T) {
	root := filepath.Join("C:", "code", "api")
	writeAgents(t, `{
  "version": 1,
  "defaults": { "agent": "claude", "model": "haiku" },
  "projects": { `+jsonString(root)+`: { "agent": "claude", "model": "opus" } },
  "agents": [
    { "id": "claude", "hidden": true },
    { "id": "local", "name": "Local llama", "runner": "api",
      "api": { "baseURL": "http://127.0.0.1:11434/v1" },
      "models": [{ "id": "qwen3-coder" }] }
  ]
}`)

	got := buildCatalog(root)
	if got.Err != "" {
		t.Fatalf("the file was rejected: %s", got.Err)
	}
	for _, a := range got.Items {
		if a.ID == agentIDClaude {
			t.Error("a hidden agent was still offered")
		}
	}
	if len(got.Items) != 1 || got.Items[0].ID != "local" {
		t.Fatalf("items = %+v, want only the user's own agent", got.Items)
	}
	if !got.Items[0].Available {
		t.Error("a model server on loopback should be offered, since it asks for no key")
	}
	if got.Default.Model != "haiku" {
		t.Errorf("overall default model = %q, want haiku", got.Default.Model)
	}
	if got.Project == nil || got.Project.Model != "opus" {
		t.Errorf("project default = %+v, want opus", got.Project)
	}
}

// jsonString writes a path as a JSON string, so a Windows separator reaches
// the file escaped rather than as the start of an escape sequence.
func jsonString(s string) string {
	out, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(out)
}

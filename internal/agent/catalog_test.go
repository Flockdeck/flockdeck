package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestBuiltinsAreWellFormed is the guard on the table itself: every entry has
// to be startable, whatever else it does or does not claim.
func TestBuiltinsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, spec := range normalizeAll(Builtins()) {
		t.Run(spec.ID, func(t *testing.T) {
			if spec.ID == "" || spec.Name == "" {
				t.Fatalf("an agent needs an id and a name, got %+v", spec)
			}
			if seen[spec.ID] {
				t.Fatalf("two built-ins share the id %q, so one of them can never be picked", spec.ID)
			}
			seen[spec.ID] = true

			argv := BuildArgv(spec, false, Tokens{
				Session: "11111111-2222-3333-4444-555555555555",
				Model:   "some-model",
				Prompt:  "do the thing",
				Cwd:     "/work",
				Pane:    "pane-1",
			})
			switch spec.Runner {
			case RunnerCLI:
				if spec.Exe == "" {
					t.Fatal("a command-line agent needs a program to run")
				}
				if len(argv) == 0 || argv[0] != spec.Exe {
					t.Fatalf("argv should start with the program, got %q", argv)
				}
				if !slices.Contains(argv, "do the thing") {
					t.Errorf("the opening task never reaches the agent: %q", argv)
				}
			case RunnerAPI:
				if spec.API.Wire == "" {
					t.Fatal("an API agent needs a wire format")
				}
				if len(argv) < 2 || argv[0] != "--agent" || argv[1] != spec.ID {
					t.Fatalf("an API agent must tell `flockdeck chat` which entry to read, got %q", argv)
				}
				if !spec.Caps.Hooks || !spec.Caps.Resume || !spec.Caps.Transcript {
					t.Errorf("`flockdeck chat` supports all three, so its agents should say so: %+v", spec.Caps)
				}
			default:
				t.Fatalf("unknown runner %q", spec.Runner)
			}

			// A resume list is a promise that the agent can reattach a
			// conversation, and one written without the capability would have
			// Flockdeck build an argv it never uses -- or, far worse, encourage
			// somebody to turn the capability on for flags nobody checked.
			if len(spec.ResumeArgs) > 0 && !spec.Caps.Resume {
				t.Errorf("%q has resume arguments but does not claim Caps.Resume", spec.ID)
			}
			if spec.Caps.Resume && len(spec.ResumeArgs) == 0 {
				t.Errorf("%q claims Caps.Resume with nothing to resume with", spec.ID)
			}
		})
	}
}

// TestUnverifiedAgentsClaimNothing holds the line the design draws: an entry
// written without the tool to hand says how to start the program and nothing
// else. If somebody with one of these installed checks its flags and fills its
// capabilities in, this test is what they update to say so.
func TestUnverifiedAgentsClaimNothing(t *testing.T) {
	unverified := []string{"codex", "gemini", "aider", "opencode", "cursor-agent"}
	catalog := &Catalog{Specs: normalizeAll(Builtins())}
	for _, id := range unverified {
		t.Run(id, func(t *testing.T) {
			spec, ok := catalog.Find(id)
			if !ok {
				t.Fatalf("%q is missing from the catalog", id)
			}
			if spec.Caps != (Caps{}) {
				t.Errorf("%q claims %+v; verify it against `%s --help` before claiming anything", id, spec.Caps, spec.Exe)
			}
			if len(spec.Patterns.Waiting) > 0 || len(spec.Patterns.Idle) > 0 {
				t.Errorf("%q recognises output nobody has watched it print", id)
			}
			if spec.Install == "" {
				t.Errorf("%q should tell somebody who lacks it how to get it", id)
			}
		})
	}
}

// TestClaudeIsUntouchedByDefault: with no agents.json, the agent every existing
// layout names has to come back exactly as it was.
func TestClaudeIsUntouchedByDefault(t *testing.T) {
	c := Merge(nil)
	claude, ok := c.Find(DefaultAgentID)
	if !ok {
		t.Fatal("the default agent is missing from the catalog")
	}
	want := claudeSpec()
	if claude.Exe != want.Exe || claude.Caps != want.Caps {
		t.Errorf("claude = %+v, want %+v", claude, want)
	}
	spec, model, ok := c.Resolve("", "", "")
	if !ok || spec.ID != DefaultAgentID || model != "" {
		t.Errorf("a pane that asks for nothing should get claude with no model, got %q/%q ok=%v", spec.ID, model, ok)
	}
}

// TestMerge covers the rules of section 4: a known id is merged field by field,
// an unknown one is a new agent, and hidden takes one out of the picker.
func TestMerge(t *testing.T) {
	tests := []struct {
		name  string
		file  string
		check func(t *testing.T, c *Catalog)
	}{
		{
			name: "a set field wins and an absent one is kept",
			file: `{"version":1,"agents":[{"id":"claude","defaultModel":"sonnet"}]}`,
			check: func(t *testing.T, c *Catalog) {
				claude, _ := c.Find("claude")
				if claude.DefaultModel != "sonnet" {
					t.Errorf("defaultModel = %q, want sonnet", claude.DefaultModel)
				}
				if claude.Exe != "claude" || !claude.Caps.Hooks || len(claude.Args) == 0 {
					t.Errorf("the rest of the built-in should be untouched, got %+v", claude)
				}
			},
		},
		{
			name: "a nested object keeps the fields it does not mention",
			file: `{"agents":[{"id":"claude","caps":{"context":"prompt"}}]}`,
			check: func(t *testing.T, c *Catalog) {
				claude, _ := c.Find("claude")
				if claude.Caps.Context != ContextPrompt {
					t.Errorf("context = %q, want prompt", claude.Caps.Context)
				}
				if !claude.Caps.Hooks || !claude.Caps.Resume {
					t.Errorf("the other capabilities should survive, got %+v", claude.Caps)
				}
			},
		},
		{
			name: "an unknown id is a new agent, from the design's own example",
			file: `{"version":1,"agents":[{"id":"local","name":"Local llama","runner":"api",
				"api":{"wire":"openai","baseURL":"http://127.0.0.1:11434/v1"},
				"models":[{"id":"qwen3-coder"}]}]}`,
			check: func(t *testing.T, c *Catalog) {
				local, ok := c.Find("local")
				if !ok {
					t.Fatal("the new agent is missing")
				}
				if local.Runner != RunnerAPI || local.API.BaseURL != "http://127.0.0.1:11434/v1" {
					t.Fatalf("local = %+v", local)
				}
				// It named no arguments, so it gets the chat client's, which
				// is the only way `flockdeck chat` learns whose endpoint to use.
				argv := BuildArgv(local, false, Tokens{Session: "s"})
				if len(argv) < 2 || argv[0] != "--agent" || argv[1] != "local" {
					t.Errorf("argv = %q", argv)
				}
				if !local.Caps.Hooks || !local.Caps.Transcript {
					t.Errorf("an API entry runs `flockdeck chat`, so it has its capabilities: %+v", local.Caps)
				}
				if _, ok := c.Find("claude"); !ok {
					t.Error("adding an agent must not remove the built-ins")
				}
			},
		},
		{
			name: "hidden takes a built-in out of the picker without deleting it",
			file: `{"agents":[{"id":"aider","hidden":true}]}`,
			check: func(t *testing.T, c *Catalog) {
				if _, ok := c.Find("aider"); !ok {
					t.Error("a hidden agent is still in the catalog")
				}
				for _, s := range c.Visible() {
					if s.ID == "aider" {
						t.Error("a hidden agent must not be offered")
					}
				}
			},
		},
		{
			name: "an entry with no id is a notice, and the rest still merge",
			file: `{"agents":[{"name":"nameless"},{"id":"codex","name":"My Codex"}]}`,
			check: func(t *testing.T, c *Catalog) {
				if c.Notice == "" {
					t.Error("the interface should be told about the unusable entry")
				}
				codex, _ := c.Find("codex")
				if codex.Name != "My Codex" {
					t.Errorf("the usable entry should still have merged, got %q", codex.Name)
				}
			},
		},
		{
			name: "an entry of the wrong shape costs that entry alone",
			file: `{"agents":[{"id":"claude","caps":42},{"id":"codex","name":"My Codex"}]}`,
			check: func(t *testing.T, c *Catalog) {
				if !strings.Contains(c.Notice, "claude") {
					t.Errorf("notice = %q, want it to name the entry at fault", c.Notice)
				}
				claude, _ := c.Find("claude")
				if !claude.Caps.Hooks {
					t.Error("the built-in it was written over should be untouched")
				}
				codex, _ := c.Find("codex")
				if codex.Name != "My Codex" {
					t.Error("every other entry should still merge")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f File
			if err := json.Unmarshal([]byte(tt.file), &f); err != nil {
				t.Fatalf("the test's own file does not parse: %v", err)
			}
			tt.check(t, Merge(&f))
		})
	}
}

// TestNormalize covers what a hand-written entry is allowed to leave out.
func TestNormalize(t *testing.T) {
	tests := []struct {
		name  string
		entry string
		check func(t *testing.T, s Spec)
	}{
		{
			name:  "an entry with only an id is the command of that name",
			entry: `{"id":"amp"}`,
			check: func(t *testing.T, s Spec) {
				if s.Runner != RunnerCLI || s.Exe != "amp" || s.Name != "amp" {
					t.Errorf("got %+v", s)
				}
			},
		},
		{
			name:  "an endpoint with no runner named is an API agent",
			entry: `{"id":"gw","api":{"baseURL":"https://gw.example/v1"}}`,
			check: func(t *testing.T, s Spec) {
				if s.Runner != RunnerAPI {
					t.Errorf("runner = %q, want api", s.Runner)
				}
				if s.API.Wire != "openai" {
					t.Errorf("wire = %q, want the format nearly every gateway speaks", s.API.Wire)
				}
			},
		},
		{
			name:  "an API entry that writes its own arguments keeps them",
			entry: `{"id":"gw","runner":"api","args":[{"value":"--agent"},{"value":"other"}]}`,
			check: func(t *testing.T, s Spec) {
				argv := BuildArgv(s, false, Tokens{})
				if !slices.Equal(argv, []string{"--agent", "other"}) {
					t.Errorf("argv = %q", argv)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f File
			if err := json.Unmarshal([]byte(`{"agents":[`+tt.entry+`]}`), &f); err != nil {
				t.Fatalf("parse: %v", err)
			}
			c := Merge(&f)
			s := c.Specs[len(c.Specs)-1]
			tt.check(t, s)
		})
	}
}

// TestResolve covers what a new pane starts with when it, the project or
// nobody has chosen.
func TestResolve(t *testing.T) {
	const file = `{
		"version": 1,
		"defaults": {"agent": "claude", "model": "sonnet"},
		"projects": {"/work/api": {"agent": "codex", "model": "gpt-5"}}
	}`
	var f File
	if err := json.Unmarshal([]byte(file), &f); err != nil {
		t.Fatalf("parse: %v", err)
	}
	c := Merge(&f)

	tests := []struct {
		name              string
		project, id, mdl  string
		wantAgent, wantMd string
	}{
		{
			name:      "nothing chosen falls to the installation's defaults",
			wantAgent: "claude", wantMd: "sonnet",
		},
		{
			name:    "a project with its own answer overrides both",
			project: "/work/api", wantAgent: "codex", wantMd: "gpt-5",
		},
		{
			name:    "however the project path is spelled",
			project: "/work/api/", wantAgent: "codex", wantMd: "gpt-5",
		},
		{
			name:    "another project keeps the installation's defaults",
			project: "/work/web", wantAgent: "claude", wantMd: "sonnet",
		},
		{
			name:    "the pane's own choice wins",
			project: "/work/api", id: "gemini", mdl: "gemini-2.5-flash",
			wantAgent: "gemini", wantMd: "gemini-2.5-flash",
		},
		{
			// The default model belongs to the default agent, so choosing
			// another agent must not hand it Claude's model id.
			name: "choosing an agent with no model falls to that agent's own",
			id:   "anthropic", wantAgent: "anthropic", wantMd: "claude-sonnet-5",
		},
		{
			name: "an agent that is no longer in the catalog opens as the default",
			id:   "deleted-agent", wantAgent: "claude", wantMd: "sonnet",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, model, ok := c.Resolve(tt.project, tt.id, tt.mdl)
			if !ok {
				t.Fatal("a pane should always get an agent")
			}
			if spec.ID != tt.wantAgent || model != tt.wantMd {
				t.Errorf("got %q/%q, want %q/%q", spec.ID, model, tt.wantAgent, tt.wantMd)
			}
		})
	}
}

// TestLoadFrom covers the three states the user's file can be in when the
// picker opens: absent, sound, and mistyped.
func TestLoadFrom(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		c := LoadFrom(t.TempDir())
		if c.Notice != "" {
			t.Errorf("a machine with no agents.json is the ordinary case, got notice %q", c.Notice)
		}
		if _, ok := c.Find("claude"); !ok {
			t.Error("the built-ins should be there")
		}
	})

	t.Run("sound", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, `{"version":1,"defaults":{"agent":"codex"},"agents":[{"id":"codex","name":"Codex CLI"}]}`)
		c := LoadFrom(dir)
		if c.Notice != "" {
			t.Errorf("notice = %q", c.Notice)
		}
		codex, _ := c.Find("codex")
		if codex.Name != "Codex CLI" {
			t.Errorf("name = %q", codex.Name)
		}
		if got := c.DefaultsFor(""); got.Agent != "codex" {
			t.Errorf("default agent = %q", got.Agent)
		}
	})

	t.Run("mistyped", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, `{"version":1, "agents": [ }`)
		c := LoadFrom(dir)
		if c.Notice == "" {
			t.Error("a file that cannot be parsed should be shown to the user")
		}
		if _, ok := c.Find("claude"); !ok {
			t.Error("and the built-ins must carry on regardless")
		}
	})
}

func write(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ConfigName), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", ConfigName, err)
	}
}

// TestStripEnvIsTheUnion: a pane is a clean top-level session whatever is in
// it, so the markers Claude leaves behind are taken out of a Codex pane too.
func TestStripEnvIsTheUnion(t *testing.T) {
	var f File
	const file = `{"agents":[
		{"id":"codex","stripEnv":["CODEX_SESSION","CLAUDECODE"]},
		{"id":"gemini","stripEnv":["CODEX_SESSION"]}
	]}`
	if err := json.Unmarshal([]byte(file), &f); err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := Merge(&f).StripEnv()

	want := map[string]bool{"CLAUDECODE": true, "CODEX_SESSION": true, "CLAUDE_CODE_SESSION_ID": true}
	for name := range want {
		if !slices.Contains(got, name) {
			t.Errorf("%q should be stripped from every pane, got %q", name, got)
		}
	}
	seen := map[string]bool{}
	for _, name := range got {
		if seen[name] {
			t.Errorf("%q appears twice in %q", name, got)
		}
		seen[name] = true
	}
}

// TestOverlaidListsDoNotInheritBuiltinElements is about the lists of structs
// in an entry written over a built-in. Decoding a JSON array reuses the
// slice's elements, so each new element used to keep every field of the
// built-in one it landed on that it did not set itself.
func TestOverlaidListsDoNotInheritBuiltinElements(t *testing.T) {
	c := Merge(&File{Agents: []json.RawMessage{
		json.RawMessage(`{"id": "claude", "models": [{"id": "opus"}], "args": [{"value": "--foo"}, {"value": "{{prompt}}"}]}`),
		json.RawMessage(`{"id": "codex", "models": []}`),
	}})
	claude, _ := c.Find("claude")
	if want := []Model{{ID: "opus"}}; !slices.Equal(claude.Models, want) {
		t.Errorf("models = %+v, want %+v", claude.Models, want)
	}
	if got, want := BuildArgv(claude, false, Tokens{Session: "s", Prompt: "go"}), []string{"claude", "--foo", "go"}; !slices.Equal(got, want) {
		t.Errorf("argv = %q, want %q", got, want)
	}
	// What the entry left alone is still the built-in's.
	if got := BuildArgv(claude, true, Tokens{Session: "s"}); !slices.Equal(got, []string{"claude", "--resume", "s"}) {
		t.Errorf("resume argv = %q, want the built-in's", got)
	}
	// An empty list is a deliberate one.
	if codex, _ := c.Find("codex"); len(codex.Models) != 0 {
		t.Errorf("an entry that empties the models should get none, got %+v", codex.Models)
	}
}

// TestAnEntryThatCannotBeUsedLeavesTheBuiltinAsItWas: the decoder fills a list
// by writing over the elements already there and carries on past a value of
// the wrong kind, so an entry dropped for one bad field had already written its
// lists into the built-in's own. Claude stopped stripping CLAUDECODE, and an
// API agent stopped looking for its key where the built-in said.
func TestAnEntryThatCannotBeUsedLeavesTheBuiltinAsItWas(t *testing.T) {
	c := Merge(&File{Agents: []json.RawMessage{
		json.RawMessage(`{"id": "claude", "stripEnv": ["FOO"], "env": ["A=1"], "patterns": {"idle": ["x"]}, "models": "oops"}`),
		json.RawMessage(`{"id": "anthropic", "api": {"keyEnv": ["MINE"]}, "caps": "oops"}`),
		json.RawMessage(`{"id": "google", "api": {"keyEnv": ["ONE", "TWO"]}, "hidden": "oops"}`),
	}})
	for _, want := range normalizeAll(Builtins()) {
		got, _ := c.Find(want.ID)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("built-in %q changed by an entry that was dropped:\n got %+v\nwant %+v", want.ID, got, want)
		}
	}
	for _, id := range []string{"claude", "anthropic", "google"} {
		if !strings.Contains(c.Notice, `agent "`+id+`"`) {
			t.Errorf("notice %q should name the dropped entry %q", c.Notice, id)
		}
	}
}

// TestDefaultNamingADeletedAgentFallsBack: every new pane asks for the default,
// so one naming an agent since deleted from agents.json stopped them all from
// starting.
func TestDefaultNamingADeletedAgentFallsBack(t *testing.T) {
	c := Merge(&File{
		Defaults: Defaults{Agent: "ollama", Model: "llama3"},
		Projects: map[string]Defaults{"/work/app": {Agent: "gone", Model: "x"}},
	})
	for _, project := range []string{"", "/work/app"} {
		if got := c.DefaultsFor(project); got != (Defaults{Agent: DefaultAgentID}) {
			t.Errorf("DefaultsFor(%q) = %+v, want Claude with no model", project, got)
		}
	}
}

// TestAProjectDefaultNamingNoAgentFallsToTheInstallations: a project entry
// misspelt "Codx" opened Claude in that project while every other project
// opened the agent the installation's default names.
func TestAProjectDefaultNamingNoAgentFallsToTheInstallations(t *testing.T) {
	c := Merge(&File{
		Defaults: Defaults{Agent: "codex", Model: "gpt-5"},
		Projects: map[string]Defaults{"/work/app": {Agent: "Codx", Model: "x"}},
	})
	if got := c.DefaultsFor("/work/app"); got != (Defaults{Agent: "codex", Model: "gpt-5"}) {
		t.Errorf("DefaultsFor = %+v, want the installation's codex/gpt-5", got)
	}
	if !strings.Contains(c.Notice, `"Codx" for /work/app`) {
		t.Errorf("notice %q should still name the misspelt default", c.Notice)
	}
}

// TestHidingClaudeWithNoDefaultOpensTheFirstAgentOffered: an agents.json that
// hides Claude and names no default went on opening Claude in every new pane,
// and the picker marked as the default an agent it did not list.
func TestHidingClaudeWithNoDefaultOpensTheFirstAgentOffered(t *testing.T) {
	c := Merge(&File{
		Defaults: Defaults{Model: "sonnet"},
		Agents:   []json.RawMessage{json.RawMessage(`{"id": "claude", "hidden": true}`)},
	})
	first := c.Visible()[0]
	if first.ID == DefaultAgentID {
		t.Fatalf("Claude is hidden and still offered first")
	}
	// Claude's model is not handed to the agent that stands in for it.
	if got := c.DefaultsFor(""); got != (Defaults{Agent: first.ID}) {
		t.Errorf("DefaultsFor = %+v, want %q with no model", got, first.ID)
	}
	for _, asked := range []string{"", "deleted-agent"} {
		spec, model, ok := c.Resolve("", asked, "")
		if !ok || spec.ID != first.ID || model != first.DefaultModel {
			t.Errorf("Resolve(%q) = %q/%q ok=%v, want %q/%q", asked, spec.ID, model, ok, first.ID, first.DefaultModel)
		}
	}
	// A pane that asks for Claude by name still gets it: hidden keeps an
	// agent out of the picker, not out of the catalog.
	if spec, _, _ := c.Resolve("", DefaultAgentID, ""); spec.ID != DefaultAgentID {
		t.Errorf("a pane asking for Claude got %q", spec.ID)
	}
	// A default that names Claude is a choice, and stands.
	chosen := Merge(&File{
		Defaults: Defaults{Agent: DefaultAgentID},
		Agents:   []json.RawMessage{json.RawMessage(`{"id": "claude", "hidden": true}`)},
	})
	if got := chosen.DefaultsFor(""); got.Agent != DefaultAgentID {
		t.Errorf("a default naming Claude became %q", got.Agent)
	}
}

// TestUnknownAgentFieldsAreNamed: a key in the wrong place used to do nothing
// and say nothing, which left an endpoint greyed out with no clue why.
func TestUnknownAgentFieldsAreNamed(t *testing.T) {
	c := Merge(&File{Agents: []json.RawMessage{
		json.RawMessage(`{"id": "openai-compatible", "baseURL": "http://gw/v1", "modles": []}`),
		json.RawMessage(`{"id": "claude", "defaultModel": "sonnet", "api": {"baseURL": "http://x"}}`),
	}})
	for _, want := range []string{`"baseURL" belongs inside "api"`, `"modles" is not something an agent has`} {
		if !strings.Contains(c.Notice, want) {
			t.Errorf("notice %q should say %s", c.Notice, want)
		}
	}
	if strings.Contains(c.Notice, "defaultModel") || strings.Contains(c.Notice, `"api" is`) {
		t.Errorf("a field an agent has was reported: %q", c.Notice)
	}
	// The entry is still used: a key this build does not know may be one a
	// later build wrote.
	if claude, _ := c.Find("claude"); claude.DefaultModel != "sonnet" {
		t.Errorf("the rest of the entry should still apply, got default model %q", claude.DefaultModel)
	}
}

// TestRunnerIsSettledIntoOneThereIs: "API" in capitals matched neither runner
// and started a pane whose command line began "--agent", with no program.
func TestRunnerIsSettledIntoOneThereIs(t *testing.T) {
	c := Merge(&File{Agents: []json.RawMessage{
		json.RawMessage(`{"id": "caps", "runner": "API", "api": {"baseURL": "http://localhost:1/v1"}}`),
		json.RawMessage(`{"id": "odd", "runner": "shell", "exe": "odd-agent"}`),
	}})
	if s, _ := c.Find("caps"); s.Runner != RunnerAPI {
		t.Errorf("runner = %q, want api", s.Runner)
	}
	odd, _ := c.Find("odd")
	if odd.Runner != RunnerCLI || BuildArgv(odd, false, Tokens{})[0] != "odd-agent" {
		t.Errorf("an unknown runner should be decided as a missing one is, got %q", odd.Runner)
	}
	if !strings.Contains(c.Notice, `runner "shell"`) || strings.Contains(c.Notice, `"caps"`) {
		t.Errorf("notice = %q, want only the unknown runner named", c.Notice)
	}
}

// TestADefaultNamingNoAgentIsReported: such a default opens Claude instead,
// and without a word that looked like the choice had not taken.
func TestADefaultNamingNoAgentIsReported(t *testing.T) {
	c := Merge(&File{
		Defaults: Defaults{Agent: "Codex"},
		Projects: map[string]Defaults{"/work/app": {Agent: "gemni"}, "/work/ok": {Agent: "codex"}},
	})
	for _, want := range []string{`"Codex"`, `"gemni" for /work/app`} {
		if !strings.Contains(c.Notice, want) {
			t.Errorf("notice %q should name %s", c.Notice, want)
		}
	}
	if strings.Contains(c.Notice, "/work/ok") {
		t.Errorf("a default naming a real agent was reported: %q", c.Notice)
	}
}

// TestUnknownTokensAreNamed: a token with no value takes its group with it,
// and a misspelt one never has a value, so `"if": "modle"` dropped the
// --model flag in silence.
func TestUnknownTokensAreNamed(t *testing.T) {
	c := Merge(&File{Agents: []json.RawMessage{
		json.RawMessage(`{"id": "x", "args": [{"if": "modle", "args": [{"value": "--model"}, {"value": "{{ modle }}"}]}, {"value": "{{prompt}}"}]}`),
	}})
	if !strings.Contains(c.Notice, `"modle" is not a token`) {
		t.Errorf("notice = %q, want the misspelt token named", c.Notice)
	}
	if strings.Count(c.Notice, "modle") != 1 {
		t.Errorf("notice = %q, want the token named once", c.Notice)
	}
	if strings.Contains(c.Notice, "prompt\" is not") {
		t.Errorf("a real token was reported: %q", c.Notice)
	}
	// The built-ins use only real tokens.
	if c := Merge(nil); c.Notice != "" {
		t.Errorf("built-ins alone gave notice %q", c.Notice)
	}
}

// TestAnAPIEntryWithModelsButNoDefaultTakesTheFirst: an API has no model it is
// already set to, and the help page's own example lists models without a
// default, so its panes were started asking for none.
func TestAnAPIEntryWithModelsButNoDefaultTakesTheFirst(t *testing.T) {
	c := Merge(&File{Agents: []json.RawMessage{
		json.RawMessage(`{"id": "local", "name": "Local llama", "runner": "api", "api": {"wire": "openai", "baseURL": "http://127.0.0.1:11434/v1"}, "models": [{"id": "qwen3-coder"}, {"id": "llama3"}]}`),
		json.RawMessage(`{"id": "mycli", "exe": "mycli", "models": [{"id": "big"}]}`),
	}})
	if s, _ := c.Find("local"); s.DefaultModel != "qwen3-coder" {
		t.Errorf("default model = %q, want the first listed", s.DefaultModel)
	}
	// A command-line agent's empty default means whatever it is set to.
	if s, _ := c.Find("mycli"); s.DefaultModel != "" {
		t.Errorf("a CLI agent's default model = %q, want it left to the CLI", s.DefaultModel)
	}
}

// TestAnAPIEntryListingTheEndpointsOwnChoiceFirstKeepsIt: a model of empty id
// is how an entry asks for whatever the endpoint defaults to.
func TestAnAPIEntryListingTheEndpointsOwnChoiceFirstKeepsIt(t *testing.T) {
	c := Merge(&File{Agents: []json.RawMessage{
		json.RawMessage(`{"id": "gw", "api": {"baseURL": "https://gw.example/v1"}, "models": [{"id": "", "name": "Default"}, {"id": "big"}]}`),
	}})
	if s, _ := c.Find("gw"); s.DefaultModel != "" {
		t.Errorf("default model = %q, want the endpoint's own choice kept", s.DefaultModel)
	}
}

// TestExeIsExpandedLikeAShellWould: a program path copied from a shell was
// looked up on PATH as written, so "~/bin/mycli" was never found.
func TestExeIsExpandedLikeAShellWould(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	t.Setenv("FLOCKDECK_TEST_TOOLS", filepath.Join("C:", "tools"))
	for exe, want := range map[string]string{
		"~/bin/mycli":                     home + "/bin/mycli",
		"%FLOCKDECK_TEST_TOOLS%/mycli":    filepath.Join("C:", "tools") + "/mycli",
		"$FLOCKDECK_TEST_TOOLS/mycli":     filepath.Join("C:", "tools") + "/mycli",
		"${FLOCKDECK_TEST_TOOLS}/mycli":   filepath.Join("C:", "tools") + "/mycli",
		"100%/mycli":                      "100%/mycli",
		"$FLOCKDECK_TEST_UNSET_VAR/mycli": "$FLOCKDECK_TEST_UNSET_VAR/mycli",
		"mycli":                           "mycli",
	} {
		if got := expandExe(exe); got != want {
			t.Errorf("expandExe(%q) = %q, want %q", exe, got, want)
		}
	}
	c := Merge(&File{Agents: []json.RawMessage{json.RawMessage(`{"id": "mine", "exe": "~/bin/mycli"}`)}})
	if s, _ := c.Find("mine"); s.Exe != home+"/bin/mycli" {
		t.Errorf("exe = %q, want it expanded", s.Exe)
	}
}

// TestEnvValuesAreExpanded: an entry's environment replaces the inherited
// variable as written, so "PATH=$HOME/bin:$PATH" left the pane with a PATH of
// those very characters.
func TestEnvValuesAreExpanded(t *testing.T) {
	t.Setenv("FLOCKDECK_TEST_BASE", "/opt/base")
	c := Merge(&File{Agents: []json.RawMessage{
		json.RawMessage(`{"id": "mine", "exe": "mine", "env": ["TOOLS=$FLOCKDECK_TEST_BASE/bin:%FLOCKDECK_TEST_BASE%/lib", "$KEEP=as written", "NOVAR=100%"]}`),
	}})
	s, _ := c.Find("mine")
	want := []string{"TOOLS=/opt/base/bin:/opt/base/lib", "$KEEP=as written", "NOVAR=100%"}
	if !slices.Equal(s.Env, want) {
		t.Errorf("env = %q, want %q", s.Env, want)
	}
}

// TestAnAPIAgentTellsTheChatClientItsEndpoint: the chat client reads nothing
// from the catalog, so an OpenAI, Gemini or local-model pane took its
// defaults -- the Anthropic wire, at Anthropic's address -- and sent its
// request and its stored key there.
func TestAnAPIAgentTellsTheChatClientItsEndpoint(t *testing.T) {
	c := Merge(&File{Agents: []json.RawMessage{
		json.RawMessage(`{"id": "local", "api": {"baseURL": "http://127.0.0.1:11434/v1"}, "models": [{"id": "qwen3-coder"}]}`),
		json.RawMessage(`{"id": "anthropic", "api": {"baseURL": "https://proxy.example/v1"}}`),
	}})
	has := func(argv []string, flag, value string) bool {
		for i := 0; i+1 < len(argv); i++ {
			if argv[i] == flag && argv[i+1] == value {
				return true
			}
		}
		return false
	}
	for _, tc := range []struct {
		id          string
		flag, value string
	}{
		{"openai", "--wire", "openai"},
		{"openai", "--key-env", "OPENAI_API_KEY"},
		{"google", "--wire", "gemini"},
		{"google", "--key-env", "GEMINI_API_KEY,GOOGLE_API_KEY"},
		{"local", "--wire", "openai"},
		{"local", "--base-url", "http://127.0.0.1:11434/v1"},
		{"anthropic", "--base-url", "https://proxy.example/v1"},
	} {
		s, ok := c.Find(tc.id)
		if !ok {
			t.Fatalf("%s: not in the catalog", tc.id)
		}
		for _, resume := range []bool{false, true} {
			argv := BuildArgv(s, resume, Tokens{Session: "s", Model: "m", Prompt: "hi"})
			if !has(argv, tc.flag, tc.value) {
				t.Errorf("%s (resume %v): argv %q lacks %s %s", tc.id, resume, argv, tc.flag, tc.value)
			}
		}
	}
}

// TestBaseURLVariablesAreExpanded: a local model's address kept in a variable
// went to the chat client, and to the check for whether it needs a key, as
// the characters "$OLLAMA_HOST".
func TestBaseURLVariablesAreExpanded(t *testing.T) {
	t.Setenv("FLOCKDECK_TEST_OLLAMA", "127.0.0.1:11434")
	c := Merge(&File{Agents: []json.RawMessage{
		json.RawMessage(`{"id": "local", "api": {"baseURL": "http://$FLOCKDECK_TEST_OLLAMA/v1"}}`),
	}})
	s, _ := c.Find("local")
	if s.API.BaseURL != "http://127.0.0.1:11434/v1" {
		t.Errorf("baseURL = %q, want the variable read", s.API.BaseURL)
	}
	if !NeedsNoKey(s) {
		t.Error("an endpoint on this machine needs no key, however its address was written")
	}
	if argv := BuildArgv(s, false, Tokens{}); !slices.Contains(argv, "http://127.0.0.1:11434/v1") {
		t.Errorf("argv %q does not carry the address", argv)
	}
}

// TestAnIdDifferingOnlyInCaseIsPointedOut: "Claude" was taken, in silence, as
// a new agent beside the built-in it was surely meant to change.
func TestAnIdDifferingOnlyInCaseIsPointedOut(t *testing.T) {
	c := Merge(&File{Agents: []json.RawMessage{
		json.RawMessage(`{"id": "Claude", "defaultModel": "opus"}`),
		json.RawMessage(`{"id": "mine", "exe": "mine"}`),
	}})
	if !strings.Contains(c.Notice, `"Claude" is a new agent, not "claude"`) {
		t.Errorf("notice = %q, want the built-in it probably meant named", c.Notice)
	}
	if strings.Contains(c.Notice, `"mine"`) {
		t.Errorf("an agent of its own was reported: %q", c.Notice)
	}
	if s, _ := c.Find("claude"); s.DefaultModel != "" {
		t.Error("ids are still matched exactly")
	}
}

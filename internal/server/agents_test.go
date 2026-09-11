package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
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
	path := filepath.Join(dir, "flockdeck")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("make the state directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, agent.ConfigName), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", agent.ConfigName, err)
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

	c := agent.Load()
	if _, ok := c.Find("local"); !ok {
		t.Error("the hand-written agent did not survive")
	}
	if got := c.DefaultsFor(root); got.Agent != "claude" || got.Model != "sonnet" {
		t.Errorf("project default = %+v, want claude on sonnet", got)
	}

	// Setting it again replaces the entry rather than adding a second one, so
	// the file cannot end up holding two answers for one project.
	if err := setAgentDefault(root, agentChoice{Agent: "claude", Model: "opus"}); err != nil {
		t.Fatalf("set it again: %v", err)
	}
	c = agent.Load()
	if len(c.Projects) != 1 {
		t.Errorf("projects = %+v, want one entry", c.Projects)
	}
	if got := c.DefaultsFor(root); got.Model != "opus" {
		t.Errorf("model = %q, want opus", got.Model)
	}
}

// A damaged file is a line in the picker and nothing more. Refusing to offer
// any agent at all because a preferences file has a comma in the wrong place
// would leave somebody unable to start a pane.
func TestADamagedFileStillOffersTheBuiltIns(t *testing.T) {
	writeAgents(t, "{ not json")

	got := buildCatalog(agent.Load(), filepath.Join("C:", "code", "api"))
	if got.Err == "" {
		t.Error("nothing was said about the damaged file")
	}
	if len(got.Items) == 0 {
		t.Fatal("no agents were offered at all")
	}
	if got.Items[0].ID != "claude" {
		t.Errorf("first agent = %q, want the built-in %q", got.Items[0].ID, "claude")
	}
	if got.Default.Agent != "claude" {
		t.Errorf("default agent = %q, want %q", got.Default.Agent, "claude")
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

	got := buildCatalog(agent.Load(), root)
	if got.Err != "" {
		t.Fatalf("the file was rejected: %s", got.Err)
	}
	var local *catalogAgent
	for i, a := range got.Items {
		if a.ID == "claude" {
			t.Error("a hidden agent was still offered")
		}
		if a.ID == "local" {
			local = &got.Items[i]
		}
	}
	if local == nil {
		t.Fatalf("the user's own agent was not offered: %+v", got.Items)
	}
	if local.Name != "Local llama" {
		t.Errorf("name = %q, want the one the file gave it", local.Name)
	}
	if !local.Available {
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

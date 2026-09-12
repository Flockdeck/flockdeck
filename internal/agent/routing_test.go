package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ConfigName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readBack(t *testing.T, dir string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ConfigName))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("%v:\n%s", err, data)
	}
	return m
}

// dig follows keys down a decoded file.
func dig(m any, keys ...string) any {
	for _, k := range keys {
		obj, ok := m.(map[string]any)
		if !ok {
			return nil
		}
		m = obj[k]
	}
	return m
}

// A routing policy, and every key in it this build does not know, survive
// every write Flockdeck makes to agents.json -- above all a default saved from
// the picker over the project's entry, which used to be written back as its
// agent and model alone.
func TestARoutingPolicySurvivesEveryWrite(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "pay")
	entry, _ := json.Marshal(project)
	writeFile(t, dir, `{
  "version": 1,
  "routing": {"mode": "suggest", "floor": "mid", "classifier": {"enabled": false},
              "rules": [{"name": "tests", "tier": "small", "when": {"task": "tests"}}]},
  "projects": {`+string(entry)+`: {"agent": "claude", "model": "opus", "routing": {"mode": "auto", "later": 1}}}
}`)
	if err := SetDefaults(dir, project, Defaults{Agent: "codex"}); err != nil {
		t.Fatal(err)
	}
	// The same project spelled another way replaces the entry, and still
	// keeps its policy.
	if err := SetDefaults(dir, project+string(filepath.Separator), Defaults{Agent: "gemini"}); err != nil {
		t.Fatal(err)
	}
	if err := SetDefaults(dir, "", Defaults{Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	if err := SetBaseURL(dir, OpenAICompatibleID, "http://127.0.0.1:11434/v1"); err != nil {
		t.Fatal(err)
	}
	m := readBack(t, dir)
	if dig(m, "routing", "floor") != "mid" || dig(m, "routing", "classifier", "enabled") != false ||
		dig(m, "routing", "rules") == nil {
		t.Errorf("the policy for every project changed: %v", m["routing"])
	}
	projects, _ := m["projects"].(map[string]any)
	if len(projects) != 1 {
		t.Fatalf("projects = %v, want the one", projects)
	}
	for _, p := range projects {
		if dig(p, "agent") != "gemini" || dig(p, "routing", "mode") != "auto" || dig(p, "routing", "later") != 1.0 {
			t.Errorf("the project's entry is %v, want gemini and its own policy, all of it", p)
		}
	}
	c := LoadFrom(dir)
	if p, own := c.RoutingFor(project); !own || p.Mode != RoutingAuto {
		t.Errorf("the project is routed by %+v (own %v), want its own policy", p, own)
	}
	if p, own := c.RoutingFor(filepath.Join(dir, "other")); own || p.Mode != RoutingSuggest || p.Floor != TierMid {
		t.Errorf("another project is routed by %+v (own %v), want every project's", p, own)
	}
}

// A file written before routing loads as it did, and routes nothing.
func TestAFileFromBeforeRoutingRoutesNothing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, `{"version": 1, "defaults": {"agent": "claude", "model": "sonnet"},
  "projects": {"/work/api": {"agent": "codex", "model": "gpt-5"}}}`)
	c := LoadFrom(dir)
	if c.Notice != "" {
		t.Errorf("notice = %q", c.Notice)
	}
	if p, own := c.RoutingFor("/work/api"); own || p.Mode != "" || p.Rules != nil {
		t.Errorf("routed by %+v, own %v; want nothing said, which is off", p, own)
	}
	if err := SetDefaults(dir, "/work/api", Defaults{Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mustRead(t, dir)), "routing") {
		t.Errorf("saving a default wrote a policy nobody asked for:\n%s", mustRead(t, dir))
	}
}

func mustRead(t *testing.T, dir string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ConfigName))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Mistakes in a policy are named in the notice, as every mistake in the file
// is, and cost only the part that is wrong.
func TestRoutingMistakesAreNamedAndSkipped(t *testing.T) {
	c := Merge(&File{Routing: json.RawMessage(`{"mode": "sometimes", "floor": "medium", "rules": [
		{"name": "broken", "tier": "small", "when": {"task": "(unclosed"}},
		{"name": "large", "tier": "large"},
		{"name": "nothing", "when": {"task": "x"}},
		{"name": "whose", "model": "opus"},
		{"name": "both", "tier": "small", "model": "opus", "agent": "claude"},
		{"name": "glob", "tier": "top", "when": {"files": ["[oops"]}},
		{"name": "kind", "tier": "top", "when": {"kind": "chat"}},
		{"tier": "Small", "when": {"task": "tests"}}
	]}`)})
	for _, want := range []string{`"sometimes"`, `"medium"`, `"broken"`, `"large"`, `"nothing"`, `"whose"`, `"both"`, `"glob"`, `"kind"`} {
		if !strings.Contains(c.Notice, want) {
			t.Errorf("notice does not mention %s: %s", want, c.Notice)
		}
	}
	p := c.Routing
	if p.Mode != RoutingOff || p.Floor != TierTop {
		t.Errorf("mode %q, floor %q; want off, and the cautious top", p.Mode, p.Floor)
	}
	if len(p.Rules) != 1 || p.Rules[0].Tier != TierSmall || p.Rules[0].Name != "rule 8" {
		t.Errorf("rules = %+v, want the one good one, named for its place and its tier's case forgiven", p.Rules)
	}
}

func TestAPolicyThatIsNotAnObjectIsOff(t *testing.T) {
	c := Merge(&File{Routing: json.RawMessage(`"suggest"`)})
	if c.Routing.Mode != RoutingOff || !strings.Contains(c.Notice, "routing") {
		t.Errorf("routed by %+v with notice %q", c.Routing, c.Notice)
	}
}

func TestSetRouting(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "app")
	writeFile(t, dir, `{"version": 1, "routing": {"floor": "mid", "rules": [], "later": true}}`)

	if err := SetRouting(dir, "", "mode", "Suggest"); err != nil {
		t.Fatal(err)
	}
	m := readBack(t, dir)
	if dig(m, "routing", "mode") != "suggest" || dig(m, "routing", "later") != true || dig(m, "routing", "floor") != "mid" {
		t.Errorf("routing = %v, want the mode changed and nothing else", m["routing"])
	}

	// A project given a policy of its own starts from every project's, so
	// turning it on there does not drop the floor and the rules.
	if err := SetRouting(dir, project, "mode", "auto"); err != nil {
		t.Fatal(err)
	}
	p, own := LoadFrom(dir).RoutingFor(project)
	if !own || p.Mode != RoutingAuto || p.Floor != TierMid || p.Rules == nil || len(p.Rules) != 0 {
		t.Errorf("the project is routed by %+v (own %v), want every project's with its own mode", p, own)
	}
	if err := SetRouting(dir, project, "floor", ""); err != nil {
		t.Fatal(err)
	}
	if p, _ := LoadFrom(dir).RoutingFor(project); p.Floor != "" {
		t.Errorf("floor = %q after it was taken away", p.Floor)
	}

	// "The same as every project" takes the project's own away, and with it
	// an entry that held nothing else.
	if err := SetRouting(dir, project, "mode", ""); err != nil {
		t.Fatal(err)
	}
	if _, own := LoadFrom(dir).RoutingFor(project); own {
		t.Error("the project still has a policy of its own")
	}
	if dig(readBack(t, dir), "projects") != nil {
		t.Errorf("an empty project entry was left behind:\n%s", mustRead(t, dir))
	}

	before := mustRead(t, dir)
	for _, bad := range [][3]string{{"", "mode", ""}, {"", "mode", "always"}, {"", "floor", "large"}, {"", "rules", "x"}} {
		if err := SetRouting(dir, bad[0], bad[1], bad[2]); err == nil {
			t.Errorf("SetRouting(%q, %q, %q) was accepted", bad[0], bad[1], bad[2])
		}
	}
	if string(mustRead(t, dir)) != string(before) {
		t.Error("a refused setting changed the file")
	}
}

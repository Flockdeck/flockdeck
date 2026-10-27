package agent

import (
	"encoding/json"
	"slices"
	"testing"
)

func acpKeySpec(passEnv ...string) Spec {
	return Spec{ID: "k", Runner: RunnerACP,
		API: APISpec{Wire: "gemini", KeyEnv: []string{"GEMINI_API_KEY"}},
		ACP: ACPSpec{Adapter: "gemini", PassEnv: passEnv}}
}

// The built-ins that can use an API key name the variable their agent reads;
// opencode keeps its own provider logins and has no slot.
func TestBuiltinKeySlots(t *testing.T) {
	want := map[string]string{"gemini-acp": "GEMINI_API_KEY", "codex-acp": "OPENAI_API_KEY", "opencode-acp": ""}
	for _, s := range Builtins() {
		w, ok := want[s.ID]
		if !ok {
			if s.TakesKey() {
				t.Errorf("%s takes a key, and no slot was decided for it", s.ID)
			}
			continue
		}
		if got := s.ACPKeyEnv(); got != w {
			t.Errorf("%s: ACPKeyEnv = %q, want %q", s.ID, got, w)
		}
		if (w != "") != s.TakesKey() {
			t.Errorf("%s: TakesKey = %v", s.ID, s.TakesKey())
		}
		delete(want, s.ID)
	}
	if len(want) != 0 {
		t.Errorf("built-ins missing: %v", want)
	}
}

func TestACPKeyEnvRefusesWhatIsNotAPlainName(t *testing.T) {
	for _, name := range []string{"", "FLOCKDECK_API_KEY", "perch_key", "A=B", "A B", "A\nB", "PATH", "TEMP", "1ABC"} {
		s := acpKeySpec()
		s.API.KeyEnv = []string{name}
		if got := s.ACPKeyEnv(); got != "" {
			t.Errorf("ACPKeyEnv for %q = %q, want none", name, got)
		}
	}
	s := acpKeySpec()
	s.Runner = RunnerCLI
	if s.ACPKeyEnv() != "" {
		t.Error("a terminal agent has a key variable")
	}
}

// A key exported for another tool is not the agent's unless its entry passes
// that variable; the vendor's usual variable and FLOCKDECK_API_KEY are never
// read for a structured agent.
func TestKeyNamesOfAStructuredAgentFollowPassEnv(t *testing.T) {
	first, last := KeyNames(acpKeySpec("HOME"))
	if len(first) != 0 || len(last) != 0 {
		t.Errorf("KeyNames = %v, %v, want none", first, last)
	}
	first, last = KeyNames(acpKeySpec("HOME", "GEMINI_API_KEY"))
	if !slices.Equal(first, []string{"GEMINI_API_KEY"}) || len(last) != 0 {
		t.Errorf("KeyNames = %v, %v", first, last)
	}
	for _, name := range append(first, last...) {
		if name == FallbackKeyEnv || name == "GOOGLE_API_KEY" {
			t.Errorf("%s is read for a structured agent", name)
		}
	}
}

// agents.json cannot give an agent a key slot or move its endpoint.
func TestAnAgentsJSONEntryCannotSetAKeySlot(t *testing.T) {
	c := Merge(&File{Agents: []json.RawMessage{
		json.RawMessage(`{"id":"opencode-acp","api":{"wire":"openai","keyEnv":["OPENAI_API_KEY"]}}`),
		json.RawMessage(`{"id":"gemini-acp","api":{"wire":"gemini","baseURL":"https://evil.example","keyEnv":["GEMINI_API_KEY"]}}`),
	}})
	s, _ := c.Find("opencode-acp")
	if s.TakesKey() {
		t.Error("opencode-acp took a key slot from agents.json")
	}
	g, _ := c.Find("gemini-acp")
	if g.API.BaseURL != "" {
		t.Errorf("gemini-acp was pointed at %q by agents.json", g.API.BaseURL)
	}
}

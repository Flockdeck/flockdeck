package creds

import (
	"errors"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
)

func structuredSpec(id, keyEnv string, passEnv ...string) agent.Spec {
	return agent.Spec{ID: id, Name: id, Runner: agent.RunnerACP,
		API: agent.APISpec{Wire: "gemini", KeyEnv: []string{keyEnv}},
		ACP: agent.ACPSpec{Adapter: "gemini", PassEnv: passEnv}}
}

// A stored key goes to the one variable of the agent it was stored for.
func TestStoredKeyIsForOneAgentAndOneVariable(t *testing.T) {
	isolateConfig(t)
	gem := structuredSpec("gem", "GEMINI_API_KEY")
	cod := structuredSpec("cod", "OPENAI_API_KEY")
	if err := Set("gem", "key-for-gem"); err != nil {
		t.Fatal(err)
	}
	if got := StoredKey(gem); got != "key-for-gem" {
		t.Errorf("StoredKey(gem) = %q", got)
	}
	if got := Env(gem); len(got) != 1 || got[0] != "GEMINI_API_KEY=key-for-gem" {
		t.Errorf("Env(gem) = %v", got)
	}
	// The other agent has a slot of its own and no key: it gets nothing, not gem's.
	if got := StoredKey(cod); got != "" {
		t.Errorf("StoredKey(cod) = %q, want none", got)
	}
	if got := Env(cod); got != nil {
		t.Errorf("Env(cod) = %v", got)
	}
	// An agent with no key variable gets nothing even where a key is stored
	// under its id.
	if err := Set("plain", "key-for-plain"); err != nil {
		t.Fatal(err)
	}
	plain := agent.Spec{ID: "plain", Runner: agent.RunnerACP, ACP: agent.ACPSpec{Adapter: "opencode"}}
	if got := StoredKey(plain); got != "" || Env(plain) != nil {
		t.Errorf("an agent with no key variable was given %q", got)
	}
}

// A variable exported in Flockdeck's environment is not turned into a launch
// entry unless the agent passes it, and the vendor's usual variable and
// FLOCKDECK_API_KEY are not read for a structured agent.
func TestStoredKeyIgnoresTheEnvironment(t *testing.T) {
	isolateConfig(t)
	t.Setenv("GEMINI_API_KEY", "exported")
	t.Setenv("GOOGLE_API_KEY", "exported-google")
	t.Setenv("FLOCKDECK_API_KEY", "shared")
	spec := structuredSpec("gem", "GEMINI_API_KEY")
	if k := Resolve(spec); k.Set() {
		t.Errorf("Resolve found %v for an agent that passes no key variable", k)
	}
	if err := Set("gem", "stored"); err != nil {
		t.Fatal(err)
	}
	if got := StoredKey(spec); got != "stored" {
		t.Errorf("StoredKey = %q, want the stored key", got)
	}
	if k := Resolve(spec); k.Source != SourceStore {
		t.Errorf("Resolve = %v, want the stored key", k)
	}
	// Passed, the exported variable is the key in use, and it reaches the agent
	// by itself: nothing is written for it.
	passing := structuredSpec("gem", "GEMINI_API_KEY", "GEMINI_API_KEY")
	if k := Resolve(passing); k.Source != SourceEnv || k.Env != "GEMINI_API_KEY" {
		t.Errorf("Resolve = %v", k)
	}
	if got := StoredKey(passing); got != "" {
		t.Errorf("StoredKey = %q for a key the agent inherits", got)
	}
}

func TestStoredKeyRefusesWhatCannotBeAnEnvironmentValue(t *testing.T) {
	isolateConfig(t)
	spec := structuredSpec("gem", "GEMINI_API_KEY")
	for name, key := range map[string]string{
		"a newline inside": "abc\ndef", "a NUL": "abc\x00def", "an escape": "abc\x1b[31m",
		"too long": strings.Repeat("k", maxKeyLen+1),
	} {
		// Written past Set, as a hand-edited keys.json would hold it.
		if err := save(map[string]string{"gem": key}); err != nil {
			t.Fatal(err)
		}
		if got := StoredKey(spec); got != "" {
			t.Errorf("a key with %s was handed to the agent", name)
		}
	}
}

// The row for a structured agent lists only the variables that reach it, and
// never carries the key.
func TestStatusOfAStructuredAgent(t *testing.T) {
	isolateConfig(t)
	spec := structuredSpec("gem", "GEMINI_API_KEY")
	st := StatusOf(spec)
	if st.Set || len(st.Vars) != 0 {
		t.Errorf("status = %+v", st)
	}
	if want := "not set: run `flockdeck keys set gem`"; st.Describe() != want {
		t.Errorf("Describe = %q, want %q", st.Describe(), want)
	}
	if err := Set("gem", "sk-very-secret"); err != nil {
		t.Fatal(err)
	}
	st = StatusOf(spec)
	if !st.Set || st.Source != SourceStore || strings.Contains(st.Describe(), "sk-very-secret") {
		t.Errorf("status = %+v, %q", st, st.Describe())
	}
	if got := StatusAll([]agent.Spec{spec, {ID: "plain", Runner: agent.RunnerACP}}); len(got) != 1 || got[0].Agent != "gem" {
		t.Errorf("StatusAll = %+v", got)
	}
}

// Set refuses a key the agent could not be given, with a fixed text that does
// not quote it, and stores nothing.
func TestSetRefusesAnUnusableKey(t *testing.T) {
	isolateConfig(t)
	for _, key := range []string{"abc\ndef", "abc\x00def", "abc\x1b[31m", strings.Repeat("k", maxKeyLen+1)} {
		err := Set("gem", key)
		if !errors.Is(err, ErrUnusable) || err.Error() != "That key can't be used" {
			t.Errorf("Set(%q...) = %v", key[:3], err)
		}
	}
	if names, _ := Names(); len(names) != 0 {
		t.Errorf("the store holds %v", names)
	}
	if !Usable("sk-fine") || Usable("") || Usable("a\tb") {
		t.Error("Usable is wrong")
	}
	if err := Set("gem", strings.Repeat("k", maxKeyLen)); err != nil {
		t.Errorf("a key at the limit was refused: %v", err)
	}
}

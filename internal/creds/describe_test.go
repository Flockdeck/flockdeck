package creds

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
)

// A key that is not set is described with where to put one, even for an agent
// that names no variable for it; and an agent talking to a model on this
// machine, which wants no key, is not described as missing one.
func TestAMissingKeySaysWhatToDoAndALocalOneIsNotMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	gateway := agent.Spec{ID: "gw", Runner: agent.RunnerAPI, API: agent.APISpec{Wire: "openai", BaseURL: "https://gw.example/v1"}}
	if got := StatusOf(gateway).Describe(); !strings.Contains(got, "flockdeck keys set gw") {
		t.Errorf("an agent with no variable is described as %q", got)
	}
	local := agent.Spec{ID: "local", Runner: agent.RunnerAPI, API: agent.APISpec{Wire: "openai", BaseURL: "http://127.0.0.1:11434/v1"}}
	if got := StatusOf(local).Describe(); !strings.Contains(got, "not needed") {
		t.Errorf("a local endpoint is described as %q", got)
	}
}

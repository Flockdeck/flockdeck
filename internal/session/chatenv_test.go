package session

import (
	"strings"
	"testing"
)

// The variables `flockdeck chat` reads its endpoint from, set in the
// environment Flockdeck was started from, are not handed on to every pane: a
// vendor's pane would otherwise send its requests, and its key, to whatever
// address they name. A catalog entry that sets one in its Spec.Env still gives
// its pane that value.
func TestAPaneDoesNotInheritTheChatsEndpoint(t *testing.T) {
	inherited := []string{
		"FLOCKDECK_BASE_URL=http://127.0.0.1:11434/v1", "FLOCKDECK_WIRE=openai", "FLOCKDECK_KEY_ENV=OLLAMA_KEY",
		"PERCH_BASE_URL=http://127.0.0.1:1234/v1", "PERCH_WIRE=gemini", "PERCH_KEY_ENV=OLD_KEY",
		"HOME=/home/me",
	}
	env := envFrom("windows", inherited, nil, []string{"FLOCKDECK_WIRE=anthropic"})

	got := map[string][]string{}
	for _, kv := range env {
		if name, v, ok := strings.Cut(kv, "="); ok {
			got[name] = append(got[name], v)
		}
	}
	for _, name := range []string{"FLOCKDECK_BASE_URL", "FLOCKDECK_KEY_ENV", "PERCH_BASE_URL", "PERCH_WIRE", "PERCH_KEY_ENV"} {
		if v, ok := got[name]; ok {
			t.Errorf("%s=%q was handed on to the pane", name, v)
		}
	}
	if v := got["FLOCKDECK_WIRE"]; len(v) != 1 || v[0] != "anthropic" {
		t.Errorf("FLOCKDECK_WIRE = %q, want the entry's own value once", v)
	}
	if v := got["HOME"]; len(v) != 1 || v[0] != "/home/me" {
		t.Errorf("HOME = %q, want it kept", v)
	}
}

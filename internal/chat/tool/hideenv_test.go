package tool

import (
	"strings"
	"testing"
)

// The key Flockdeck handed a pane is in the chat's environment for the chat to
// use; a command the model runs must not inherit it, or `env` puts it into the
// conversation.
func TestAHiddenVariableIsNotPassedToCommands(t *testing.T) {
	t.Setenv("FLOCKDECK_TEST_SECRET", "sk-do-not-show")
	set, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	set.HideEnv("FLOCKDECK_TEST_SECRET")
	tl, _ := set.Lookup("run_command")
	got, err := call(t, tl, map[string]any{"command": helperLine(t, "env", "FLOCKDECK_TEST_SECRET")})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "sk-do-not-show") {
		t.Errorf("the hidden variable reached the command:\n%s", got)
	}
	// Everything else still does: the helper itself needs its marker variable.
	if !strings.Contains(got, "FLOCKDECK_TEST_SECRET=") {
		t.Errorf("the command did not run as expected:\n%s", got)
	}
}

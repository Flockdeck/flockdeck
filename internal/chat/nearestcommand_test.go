package chat

import (
	"strings"
	"testing"
)

// A mistyped command is answered with the command meant, where one is close;
// anything further off still points at /help.
func TestAMistypedCommandIsAnsweredWithTheOneMeant(t *testing.T) {
	for name, want := range map[string]string{
		"modle":      "model",
		"stauts":     "status",
		"histroy":    "history",
		"hlp":        "help",
		"Retyr":      "retry",
		"":           "",
		"nonsense":   "",
		"frobnicate": "",
	} {
		if got := nearestCommand(name); got != want {
			t.Errorf("nearestCommand(%q) = %q, want %q", name, got, want)
		}
	}

	out := run(t, Options{Agent: "anthropic", Model: "claude-opus-5"}, "/modle\n/frobnicate\n/exit\n", &scriptedWire{})
	said := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{"no such command: /modle — did you mean /model?", "no such command: /frobnicate — try /help"} {
		if !strings.Contains(said, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

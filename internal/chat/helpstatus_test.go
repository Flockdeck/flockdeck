package chat

import (
	"strings"
	"testing"
)

// /help says what /status is for now: the endpoint and the key are the two
// settings to look at when a pane is failing, and /help is where somebody
// looks to find out where to see them.
func TestHelpSaysStatusShowsTheEndpointAndTheKey(t *testing.T) {
	out := run(t, Options{Agent: "anthropic", Model: "claude-opus-5"}, "/help\n/exit\n", &scriptedWire{})
	said := strings.Join(strings.Fields(out), " ")
	if !strings.Contains(said, "/status what has been spent, the endpoint, the key, the transcript") {
		t.Errorf("/help does not say what /status shows:\n%s", out)
	}
}

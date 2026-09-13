package chat

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

// A pane waiting for a key is waiting on the user, and says so to the
// workspace the way a tool's question does.
func TestAPaneWaitingForAKeyTurnsAmber(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("FLOCKDECK_API_KEY", "")
	savedStore, savedPoll := KeyStore, keyPoll
	t.Cleanup(func() { KeyStore, keyPoll = savedStore, savedPoll })
	keyPoll = 5 * time.Millisecond
	asked := 0
	KeyStore = func(string) string {
		if asked++; asked < 2 {
			return ""
		}
		return "sk-stored-meanwhile"
	}
	pane := newPaneServer(t, "")

	var out strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Run(ctx, Options{
		Agent: "anthropic", Wire: "anthropic", Model: "claude-opus-5", Session: "s", Dir: t.TempDir(),
		API: pane.srv.BaseURL(), Token: pane.srv.Token(),
		In: strings.NewReader("/exit\n"), Out: &out, Width: 90, waitForKey: true,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !pane.saw("Notification") {
		t.Errorf("the pane never said it was waiting; it reported %q", pane.names())
	}
	// Once the key turns up the pane is at its prompt, and says so before it
	// opens the chat: the SessionStart that follows leaves a status alone, so
	// without it the pane stayed amber until somebody typed a prompt.
	names := pane.names()
	waited, stopped, started := slices.Index(names, "Notification"), slices.Index(names, "Stop"), slices.Index(names, "SessionStart")
	if stopped < 0 || stopped < waited || (started >= 0 && stopped > started) {
		t.Errorf("the pane never said the wait was over before the chat opened; it reported %q", names)
	}
}

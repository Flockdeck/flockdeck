package chat

import (
	"errors"
	"strings"
	"testing"
)

// The help says that what the flags leave unsaid comes from the agent's
// catalog entry, which is how a pane is told its endpoint at all.
func TestChatHelpSaysTheEndpointComesFromTheCatalog(t *testing.T) {
	var out strings.Builder
	if _, err := ParseArgs([]string{"-h"}, &out); !errors.Is(err, ErrHelpShown) {
		t.Fatalf("ParseArgs(-h) = %v", err)
	}
	said := strings.Join(strings.Fields(out.String()), " ")
	for _, want := range []string{
		"request shape: anthropic, openai or gemini; empty takes the agent's catalog entry",
		"endpoint root; empty takes the agent's catalog entry, and then the vendor's own",
		"What -wire, -base-url and -key-env leave unsaid is taken from the -agent's catalog entry",
	} {
		if !strings.Contains(said, want) {
			t.Errorf("the help lacks %q:\n%s", want, out.String())
		}
	}
}

package chat

import (
	"strings"
	"testing"
)

// The help is where somebody looks for what the chat can do once it is
// running, and it has to mention the commands for them to be found.
func TestHelpMentionsTheCommands(t *testing.T) {
	var out strings.Builder
	if _, err := ParseArgs([]string{"-h"}, &out); err != ErrHelpShown {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"/help", "/model", "/history", "flockdeck keys set"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the help does not mention %s:\n%s", want, out.String())
		}
	}
}

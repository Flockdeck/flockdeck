package tool

import (
	"strings"
	"testing"
)

// A command's prompt can never be answered -- its input is empty and its output
// goes to the model -- so git is told there is no terminal to ask in, and fails
// at once rather than waiting out the timeout for a username.
func TestACommandIsToldThereIsNobodyToPrompt(t *testing.T) {
	tl := &runCommand{root: newRoot(t)}
	got, err := call(t, tl, map[string]any{"command": helperLine(t, "env", "GIT_TERMINAL_PROMPT")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "GIT_TERMINAL_PROMPT=0") {
		t.Errorf("the command was not told there is no terminal:\n%s", got)
	}
}

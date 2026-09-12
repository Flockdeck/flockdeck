package chat

import (
	"strings"
	"testing"
)

// Code in a replayed conversation is background like the rest of it, and is
// dimmed with it rather than drawn brighter than the answer being written now.
func TestCodeInDimmedTextIsDimmedToo(t *testing.T) {
	var out strings.Builder
	p := newPrinter(&out, 60, true)
	p.setDim(true)
	p.text("```go\nx := 1\n```\n")
	p.endMessage()
	if !strings.Contains(out.String(), ansiDim+ansiCyan+"x := 1") {
		t.Errorf("the code was not dimmed: %q", out.String())
	}
}

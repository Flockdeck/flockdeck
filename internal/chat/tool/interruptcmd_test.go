package tool

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Ctrl+C while a command runs kills it, and what it exits with then is the
// kill's doing: told "[exit status 1]", the model reads a failure of the
// command and sets about fixing it. It is told the user stopped it.
func TestAnInterruptedCommandSaysTheUserStoppedIt(t *testing.T) {
	tl := &runCommand{root: newRoot(t)}
	line := helperLine(t, "linger")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()
	out, err := tl.Run(ctx, rawArgs(t, map[string]any{"command": line}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[stopped: the user interrupted it]") || strings.Contains(out, "exit status") {
		t.Errorf("an interrupted command reported:\n%s", out)
	}
}

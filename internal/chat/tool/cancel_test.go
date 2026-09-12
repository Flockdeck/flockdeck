package tool

import (
	"context"
	"errors"
	"testing"
)

// A search of a large checkout is the longest read there is, and Ctrl+C must
// stop it rather than wait for the walk to finish.
func TestAnInterruptedSearchStops(t *testing.T) {
	root := newRoot(t)
	write(t, root, "a/b.go", "x\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []struct {
		tool Tool
		args map[string]any
	}{
		{&globTool{root: root}, map[string]any{"pattern": "**/*.go"}},
		{&grepTool{root: root}, map[string]any{"pattern": "x"}},
	} {
		out, err := c.tool.Run(ctx, rawArgs(t, c.args))
		if !errors.Is(err, context.Canceled) {
			t.Errorf("%s after an interrupt = %q, %v; want it to stop", c.tool.Name(), out, err)
		}
	}
}

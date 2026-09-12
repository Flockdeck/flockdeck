package tool

import (
	"strings"
	"testing"
)

// A malformed glob matches nothing, and reported as "no files match" it sends
// the model looking for files that are there. It is refused as malformed.
func TestAMalformedGlobIsSaidToBeOne(t *testing.T) {
	root := newRoot(t)
	write(t, root, "src/a.go", "package a\n")
	for _, c := range []struct {
		tool Tool
		args map[string]any
	}{
		{&globTool{root: root}, map[string]any{"pattern": "src/[a.go"}},
		{&grepTool{root: root}, map[string]any{"pattern": "package", "glob": "**/[x"}},
	} {
		_, err := c.tool.Run(t.Context(), rawArgs(t, c.args))
		if err == nil || !strings.Contains(err.Error(), "not a valid glob") {
			t.Errorf("%s: error = %v, want the pattern refused as malformed", c.tool.Name(), err)
		}
	}
	// A good pattern with ** still works.
	got, err := call(t, &globTool{root: root}, map[string]any{"pattern": "**/*.go"})
	if err != nil || !strings.Contains(got, "src/a.go") {
		t.Errorf("glob **/*.go = %q, %v", got, err)
	}
}

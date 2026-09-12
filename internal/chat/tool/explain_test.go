package tool

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
)

// A file the model asked for that is not there is said as the model named it,
// not as the absolute path the system reported -- which is long, the same for
// every file, and in each system's own words.
func TestAMissingFileIsNamedAsItWasAskedFor(t *testing.T) {
	root := newRoot(t)
	for _, c := range []struct {
		tool Tool
		args map[string]any
	}{
		{&readFile{root: root}, map[string]any{"path": "src/nope.go"}},
		{&listDir{root: root}, map[string]any{"path": "nowhere"}},
		{&editFile{root: root}, map[string]any{"path": "nope.txt", "old_string": "a", "new_string": "b"}},
	} {
		_, err := c.tool.Run(t.Context(), rawArgs(t, c.args))
		if err == nil {
			t.Fatalf("%s: no error for a missing path", c.tool.Name())
		}
		if strings.Contains(err.Error(), root.Dir()) {
			t.Errorf("%s: the error names the absolute path: %v", c.tool.Name(), err)
		}
		if !errors.Is(err, fs.ErrNotExist) || !strings.Contains(err.Error(), "does not exist") {
			t.Errorf("%s: error = %v, want one saying the path does not exist", c.tool.Name(), err)
		}
	}
}

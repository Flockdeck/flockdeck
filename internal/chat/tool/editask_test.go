package tool

import (
	"strings"
	"testing"
)

// An edit of several lines, joined into one with " / ", cannot be read closely
// enough to agree to. The question shows the lines as a diff does.
func TestEditFileShowsTheLinesItReplaces(t *testing.T) {
	root := newRoot(t)
	write(t, root, "a.go", "func a() {\n\treturn 1\n}\n")
	tl := &editFile{root: root}

	q := tl.Approval(rawArgs(t, map[string]any{
		"path": "a.go", "old_string": "\treturn 1\n}", "new_string": "\tx := 2\n\treturn x\n}",
	}))
	for _, want := range []string{"Edit a.go, replacing 1 occurrence?", "  - \treturn 1\n  - }", "  + \tx := 2\n  + \treturn x\n  + }"} {
		if !strings.Contains(q, want) {
			t.Errorf("the question lacks %q:\n%s", want, q)
		}
	}
	if strings.Contains(q, " / ") {
		t.Errorf("the lines were joined into one:\n%s", q)
	}

	q = tl.Approval(rawArgs(t, map[string]any{"path": "a.go", "old_string": "\treturn 1\n", "new_string": ""}))
	if !strings.Contains(q, "the text is removed") {
		t.Errorf("a deletion does not say so:\n%s", q)
	}
}

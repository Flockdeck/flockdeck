package tool

import (
	"strings"
	"testing"
)

// An edit that misses only on spacing -- a tab written as spaces -- says where
// the text nearly is, so the model reads those lines again instead of guessing.
func TestAnEditThatMissesOnSpacingSaysWhere(t *testing.T) {
	root := newRoot(t)
	write(t, root, "a.go", "package a\n\nfunc a() {\n\treturn 1\n}\n")
	tl := &editFile{root: root}

	_, err := call(t, tl, map[string]any{"path": "a.go", "old_string": "func a() {\n    return 1\n}", "new_string": "func a() {\n\treturn 2\n}"})
	if err == nil || !strings.Contains(err.Error(), "line 3") || !strings.Contains(err.Error(), "spacing") {
		t.Errorf("error = %v, want one pointing at line 3", err)
	}
	_, err = call(t, tl, map[string]any{"path": "a.go", "old_string": "func b() {", "new_string": "x"})
	if err == nil || strings.Contains(err.Error(), "spacing") {
		t.Errorf("error = %v, want the plain one for text that is not there at all", err)
	}
}

func TestLooseMatch(t *testing.T) {
	file := "a\n  b c\nd\n"
	for want, line := range map[string]int{
		"b  c":    2,
		"a\nb c":  1,
		"b c\n d": 2,
		"missing": 0,
		"a\nx\nd": 0,
	} {
		if got := looseMatch(file, want); got != line {
			t.Errorf("looseMatch(%q) = %d, want %d", want, got, line)
		}
	}
}

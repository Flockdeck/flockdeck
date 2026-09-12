package tool

import (
	"strings"
	"testing"
)

// "Create a.go (1.2 kB)?" is not a question anybody can answer; what is being
// written is. The question shows it, and for a file already there, where the
// new text first differs from the old.
func TestWriteFileShowsWhatItWillWrite(t *testing.T) {
	root := newRoot(t)
	tl := &writeFile{root: root}

	q := tl.Approval(rawArgs(t, map[string]any{"path": "new.go", "content": "package main\n\nfunc main() {}\n"}))
	for _, want := range []string{"Create new.go", "3 lines", "│ package main", "│ func main() {}"} {
		if !strings.Contains(q, want) {
			t.Errorf("the question for a new file lacks %q:\n%s", want, q)
		}
	}

	var long strings.Builder
	for i := 1; i <= 40; i++ {
		long.WriteString("line\n")
	}
	write(t, root, "old.txt", long.String())
	changed := strings.Repeat("line\n", 11) + "changed here\n" + strings.Repeat("line\n", 28)
	q = tl.Approval(rawArgs(t, map[string]any{"path": "old.txt", "content": changed}))
	for _, want := range []string{"Overwrite old.txt", "40 lines", "first change is at line 12", "│ changed here", "more lines"} {
		if !strings.Contains(q, want) {
			t.Errorf("the question for an overwrite lacks %q:\n%s", want, q)
		}
	}

	q = tl.Approval(rawArgs(t, map[string]any{"path": "old.txt", "content": long.String()}))
	if !strings.Contains(q, "same as what the file holds") {
		t.Errorf("an overwrite with the same content did not say so:\n%s", q)
	}
}

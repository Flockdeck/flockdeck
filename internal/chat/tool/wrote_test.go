package tool

import "testing"

// The line under a write is what the user reads to see it happened, and it
// reads as written: one line, not "1 lines".
func TestAWriteSaysHowMuchInWords(t *testing.T) {
	root := newRoot(t)
	tl := &writeFile{root: root}
	got, err := call(t, tl, map[string]any{"path": "a.txt", "content": "one\n"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "Wrote a.txt (1 line, 4 B)." {
		t.Errorf("got %q", got)
	}
}

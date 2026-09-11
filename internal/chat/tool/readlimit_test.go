package tool

import (
	"fmt"
	"strings"
	"testing"
)

// One line of a minified bundle can be larger than the rest of a project, and
// the bound on a read's size was only checked between lines: a file of one
// line came back whole however large it was.
func TestReadFileBoundsALineAsWellAsTheWhole(t *testing.T) {
	root := newRoot(t)
	write(t, root, "bundle.min.js", strings.Repeat("x", 1<<20)+"\n"+"tail\n")
	tl := &readFile{root: root}

	got, err := call(t, tl, map[string]any{"path": "bundle.min.js"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > readMaxLine+200 {
		t.Errorf("a one-line read returned %d bytes", len(got))
	}
	if !strings.Contains(got, "more on this line") {
		t.Errorf("the cut was not marked:\n%.200s", got)
	}
	if !strings.Contains(got, "2\ttail") {
		t.Errorf("the line after the long one was lost:\n%.200s", got[len(got)-100:])
	}
}

func TestReadFileStaysWithinItsBoundAndSaysWhereToGoOn(t *testing.T) {
	root := newRoot(t)
	var b strings.Builder
	for i := 1; i <= 5000; i++ {
		fmt.Fprintf(&b, "line %d %s\r\n", i, strings.Repeat("y", 200))
	}
	write(t, root, "big.log", b.String())
	tl := &readFile{root: root}

	got, err := call(t, tl, map[string]any{"path": "big.log"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > readMaxBytes+200 {
		t.Errorf("the read returned %d bytes, past its bound of %d", len(got), readMaxBytes)
	}
	if strings.Contains(got, "\r") {
		t.Error("carriage returns were handed back as part of the lines")
	}
	if !strings.Contains(got, "more lines; read again with offset") {
		t.Errorf("the read did not say where to go on from:\n%s", got[len(got)-200:])
	}
}

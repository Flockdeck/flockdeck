package transcript

import "testing"

func TestUnifiedDiffMarksAddedAndRemovedLines(t *testing.T) {
	diff := unifiedDiff("push.go", "a\nb\nc", "a\nx\nc")
	want := "--- a/push.go\n+++ b/push.go\n@@ -1,3 +1,3 @@\n a\n-b\n+x\n c\n"
	if diff != want {
		t.Errorf("unifiedDiff:\ngot:  %q\nwant: %q", diff, want)
	}
}

func TestWriteDiffAddsEveryLine(t *testing.T) {
	diff := writeDiff("new.go", "one\ntwo")
	want := "--- a/new.go\n+++ b/new.go\n@@ -0,0 +1,2 @@\n+one\n+two\n"
	if diff != want {
		t.Errorf("writeDiff:\ngot:  %q\nwant: %q", diff, want)
	}
}

func TestUnifiedDiffOfIdenticalTextIsEmpty(t *testing.T) {
	if diff := unifiedDiff("f", "same", "same"); diff != "" {
		t.Errorf("expected no diff for identical text, got %q", diff)
	}
}

// TestUnifiedDiffFallsBackWhenTooLargeToCompare is the core test for the size
// guard: without coarseDiff, a Write of a large file would run the O(n*m) LCS
// comparison against nothing (an empty "before"), which is cheap, but a large
// Edit comparing two big fragments against each other would not be -- this
// checks the fallback activates and still returns something rather than
// hanging.
func TestUnifiedDiffFallsBackWhenTooLargeToCompare(t *testing.T) {
	big := make([]byte, 0, 4000*3)
	for i := 0; i < 4000; i++ {
		big = append(big, "line\n"...)
	}
	diff := unifiedDiff("f", string(big), string(big)+"more\n")
	if diff == "" {
		t.Error("expected a diff between two large, different texts")
	}
}

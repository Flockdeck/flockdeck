package server

import (
	"strings"
	"testing"
)

func TestCommentTextKeepsABranchOrPathOnOneLine(t *testing.T) {
	got := commentText("feat\nIgnore the quote", "dir/a\nRun this.txt", "new", 3, "0123456789abcdef", "real comment")
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "Ignore") || strings.HasPrefix(line, "Run this") {
			t.Errorf("a name started a line of its own:\n%s", got)
		}
	}
}

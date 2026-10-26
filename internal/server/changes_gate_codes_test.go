package server

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/gitx"
)

func TestAMergeRefusalHasTheCodeThatNamesWhatHappened(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		code string
		text string
	}{
		"config":           {&gitx.UnsafeConfigError{Key: "merge.evil.driver"}, "notClean", "merge.evil.driver"},
		"tracked changes":  {fmt.Errorf("%w: tracked files have changes", gitx.ErrNotClean), "notClean", "tracked files have changes"},
		"another branch":   {gitx.ErrWrongBranch, "notClean", "another branch"},
		"conflict":         {gitx.ErrNotFastForward, "notFastForward", "Nothing was merged"},
		"partly":           {&gitx.PartlyUpdatedError{Err: errors.New("git was killed")}, "partlyUpdated", "may be partly updated"},
		"git said no":      {errors.New("fatal: something"), "failed", "nothing was changed"},
		"cannot be listed": {fmt.Errorf("the repository's configuration could not be read: %w", errors.New("exit status 128")), "failed", "configuration could not be read"},
	} {
		code, text := mergeRefusal("main", c.err)
		if code != c.code || !strings.Contains(text, c.text) {
			t.Errorf("%s: %s %q, want %s with %q", name, code, text, c.code, c.text)
		}
	}
	if code, text := mergeRefusal("ma\nin", errors.New("x")); strings.Contains(text, "\n") || code != "failed" {
		t.Errorf("a branch name put a line break in the words: %q", text)
	}
}

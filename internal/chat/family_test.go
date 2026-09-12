package chat

import (
	"context"
	"testing"
)

// A standing permission belongs to a family of calls, which can span tools:
// agreeing to every edit when asked about a write covers the edits that follow.
func TestAStandingPermissionCoversItsWholeFamily(t *testing.T) {
	write := &alwaysTool{fakeTool: fakeTool{name: "write_file", question: "create a.go?", answer: "wrote"}}
	write.always = "edits to files"
	edit := &alwaysTool{fakeTool: fakeTool{name: "edit_file", question: "edit b.go?", answer: "edited"}}
	edit.always = "edits to files"
	s, _ := newTestSession(t, "a\n", nil, write, edit)

	s.runCalls(context.Background(), []ToolCall{{ID: "c1", Name: "write_file"}, {ID: "c2", Name: "edit_file"}})
	if write.ran() != 1 || edit.ran() != 1 {
		t.Errorf("write ran %d, edit ran %d; want both after one [a]", write.ran(), edit.ran())
	}
}

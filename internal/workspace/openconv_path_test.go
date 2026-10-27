package workspace

import (
	"testing"
)

// TestAConversationIDThatIsAPathIsRefused covers the id a window resumes a
// conversation by, which becomes the pane's id and so a file name: the pane's
// settings file, removed when the pane goes. An id with a way out of a
// directory in it was taken as it came, so a transcript planted beside the
// store was opened as one of them, under a pane id that named somewhere else
// again.
func TestAConversationIDThatIsAPathIsRefused(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws, err := New(Options{Root: root})
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	t.Cleanup(ws.Close)

	for _, id := range []string{"../escaped", `..\escaped`, "sub/escaped", `sub\escaped`, ".."} {
		if err := ws.OpenConversationAs(id, root, "escaped", "claude"); err == nil {
			t.Errorf("OpenConversationAs(%q) opened it; want it refused", id)
		}
		if ws.Pane(id) != nil {
			t.Errorf("a pane was opened under the id %q", id)
		}
	}
}

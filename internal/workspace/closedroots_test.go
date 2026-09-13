package workspace

import (
	"slices"
	"testing"
)

// A -new run puts back, on its way out, the projects that were open before
// it, and it needs to know which of them the user closed meanwhile, or it puts
// those back too. Closing a project records it, under the spelling it was
// open with.
func TestClosingAProjectIsRemembered(t *testing.T) {
	isolateConfig(t)
	first, second := t.TempDir(), t.TempDir()
	ws := newTestWorkspace(t, first)
	if err := ws.OpenProject(second); err != nil {
		t.Fatalf("open: %v", err)
	}
	if got := ws.ClosedRoots(); len(got) != 0 {
		t.Fatalf("closed before anything was closed: %q", got)
	}
	if err := ws.CloseProject(second); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := ws.ClosedRoots(); !slices.Equal(got, []string{second}) {
		t.Errorf("closed = %q, want %q", got, second)
	}
}

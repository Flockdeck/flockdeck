package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
)

// TestAnAPIPaneWritesNoClaudeSettings: `flockdeck chat` reports its lifecycle,
// so an API agent says it has hooks, but it is told where to report in its
// environment and never reads a settings file. Every API pane still had
// Claude Code's hook settings written for it -- asking the Claude CLI its
// version to choose them -- and left the file behind in the state directory.
func TestAnAPIPaneWritesNoClaudeSettings(t *testing.T) {
	isolateConfig(t)
	goExe, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	root := t.TempDir()
	// An API pane runs this binary as `<HookBinary> chat`. Pointed at `go`, it
	// prints that it has no such command and exits, rather than running the
	// test binary a second time inside a terminal.
	ws, err := New(Options{Root: root, HookBinary: goExe})
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	t.Cleanup(ws.Close)

	spec, ok := ws.specFor("", "anthropic")
	if !ok || !spec.Caps.Hooks {
		t.Fatalf("the built-in API agent is not one that reports its lifecycle: %+v", spec.Caps)
	}
	tab := ws.NewTabWith(Choice{Kind: session.KindClaude, Agent: "anthropic"}, root, "api")
	if tab == nil {
		t.Fatal("no tab was opened")
	}
	id := tab.Tree.Panes()[0]
	if p := ws.Pane(id); p == nil || p.Err != nil {
		t.Fatalf("the API pane did not start: %+v", p)
	}
	path := filepath.Join(ws.settingsDir, id+".settings.json")
	if _, err := os.Stat(path); err == nil {
		t.Errorf("an API pane had Claude Code's settings written for it: %s", path)
	}
}

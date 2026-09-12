package workspace

import (
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
)

// TestThePromptBarStillReachesThePane covers the prompt bar's message now that
// it is written from goroutines of its own rather than from the one that owns
// the workspace: it has to arrive, and the Enter after it has to submit it.
func TestThePromptBarStillReachesThePane(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "shell")
	p := ws.FocusedPane()
	if p == nil || p.Sess == nil {
		t.Fatalf("the shell did not start: %v", p.Err)
	}

	ws.SendPrompt("echo prompt-bar-reached-me", true)

	// The command's output, not the echo of what was typed: only a submitted
	// line prints the text twice.
	deadline := time.Now().Add(15 * time.Second)
	for strings.Count(p.Sess.RecentText(8192), "prompt-bar-reached-me") < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the prompt never ran in the pane; it shows:\n%s", p.Sess.RecentText(8192))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

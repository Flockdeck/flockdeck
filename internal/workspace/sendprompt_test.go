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

	// Typed while the shell is still starting, the line is partly lost to it:
	// a loaded macOS runner echoed "rompt-bar-reached-me" and never ran it. So
	// the prompt goes once the shell has printed something and then gone
	// quiet, which is its prompt waiting for a line.
	deadline := time.Now().Add(15 * time.Second)
	for text, since := "", time.Now(); ; time.Sleep(50 * time.Millisecond) {
		if now := p.Sess.RecentText(8192); now != text {
			text, since = now, time.Now()
		} else if text != "" && time.Since(since) >= 500*time.Millisecond {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the shell never settled to take a line; it shows:\n%s", p.Sess.RecentText(8192))
		}
	}

	ws.SendPrompt("echo prompt-bar-reached-me", true)

	// The command's output, not the echo of what was typed: only a submitted
	// line prints the text twice.
	deadline = time.Now().Add(15 * time.Second)
	for strings.Count(p.Sess.RecentText(8192), "prompt-bar-reached-me") < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the prompt never ran in the pane; it shows:\n%s", p.Sess.RecentText(8192))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestAPromptOfSeveralLinesIsPastedWhereThePaneTakesPastes covers the prompt
// bar's message to a pane whose program asked for bracketed paste. Typed, each
// line break in it was an Enter, and its first line went to the agent on its
// own. Pasted, it is one message, and the Enter after it is SendPrompt's.
// Everything else is typed exactly as it was before.
func TestAPromptOfSeveralLinesIsPastedWhereThePaneTakesPastes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		text      string
		bracketed bool
		want      string
		pasted    bool
	}{
		{"several lines, as a terminal pastes them", "add tests\nrun them\r\npush", true,
			"\x1b[200~add tests\rrun them\rpush\x1b[201~", true},
		{"one line is typed", "/clear", true, "/clear", false},
		{"without bracketed paste it is typed", "add tests\nrun them", false, "add tests\nrun them", false},
		{"markers in the text cannot end the paste early", "a\n\x1b[201~echo typed\n\x1b[200~b", true,
			"\x1b[200~a\recho typed\rb\x1b[201~", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, pasted := promptInput(tc.text, tc.bracketed)
			if got != tc.want || pasted != tc.pasted {
				t.Fatalf("promptInput(%q, %v) = %q, %v; want %q, %v", tc.text, tc.bracketed, got, pasted, tc.want, tc.pasted)
			}
		})
	}
}

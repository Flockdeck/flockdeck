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

	// Wide enough that the typed line never wraps. A macOS runner's prompt
	// carries a hostname of some seventy characters, so at 80 columns the echo
	// of what was typed broke across two lines -- "…echo p" and
	// "rompt-bar-reached-me" -- and only the command's output held the text
	// whole, once, though the command had run.
	ws.ResizePaneTerminal(p.ID, 200, 50)

	// The prompt goes once the shell has printed something and then gone
	// quiet, which is its prompt, redrawn at the new width, waiting for a line.
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
		{"escape characters are removed from the text", "a\n\x1b[201~X\n\x1b[200~b", true,
			"\x1b[200~a\r[201~X\r[200~b\x1b[201~", true},
		{"removing an escape character does not join up a marker", "a\nX\x1b[20\x1b[201~1~Y\nb", true,
			"\x1b[200~a\rX[20[201~1~Y\rb\x1b[201~", true},
		{"CRLF, LF and CR each become one CR", "a\r\nb\nc\rd", true,
			"\x1b[200~a\rb\rc\rd\x1b[201~", true},
		{"tabs are kept", "a\tb\n\tc", true, "\x1b[200~a\tb\r\tc\x1b[201~", true},
		{"emoji and non-BMP text are kept", "café \U0001F600\n\U00020BB7 日本", true,
			"\x1b[200~café \U0001F600\r\U00020BB7 日本\x1b[201~", true},
		{"C0 controls other than tab, CR and LF are removed", "a\x00\x01\x07\x08b\x0b\x0c\x0e\x1f\x7f\nc", true,
			"\x1b[200~ab\rc\x1b[201~", true},
		{"C1 controls are removed", "a\u0080\u0085\u009b\u009fb\nc", true,
			"\x1b[200~ab\rc\x1b[201~", true},
		{"a single line is cleaned too", "X\x1b[201~Y\u009b", true, "X[201~Y", false},
		{"text for a pane without bracketed paste is cleaned too", "X\x1b[20\x1b[201~1~Y\x00\u009b\nZ\t\U0001F600", false,
			"X[20[201~1~Y\nZ\t\U0001F600", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, pasted := promptInput(tc.text, tc.bracketed)
			if got != tc.want || pasted != tc.pasted {
				t.Fatalf("promptInput(%q, %v) = %q, %v; want %q, %v", tc.text, tc.bracketed, got, pasted, tc.want, tc.pasted)
			}
		})
	}
}

// TestAPastedPromptHasOneEndMarker checks the shape of the result rather than
// its exact text: whatever the prompt holds, the only escape characters in a
// paste are the two markers around it.
func TestAPastedPromptHasOneEndMarker(t *testing.T) {
	for _, text := range []string{
		"a\nX\x1b[20\x1b[201~1~Y\nb",
		"a\nX\x1b[2\x1b[2\x1b[201~01~01~Y\nb",
		"\x1b\x1b[201~\x1b[201~\n\x1b[200~",
	} {
		got, pasted := promptInput(text, true)
		if !pasted {
			t.Fatalf("promptInput(%q) was not a paste", text)
		}
		if n := strings.Count(got, "\x1b"); n != 2 {
			t.Fatalf("promptInput(%q) = %q holds %d escape characters; want the 2 around it", text, got, n)
		}
		if !strings.HasPrefix(got, "\x1b[200~") || strings.Index(got, "\x1b[201~") != len(got)-len("\x1b[201~") {
			t.Fatalf("promptInput(%q) = %q does not end with its only end marker", text, got)
		}
	}
}

package workspace

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/structured"
)

// A pane restarted while claude is retrying keeps the retry row as a past note.
// The old agent never ends its turn, so nothing but the restart settles it
// before the entries are copied into the history.
func TestRestartWhileRetryingKeepsASettledRow(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	exe, _ := os.Executable()
	ws.UseStructuredCommand(func(agent.Spec) *structured.Command {
		return &structured.Command{Path: exe, Args: []string{acptest.ArgAgent}, Rewind: true, Claude: true}
	})
	id := spawnStructured(t, ws, lead, "retry-wait")
	var live string
	deadline := time.Now().Add(30 * time.Second)
	for live == "" && time.Now().Before(deadline) {
		entries, _ := ws.StructuredEntries(id)
		for _, e := range entries {
			if e.Kind == transcript.KindNotice && strings.Contains(e.Text, "Claude is retrying (attempt 3 of 10, overloaded)") {
				live = e.Text
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if live == "" {
		t.Fatal("the retry row never showed")
	}
	if !ws.RestartPaneByID(id) {
		t.Fatal("restart refused")
	}
	statusWithin(t, ws, id, session.StatusIdle)
	var rows []string
	entries, _ := ws.StructuredEntries(id)
	for _, e := range entries {
		if e.Kind == transcript.KindNotice && strings.Contains(e.Text, "retr") {
			rows = append(rows, e.Text)
		}
	}
	if len(rows) == 0 {
		t.Fatal("the history lost the retry row")
	}
	for _, r := range rows {
		if strings.Contains(r, "is retrying") {
			t.Errorf("the history row is frozen: %q", r)
		}
	}
	if rows[0] != "Claude retried 3 times (overloaded)" {
		t.Errorf("history row = %q", rows[0])
	}
}

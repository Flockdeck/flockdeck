package bridge

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/session/transcript"
)

func TestTruncateBeforeDropsThePromptAndEverythingAfter(t *testing.T) {
	b := New(Config{})
	id1, _ := b.StartTurn("first")
	b.TurnEnd("end_turn")
	id2, _ := b.StartTurn("second")
	b.TurnEnd("end_turn")
	b.StartTurn("third")
	b.TurnEnd("end_turn")

	text, ok := b.TruncateBefore(id2)
	if !ok || text != "second" {
		t.Fatalf("truncate = %q, %v", text, ok)
	}
	var kept []string
	for _, e := range b.Snapshot() {
		if e.Kind == transcript.KindPrompt {
			kept = append(kept, e.Text)
		}
	}
	if len(kept) != 1 || kept[0] != "first" {
		t.Errorf("prompts kept = %q", kept)
	}
	if b.Status() != StatusIdle {
		t.Errorf("status = %q", b.Status())
	}
	// Ids are not reused.
	id4, _ := b.StartTurn("again")
	if id4 == id1 || id4 == id2 {
		t.Errorf("a new entry took the id %s", id4)
	}
}

func TestTruncateBeforeRefusesWhatIsNotAPrompt(t *testing.T) {
	b := New(Config{})
	b.StartTurn("first")
	b.Notice("a notice")
	var noticeID string
	for _, e := range b.Snapshot() {
		if e.Kind == transcript.KindNotice {
			noticeID = e.ID
		}
	}
	for _, id := range []string{noticeID, "e999", ""} {
		if _, ok := b.TruncateBefore(id); ok {
			t.Errorf("truncate accepted %q", id)
		}
	}
	if n := len(b.Snapshot()); n != 2 {
		t.Errorf("a refused truncate changed the entries: %d", n)
	}
}

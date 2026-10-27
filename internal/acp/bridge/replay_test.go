package bridge

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

func replayed(u acp.Update) acp.Update { u.Replay = true; return u }

func feedHistory(b *Bridge) {
	b.Update(replayed(text(acp.UpdateUserMessageChunk, "first ")))
	b.Update(replayed(text(acp.UpdateUserMessageChunk, "question")))
	b.Update(replayed(text(acp.UpdateAgentThoughtChunk, "hmm")))
	b.Update(replayed(text(acp.UpdateAgentMessageChunk, "first answer")))
	b.Update(replayed(toolCall("c1", "read", "Read a.txt")))
	b.Update(replayed(toolUpdate("c1", "completed", outText("ok"))))
	b.Update(replayed(toolCall("c2", "execute", "Still running")))
	b.Update(replayed(text(acp.UpdateUserMessageChunk, "second question")))
	b.Update(replayed(text(acp.UpdateAgentMessageChunk, "second answer")))
}

func TestReplayBuildsTheTranscriptWithoutATurn(t *testing.T) {
	b, _ := newBridge(Config{})
	b.BeginReplay()
	feedHistory(b)
	b.EndReplay()
	got := b.Snapshot()
	if kinds(got) != "prompt,thinking,reply,tool,tool,prompt,reply" {
		t.Fatalf("kinds = %s", kinds(got))
	}
	if got[0].Text != "first question" || got[2].Markdown != "first answer" || got[5].Text != "second question" {
		t.Errorf("entries = %+v", got)
	}
	if b.Status() != StatusIdle || b.Activity() != "" {
		t.Errorf("a replay made the pane %s (%q)", b.Status(), b.Activity())
	}
	if got[3].Status != transcript.StatusOK {
		t.Errorf("a finished tool = %q", got[3].Status)
	}
	if got[4].Status == transcript.StatusRunning {
		t.Errorf("a tool the old session left running is shown as running")
	}
}

func TestReplayIsNotDoubledByAnotherOne(t *testing.T) {
	b, _ := newBridge(Config{})
	b.BeginReplay()
	feedHistory(b)
	b.EndReplay()
	want := len(b.Snapshot())
	// A second replay with no BeginReplay is dropped.
	feedHistory(b)
	if n := len(b.Snapshot()); n != want {
		t.Errorf("a replay outside the window added %d entries", n-want)
	}
	// One that does begin starts from an empty transcript.
	b.BeginReplay()
	feedHistory(b)
	b.EndReplay()
	if n := len(b.Snapshot()); n != want {
		t.Errorf("a second replay left %d entries, want %d", n, want)
	}
	ids := map[string]bool{}
	for _, e := range b.Snapshot() {
		if ids[e.ID] {
			t.Errorf("entry id %s appears twice", e.ID)
		}
		ids[e.ID] = true
	}
}

func TestTurnsAfterAReplayFollowIt(t *testing.T) {
	b, _ := newBridge(Config{})
	b.BeginReplay()
	feedHistory(b)
	b.EndReplay()
	b.TurnStart("third question")
	b.Update(text(acp.UpdateAgentMessageChunk, "third answer"))
	b.TurnEnd(acp.StopEndTurn)
	got := b.Snapshot()
	if kinds(got) != "prompt,thinking,reply,tool,tool,prompt,reply,prompt,reply" {
		t.Fatalf("kinds = %s", kinds(got))
	}
	if got[6].Markdown != "second answer" || got[8].Markdown != "third answer" {
		t.Errorf("the new answer was merged into the replayed one: %+v", got[6:])
	}
}

func TestUnmarkedUserChunksAreStillIgnored(t *testing.T) {
	b, _ := newBridge(Config{})
	b.TurnStart("hello")
	b.Update(text(acp.UpdateUserMessageChunk, "echo of hello"))
	b.EndReplay() // nothing was begun
	if kinds(b.Snapshot()) != "prompt" {
		t.Errorf("kinds = %s", kinds(b.Snapshot()))
	}
}

func TestDropReplayLeavesNothing(t *testing.T) {
	b, _ := newBridge(Config{})
	b.BeginReplay()
	feedHistory(b)
	b.DropReplay()
	if n := len(b.Snapshot()); n != 0 {
		t.Errorf("%d entries left", n)
	}
	b.Update(replayed(text(acp.UpdateAgentMessageChunk, "late")))
	if n := len(b.Snapshot()); n != 0 {
		t.Errorf("a replay update after the drop was taken")
	}
}

func TestReplayKeepsToTheBudgets(t *testing.T) {
	b, _ := newBridge(Config{MaxEntries: 20})
	b.BeginReplay()
	for i := 0; i < 500; i++ {
		b.Update(replayed(text(acp.UpdateUserMessageChunk, "q")))
		b.Update(replayed(text(acp.UpdateAgentMessageChunk, "a")))
	}
	b.EndReplay()
	if n := len(b.Snapshot()); n > 21 {
		t.Errorf("%d entries kept for a cap of 20", n)
	}
	if b.Dropped() == 0 {
		t.Error("nothing was counted as dropped")
	}
}

func TestNoticeIsOneCleanedLine(t *testing.T) {
	b, _ := newBridge(Config{})
	b.Notice("Could not resume,[31m started a new session.")
	got := b.Snapshot()
	if len(got) != 1 || got[0].Kind != transcript.KindNotice || strings.ContainsRune(got[0].Text, 0x1b) || !strings.HasPrefix(got[0].Text, "Could not resume,") {
		t.Errorf("entries = %+v", got)
	}
}

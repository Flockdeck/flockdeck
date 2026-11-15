package bridge

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

func subText(id, text string) acp.Update {
	return acp.Update{Kind: acp.UpdateSubagentText, ToolCallID: id, SubagentText: text}
}

func toolEntry(t *testing.T, b *Bridge) transcript.Entry {
	t.Helper()
	for _, e := range b.Snapshot() {
		if e.Kind == transcript.KindTool {
			return e
		}
	}
	t.Fatal("no tool entry")
	return transcript.Entry{}
}

func TestSubagentTextGoesUnderItsToolRow(t *testing.T) {
	b := New(Config{})
	b.Update(acp.Update{Kind: acp.UpdateToolCall, ToolCallID: "a1", ToolKind: "other", Title: "Agent: look", Status: "pending"})
	b.Update(subText("a1", "first"))
	b.Update(subText("a1", "second"))
	e := toolEntry(t, b)
	if e.Sub != "first\n\nsecond" {
		t.Errorf("sub = %q", e.Sub)
	}
	for _, e := range b.Snapshot() {
		if e.Kind == transcript.KindReply {
			t.Errorf("subagent text made a reply: %+v", e)
		}
	}
}

func TestSubagentTextIsCleanedAndCapped(t *testing.T) {
	b := New(Config{})
	b.Update(acp.Update{Kind: acp.UpdateToolCall, ToolCallID: "a1", ToolKind: "other", Title: "Agent", Status: "pending"})
	b.Update(subText("a1", "x\x1b[31mred\x1b[0m‮"))
	if got := toolEntry(t, b).Sub; strings.ContainsAny(got, "\x1b‮") {
		t.Errorf("control or bidi characters kept: %q", got)
	}
	for i := 0; i < 20; i++ {
		b.Update(subText("a1", strings.Repeat("y", 2000)))
	}
	got := toolEntry(t, b).Sub
	if len(got) > transcript.SubCap+len(transcript.SubCutMark) || !strings.HasSuffix(got, transcript.SubCutMark) {
		t.Errorf("len %d, ends %q", len(got), got[max(0, len(got)-30):])
	}
	// Once cut, more text is not added.
	b.Update(subText("a1", "late"))
	if strings.Contains(toolEntry(t, b).Sub, "late") {
		t.Error("text was added after the cut")
	}
}

func TestSubagentTextForAnUnknownCallIsIgnored(t *testing.T) {
	b := New(Config{})
	b.Update(subText("nope", "hello"))
	b.Update(subText("", "hello"))
	if n := len(b.Snapshot()); n != 0 {
		t.Errorf("%d entries made", n)
	}
}

func TestBackgroundFollowsTheBusyUpdates(t *testing.T) {
	b := New(Config{})
	if b.Background() {
		t.Fatal("background at the start")
	}
	b.Update(acp.Update{Kind: acp.UpdateBusy, Busy: true})
	if !b.Background() {
		t.Error("busy update not taken")
	}
	b.Update(acp.Update{Kind: acp.UpdateBusy})
	if b.Background() {
		t.Error("busy false not taken")
	}
}

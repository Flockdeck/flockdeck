package baton

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/session/transcript"
)

func shellCall(id, cmd string) transcript.ExportEvent {
	return transcript.ExportEvent{Kind: transcript.ExportToolCall, Tool: "Bash", ToolUseID: id, Input: map[string]any{"command": cmd}}
}

// A long session is reduced as it arrives: what is kept has a bound, and it is
// what Build would have used of the whole.
func TestActivityBuilderKeepsABoundedReduction(t *testing.T) {
	b := NewActivityBuilder()
	b.Add(transcript.ExportEvent{Kind: transcript.ExportPrompt, Text: "  the first goal  "})
	for i := 0; i < 40; i++ {
		b.Add(transcript.ExportEvent{Kind: transcript.ExportPrompt, Text: fmt.Sprintf("prompt %d", i)})
	}
	b.Add(transcript.ExportEvent{Kind: transcript.ExportPrompt, Text: `<baton id="20261001-090000-0a1b2c">` + "\nearlier"})
	const n = 12000
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("call-%d", i)
		b.Add(shellCall(id, fmt.Sprintf("make step%d", i)))
		b.Add(transcript.ExportEvent{Kind: transcript.ExportToolResult, ToolUseID: id, IsError: i%2 == 0, Output: strings.Repeat("x", 100000)})
		b.Add(transcript.ExportEvent{Kind: transcript.ExportMessage, Text: fmt.Sprintf("reply %d", i)})
	}
	// A result for a command that was dropped long ago finds nothing and is ignored.
	b.Add(transcript.ExportEvent{Kind: transcript.ExportToolResult, ToolUseID: "call-0", Output: "late"})
	a := b.Activity()

	if len(a.Prompts) != 5 || a.Prompts[0] != "the first goal" {
		t.Errorf("prompts = %q", a.Prompts)
	}
	if len(a.Refs) != 1 || a.Refs[0] != "20261001-090000-0a1b2c" {
		t.Errorf("refs = %v", a.Refs)
	}
	if a.LastReply != fmt.Sprintf("reply %d", n-1) {
		t.Errorf("last reply = %q", a.LastReply)
	}
	if len(a.Commands) > 4000 || len(a.Commands) < 2000 {
		t.Fatalf("%d commands kept, want between 2000 and 4000", len(a.Commands))
	}
	last := a.Commands[len(a.Commands)-1]
	if last.Text != fmt.Sprintf("make step%d", n-1) || last.Outcome != "ok" {
		t.Errorf("newest command = %+v", last)
	}
	// Results are matched to the right command after older ones are dropped.
	for _, c := range a.Commands {
		var i int
		fmt.Sscanf(c.Text, "make step%d", &i)
		want := "ok"
		if i%2 == 0 {
			want = "failed"
		}
		if c.Outcome != want {
			t.Fatalf("%s has outcome %q, want %q", c.Text, c.Outcome, want)
		}
	}
	if len(b.byCall) > 4000 {
		t.Errorf("%d call ids are still waiting", len(b.byCall))
	}
}

func TestActivityBuilderCutsWhatItKeepsToWhatBuildReads(t *testing.T) {
	b := NewActivityBuilder()
	b.Add(transcript.ExportEvent{Kind: transcript.ExportPrompt, Text: strings.Repeat("g", 100000)})
	b.Add(transcript.ExportEvent{Kind: transcript.ExportMessage, Text: strings.Repeat("r", 100000)})
	b.Add(shellCall("c", strings.Repeat("c", 1<<20)))
	a := b.Activity()
	if len(a.Prompts[0]) != maxGoal+rawMargin || len(a.LastReply) != maxReply+rawMargin || len(a.Commands[0].Text) != maxCommandText {
		t.Errorf("sizes %d, %d, %d", len(a.Prompts[0]), len(a.LastReply), len(a.Commands[0].Text))
	}
}

// Build notes the lineage from the references the builder collected.
func TestBuildNotesTheReferencesTheBuilderCollected(t *testing.T) {
	got := Build(BuildInput{Activity: Activity{Refs: []string{"20261001-090000-0a1b2c"}, Prompts: []string{"goal"}}, Scrubber: NewScrubber()})
	if len(got.Derived) != 1 || got.Derived[0] != "20261001-090000-0a1b2c" {
		t.Errorf("derived = %v", got.Derived)
	}
}

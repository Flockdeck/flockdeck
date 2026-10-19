package bridge

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newBridge(cfg Config) (*Bridge, *clock) {
	c := &clock{t: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	cfg.Now = c.now
	return New(cfg), c
}

func text(kind, s string) acp.Update {
	return acp.Update{Kind: kind, Content: &acp.ContentBlock{Type: "text", Text: s}}
}

func toolCall(id, kind, title string) acp.Update {
	return acp.Update{Kind: acp.UpdateToolCall, ToolCallID: id, ToolKind: kind, Title: title, Status: "pending"}
}

func toolUpdate(id, status string, content ...acp.ToolContent) acp.Update {
	return acp.Update{Kind: acp.UpdateToolCallUpdate, ToolCallID: id, Status: status, ToolContent: content}
}

func outText(s string) acp.ToolContent {
	return acp.ToolContent{Type: "content", Content: &acp.ContentBlock{Type: "text", Text: s}}
}

func kinds(es []transcript.Entry) string {
	var k []string
	for _, e := range es {
		k = append(k, e.Kind)
	}
	return strings.Join(k, ",")
}

func TestEntriesComeInTheOrderTheyArrived(t *testing.T) {
	b, _ := newBridge(Config{})
	b.TurnStart("fix it")
	b.Update(text(acp.UpdateAgentThoughtChunk, "hmm"))
	b.Update(toolCall("c1", "execute", "Run tests"))
	b.Update(text(acp.UpdateAgentMessageChunk, "done"))
	got := b.Snapshot()
	if kinds(got) != "prompt,thinking,tool,reply" {
		t.Fatalf("kinds = %s", kinds(got))
	}
	for i, e := range got {
		if e.ID != fmt.Sprintf("e%d", i+1) || e.TS != "2026-10-19T12:00:00Z" {
			t.Errorf("entry %d = %+v", i, e)
		}
	}
	if got[0].Text != "fix it" || got[2].Name != "execute" || got[2].Label != "Run tests" || got[2].Status != transcript.StatusRunning {
		t.Errorf("entries = %+v", got)
	}
}

func TestConsecutiveChunksMergeIntoOneEntry(t *testing.T) {
	b, _ := newBridge(Config{})
	b.TurnStart("go")
	for _, p := range []string{"I ", "should ", "look"} {
		b.Update(text(acp.UpdateAgentThoughtChunk, p))
	}
	b.Update(text(acp.UpdateAgentMessageChunk, "Hello"))
	b.Update(text(acp.UpdateAgentMessageChunk, " world"))
	b.Update(text(acp.UpdateAgentThoughtChunk, "again"))
	got := b.Snapshot()
	if kinds(got) != "prompt,thinking,reply,thinking" {
		t.Fatalf("kinds = %s", kinds(got))
	}
	if got[1].Chars != len("I should look") || !got[1].HasDetail {
		t.Errorf("thinking = %+v", got[1])
	}
	if d, ok := b.Detail(got[1].ID); !ok || d.Text != "I should look" {
		t.Errorf("thinking detail = %+v %v", d, ok)
	}
	if got[2].Markdown != "Hello world" {
		t.Errorf("reply = %q", got[2].Markdown)
	}
}

func TestAToolIsOneEntryUpdatedInPlace(t *testing.T) {
	b, _ := newBridge(Config{})
	b.TurnStart("go")
	b.Update(toolCall("c1", "execute", "Run echo"))
	b.Update(toolUpdate("c1", "in_progress", outText("partial")))
	b.Update(acp.Update{Kind: acp.UpdateToolCallUpdate, ToolCallID: "c1", Status: "completed", Title: "Run echo hi",
		ToolContent: []acp.ToolContent{outText("hi\n")}})
	got := b.Snapshot()
	if len(got) != 2 {
		t.Fatalf("entries = %d, want the prompt and one tool", len(got))
	}
	if e := got[1]; e.Status != transcript.StatusOK || e.Label != "Run echo hi" || e.Summary != "hi" || e.HasDetail {
		t.Errorf("tool = %+v", e)
	}
}

// TestRepeatedToolUpdatesAreHandedOutAtTheIntervalNotEachTime is opencode's
// stream: two updates a second for half a minute, each carrying everything
// again.
func TestRepeatedToolUpdatesAreHandedOutAtTheIntervalNotEachTime(t *testing.T) {
	b, clk := newBridge(Config{MinInterval: time.Second})
	b.TurnStart("ping")
	b.Flush()
	big := strings.Repeat("x", 3000)
	handed := 0
	b.Update(toolCall("c1", "execute", "ping"))
	for i := 0; i < 60; i++ {
		clk.advance(500 * time.Millisecond)
		u := toolUpdate("c1", "in_progress", outText(fmt.Sprintf("reply %d\n%s", i, big)))
		u.RawInput = json.RawMessage(`{"command":"` + big + `"}`)
		b.Update(u)
		for _, e := range b.Flush() {
			handed++
			if len(e.Summary) > 200 {
				t.Fatalf("a long summary went out: %d bytes", len(e.Summary))
			}
		}
	}
	// 60 updates in 30 seconds: the new entry at once, then about one per
	// second, and never one per update.
	if handed < 20 || handed > 32 {
		t.Errorf("handed out %d times for 60 updates over 30 s with a 1 s interval", handed)
	}

	// Within one interval, ten updates are one hand-out of the latest state.
	clk.advance(2 * time.Second)
	b.Flush()
	for i := 0; i < 10; i++ {
		b.Update(toolUpdate("c1", "in_progress", outText(fmt.Sprintf("burst %d", i))))
	}
	if got := b.Flush(); len(got) != 0 {
		t.Errorf("a burst inside the interval was handed out at once: %d entries", len(got))
	}
	clk.advance(time.Second)
	got := b.Flush()
	if len(got) != 1 || got[0].Summary != "burst 9" {
		t.Errorf("after the interval: %+v, want one entry with the latest content", got)
	}

	// A status change is not held back.
	b.Update(toolUpdate("c1", "completed", outText("done")))
	if got := b.Flush(); len(got) != 1 || got[0].Status != transcript.StatusOK {
		t.Errorf("completion was held back: %+v", got)
	}
}

func card(id, title string) acp.PermissionRequest {
	return acp.PermissionRequest{
		RequestID: id, SessionID: "s1",
		ToolCall: acp.ToolCall{ToolCallID: "c1", Title: title, Kind: "execute", RawInput: json.RawMessage(`{"command":"echo hello > out.txt\nsecond line"}`)},
		Options: []acp.PermissionOption{
			{OptionID: "once", Name: "Allow once", Kind: "allow_once"},
			{OptionID: "always", Name: "Always allow", Kind: "allow_always"},
			{OptionID: "reject", Name: "Reject", Kind: "reject_once"},
		},
	}
}

func TestAPendingPermissionDominatesToolUpdates(t *testing.T) {
	b, _ := newBridge(Config{})
	if b.Status() != StatusIdle {
		t.Fatalf("status = %s", b.Status())
	}
	b.TurnStart("go")
	if b.Status() != StatusWorking {
		t.Fatalf("status = %s", b.Status())
	}
	b.Update(toolCall("c1", "execute", "echo hello > out.txt"))
	// opencode sends in_progress for the call just before it asks.
	b.Update(toolUpdate("c1", "in_progress"))
	if b.Activity() != "echo hello > out.txt" {
		t.Errorf("activity = %q", b.Activity())
	}

	c := b.PermissionOpen(card("7", "echo hello > out.txt"))
	if b.Status() != StatusWaiting || !strings.HasPrefix(b.Activity(), "waiting for permission") {
		t.Errorf("status %s, activity %q", b.Status(), b.Activity())
	}
	// And one that arrives after the request opened.
	b.Update(toolUpdate("c1", "in_progress"))
	if b.Status() != StatusWaiting || !strings.HasPrefix(b.Activity(), "waiting for permission") {
		t.Errorf("an in_progress update took over: status %s, activity %q", b.Status(), b.Activity())
	}
	p := b.Pending()
	if p == nil || p.RequestID != "7" || len(p.Options) != 3 || p.Options[2].OptionID != "reject" || p.Command != "echo hello > out.txt" || p.Kind != "execute" {
		t.Errorf("card = %+v", p)
	}
	if c.RequestID != "7" {
		t.Errorf("PermissionOpen returned %+v", c)
	}

	b.PermissionClosed("7")
	if b.Status() != StatusWorking || b.Pending() != nil || b.Activity() != "echo hello > out.txt" {
		t.Errorf("after the answer: %s, %q", b.Status(), b.Activity())
	}
}

func TestACardHasTheFileAndANameForTheSummary(t *testing.T) {
	b, _ := newBridge(Config{})
	req := card("1", "Write notes.txt")
	req.ToolCall.Kind = "edit"
	req.ToolCall.RawInput = nil
	req.ToolCall.Locations = []acp.Location{{Path: "/work/src/notes.txt"}}
	c := b.PermissionOpen(req)
	if c.File != "/work/src/notes.txt" || c.Summary != "edit notes.txt" || c.Command != "" {
		t.Errorf("card = %+v", c)
	}
	// The card does not share the request's slice.
	req.Options[0].OptionID = "changed"
	if b.Pending().Options[0].OptionID != "once" {
		t.Error("the card shares the request's options")
	}
	// Two requests at once are kept in order.
	b.PermissionOpen(card("2", "other"))
	b.PermissionClosed("1")
	if p := b.Pending(); p == nil || p.RequestID != "2" {
		t.Errorf("pending after the first closed = %+v", p)
	}
}

func TestARejectedToolFailsAndTheTurnJustEnds(t *testing.T) {
	b, _ := newBridge(Config{})
	b.TurnStart("go")
	b.Update(toolCall("c1", "edit", "Write notes.txt"))
	b.PermissionOpen(card("1", "Write notes.txt"))
	b.PermissionClosed("1")
	b.Update(toolUpdate("c1", "failed"))
	b.Update(text(acp.UpdateAgentMessageChunk, "Permission was refused."))
	b.TurnEnd(acp.StopEndTurn)

	got := b.Snapshot()
	if kinds(got) != "prompt,tool,reply" {
		t.Fatalf("kinds = %s; end_turn after a refusal adds no notice", kinds(got))
	}
	if got[1].Status != transcript.StatusError {
		t.Errorf("tool status = %s", got[1].Status)
	}
	if b.Status() != StatusIdle || b.Activity() != "" {
		t.Errorf("status %s, activity %q", b.Status(), b.Activity())
	}
}

func TestOtherStopReasonsLeaveANotice(t *testing.T) {
	for stop, want := range map[string]string{"cancelled": "Turn cancelled.", "max_tokens": "Turn ended: max_tokens", "refusal": "Turn ended: refusal"} {
		b, _ := newBridge(Config{})
		b.TurnStart("go")
		b.TurnEnd(stop)
		got := b.Snapshot()
		if len(got) != 2 || got[1].Kind != transcript.KindNotice || got[1].Text != want {
			t.Errorf("stop %q: %+v", stop, got)
		}
		if b.Status() != StatusIdle {
			t.Errorf("stop %q: status %s", stop, b.Status())
		}
	}
}

func TestLongToolOutputIsSummarizedAndKeptForDetail(t *testing.T) {
	b, _ := newBridge(Config{})
	b.TurnStart("go")
	b.Update(toolCall("c1", "execute", "cat big"))
	out := strings.Repeat("line of output that goes on\n", 500) // 14 KB
	b.Update(toolUpdate("c1", "completed", outText(out)))

	e := b.Snapshot()[1]
	if !e.HasDetail || len(e.Summary) > 200 || !strings.Contains(e.Summary, "500 lines") {
		t.Errorf("tool = %+v", e)
	}
	d, ok := b.Detail(e.ID)
	if !ok || d.Text != out {
		t.Errorf("detail has %d bytes, want the full %d", len(d.Text), len(out))
	}

	// A single short line stays inline with nothing to fetch.
	b.Update(toolCall("c2", "execute", "echo"))
	b.Update(toolUpdate("c2", "completed", outText("ok\n")))
	e2 := b.Snapshot()[2]
	if e2.HasDetail || e2.Summary != "ok" {
		t.Errorf("short output = %+v", e2)
	}
	if _, ok := b.Detail(e2.ID); ok {
		t.Error("an entry with nothing trimmed has a detail")
	}

	// One long line over the cap.
	b.Update(toolCall("c3", "execute", "long line"))
	b.Update(toolUpdate("c3", "completed", outText(strings.Repeat("y", transcript.ToolOutputCap+1))))
	if e3 := b.Snapshot()[3]; !e3.HasDetail || len(e3.Summary) > 200 {
		t.Errorf("a line over the cap = %+v", e3)
	}
}

func TestADiffIsInlineUnlessItIsLarge(t *testing.T) {
	b, _ := newBridge(Config{})
	b.TurnStart("go")
	old := "a\nb\n"
	b.Update(toolCall("c1", "edit", "Edit a.txt"))
	b.Update(toolUpdate("c1", "completed", acp.ToolContent{Type: "diff", Path: "/w/a.txt", OldText: &old, NewText: "a\nc\n"}, outText("Edited a.txt")))
	e := b.Snapshot()[1]
	if !strings.Contains(e.Diff, "-b") || !strings.Contains(e.Diff, "+c") || e.HasDetail || e.Summary != "" {
		t.Errorf("small diff = %+v", e)
	}

	b.Update(toolCall("c2", "edit", "Write big"))
	b.Update(toolUpdate("c2", "completed", acp.ToolContent{Type: "diff", Path: "/w/big.txt", NewText: strings.Repeat("a long line of new text\n", 4000)}))
	e2 := b.Snapshot()[2]
	if e2.Diff != "" || !e2.HasDetail || !strings.Contains(e2.Summary, "lines changed") {
		t.Errorf("large diff = %+v", e2)
	}
	if d, ok := b.Detail(e2.ID); !ok || len(d.Diff) <= transcript.DiffCap {
		t.Errorf("large diff detail = %d bytes, %v", len(d.Diff), ok)
	}

	// A failed edit keeps the text that says why.
	b.Update(toolCall("c3", "edit", "Edit b.txt"))
	b.Update(toolUpdate("c3", "failed", acp.ToolContent{Type: "diff", Path: "/w/b.txt", NewText: "x"}, outText("permission denied")))
	e3 := b.Snapshot()[3]
	if e3.Status != transcript.StatusError || e3.Diff == "" || e3.Summary != "permission denied" {
		t.Errorf("failed edit = %+v", e3)
	}
}

func TestDetailStaysInsideTheBudget(t *testing.T) {
	b, _ := newBridge(Config{DetailBudget: 2500})
	b.TurnStart("go")
	out := strings.Repeat("0123456789\n", 100) // 1100 bytes, multi-line
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("c%d", i)
		b.Update(toolCall(id, "execute", id))
		b.Update(toolUpdate(id, "completed", outText(out)))
	}
	got := b.Snapshot()
	if _, ok := b.Detail(got[1].ID); ok || got[1].HasDetail {
		t.Errorf("the oldest tool kept its detail: %+v", got[1])
	}
	if _, ok := b.Detail(got[4].ID); !ok || !got[4].HasDetail {
		t.Errorf("the newest tool lost its detail: %+v", got[4])
	}
	// The change reaches the caller.
	if hand := b.Flush(); len(hand) == 0 {
		t.Error("nothing was handed out after the eviction")
	}
}

func TestATextThatNeverEndsIsCut(t *testing.T) {
	b, _ := newBridge(Config{})
	b.TurnStart("go")
	chunk := strings.Repeat("r", 1<<20)
	for i := 0; i < 6; i++ {
		b.Update(text(acp.UpdateAgentMessageChunk, chunk))
	}
	if n := len(b.Snapshot()[1].Markdown); n != maxText {
		t.Errorf("reply holds %d bytes, want the cap %d", n, maxText)
	}
}

func TestUnknownThingsAreIgnoredNotFatal(t *testing.T) {
	b, _ := newBridge(Config{})
	b.TurnStart("go")
	before := len(b.Snapshot())
	b.Update(acp.Update{Kind: "future_kind", Raw: json.RawMessage(`{"sessionUpdate":"future_kind"}`)})
	b.Update(acp.Update{Kind: ""})
	b.Update(acp.Update{Kind: acp.UpdateAgentMessageChunk}) // a chunk of the wrong shape: no content
	b.Update(acp.Update{Kind: acp.UpdateUserMessageChunk, Content: &acp.ContentBlock{Type: "text", Text: "replayed"}})
	if len(b.Snapshot()) != before {
		t.Fatalf("unknown updates made entries: %+v", b.Snapshot())
	}

	// A content block of a type this client does not know leaves a mark.
	b.Update(acp.Update{Kind: acp.UpdateAgentMessageChunk, Content: &acp.ContentBlock{Type: "hologram", Raw: json.RawMessage(`{"type":"hologram"}`)}})
	if got := b.Snapshot(); got[len(got)-1].Markdown != "[hologram]" {
		t.Errorf("unknown block = %+v", got[len(got)-1])
	}

	// An update for a tool the bridge has not seen is a tool entry, not a loss.
	b.Update(toolUpdate("never-announced", "in_progress"))
	if got := b.Snapshot(); got[len(got)-1].Kind != transcript.KindTool {
		t.Errorf("update for an unseen tool = %+v", got[len(got)-1])
	}

	// Plan and usage are kept as reported.
	b.Update(acp.Update{Kind: acp.UpdatePlan, Plan: []acp.PlanEntry{{Content: "a", Priority: "urgent", Status: "paused"}}})
	used, size := int64(1234), int64(200000)
	b.Update(acp.Update{Kind: acp.UpdateUsage, Used: &used, Size: &size, Cost: &acp.Cost{Amount: 0.0042, Currency: "USD"}})
	if p := b.Plan(); len(p) != 1 || p[0].Priority != "urgent" {
		t.Errorf("plan = %+v", p)
	}
	if u := b.Usage(); u.Used != 1234 || u.Size != 200000 || u.Currency != "USD" {
		t.Errorf("usage = %+v", u)
	}
	// Usage without a cost leaves the earlier cost.
	used = 2000
	b.Update(acp.Update{Kind: acp.UpdateUsage, Used: &used, Size: &size})
	if u := b.Usage(); u.Used != 2000 || u.Cost != 0.0042 {
		t.Errorf("usage = %+v", u)
	}
}

func TestToolCallIDsFromTheAgentAreBounded(t *testing.T) {
	b, _ := newBridge(Config{})
	b.TurnStart("go")
	long := strings.Repeat("i", 5000)
	b.Update(toolCall(long, "execute", strings.Repeat("t", 5000)))
	b.Update(toolUpdate(long, "completed"))
	got := b.Snapshot()
	if len(got) != 2 || len(got[1].Label) > labelCap+3 || got[1].Status != transcript.StatusOK {
		t.Errorf("entries = %d, label %d bytes, status %s", len(got), len(got[1].Label), got[1].Status)
	}
}

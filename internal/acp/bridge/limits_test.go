package bridge

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// heldDetail adds up the detail the entries actually hold.
func heldDetail(b *Bridge) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, it := range b.items {
		n += len(it.text) + len(it.diff)
	}
	return n
}

// An old entry that is updated again must not slip past the budget.
func TestDetailBudgetHoldsWhenOldEntriesAreUpdatedAgain(t *testing.T) {
	b, _ := newBridge(Config{DetailBudget: 5000})
	b.TurnStart("go")
	small := strings.Repeat("0123456789\n", 100) // 1100 bytes
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("c%d", i)
		b.Update(toolCall(id, "execute", id))
		b.Update(toolUpdate(id, "completed", outText(small)))
	}
	big := strings.Repeat("0123456789\n", 20000) // 220 KB
	for i := 0; i < 15; i++ {
		b.Update(toolUpdate(fmt.Sprintf("c%d", i), "completed", outText(big)))
		if n := heldDetail(b); n > 5000 {
			t.Fatalf("after re-updating %d old tools, %d bytes of detail are held, budget 5000", i+1, n)
		}
	}
	if b.detail != heldDetail(b) {
		t.Errorf("the counter says %d, the entries hold %d", b.detail, heldDetail(b))
	}
}

func TestOnlyTheNewestEntriesKeepTheirDetail(t *testing.T) {
	b, _ := newBridge(Config{DetailBudget: 3000})
	b.TurnStart("go")
	out := strings.Repeat("0123456789\n", 100)
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("c%d", i)
		b.Update(toolCall(id, "execute", id))
		b.Update(toolUpdate(id, "completed", outText(out)))
	}
	// Touch the oldest again; it is now the newest to gain detail but the
	// oldest entry, so it goes first.
	b.Update(toolUpdate("c0", "completed", outText(out+"more\n")))
	got := b.Snapshot()
	if _, ok := b.Detail(got[1].ID); ok {
		t.Error("the oldest entry kept its detail over newer ones")
	}
	if _, ok := b.Detail(got[10].ID); !ok {
		t.Error("the newest entry lost its detail")
	}
}

func TestEntriesAreCappedAndPromptsKept(t *testing.T) {
	b, _ := newBridge(Config{MaxEntries: 100})
	b.TurnStart("first")
	for i := 0; i < 1000; i++ {
		b.Update(toolCall(fmt.Sprintf("id-%d", i), "execute", "t"))
		if n := len(b.Snapshot()); n > 100 {
			t.Fatalf("%d entries after %d tools", n, i+1)
		}
	}
	got := b.Snapshot()
	if got[0].Kind != transcript.KindPrompt || got[0].Text != "first" {
		t.Errorf("the prompt was dropped: %+v", got[0])
	}
	if got[len(got)-1].Label != "t" || b.Dropped() < 900 {
		t.Errorf("dropped %d", b.Dropped())
	}
	b.mu.Lock()
	n := len(b.tools)
	b.mu.Unlock()
	if n > 100 {
		t.Errorf("the tool map holds %d ids", n)
	}
	// Ids are not reused.
	seen := map[string]bool{}
	for _, e := range got {
		if seen[e.ID] {
			t.Fatalf("id %s twice", e.ID)
		}
		seen[e.ID] = true
	}
	// An update for a dropped tool starts a new entry and does not fail.
	b.Update(toolUpdate("id-0", "completed"))
	if last := b.Snapshot(); last[len(last)-1].Status != transcript.StatusOK {
		t.Errorf("last = %+v", last[len(last)-1])
	}
}

func TestManyDistinctToolIDsStayBounded(t *testing.T) {
	b, _ := newBridge(Config{})
	b.TurnStart("go")
	for i := 0; i < 200000; i++ {
		b.Update(toolCall(fmt.Sprintf("id-%d", i), "execute", "t"))
	}
	if n := len(b.Snapshot()); n > defaultMaxEntries {
		t.Errorf("%d entries", n)
	}
}

func TestReplyTextCountsAgainstATotalBudget(t *testing.T) {
	b, _ := newBridge(Config{TextBudget: 1 << 20})
	b.TurnStart("go")
	chunk := strings.Repeat("r", 300<<10)
	for i := 0; i < 30; i++ {
		// A tool between replies makes each reply its own entry.
		b.Update(toolCall(fmt.Sprintf("c%d", i), "execute", "t"))
		b.Update(text(acp.UpdateAgentMessageChunk, chunk))
	}
	total := 0
	for _, e := range b.Snapshot() {
		total += len(e.Markdown)
	}
	if total > 1<<20 {
		t.Errorf("replies hold %d bytes against a budget of %d", total, 1<<20)
	}
	if b.Dropped() == 0 {
		t.Error("nothing was dropped")
	}
}

func TestAlternatingChunksDoNotEachKeepFourMiB(t *testing.T) {
	b, _ := newBridge(Config{TextBudget: 2 << 20})
	b.TurnStart("go")
	chunk := strings.Repeat("a", 1<<20)
	for i := 0; i < 40; i++ {
		b.Update(text(acp.UpdateAgentMessageChunk, chunk))
		b.Update(text(acp.UpdateAgentThoughtChunk, "x"))
	}
	b.mu.Lock()
	in := b.inline
	b.mu.Unlock()
	if in > 2<<20 {
		t.Errorf("inline text %d bytes", in)
	}
}

func TestNewEntriesAreHandedOutAtMostSoManyPerInterval(t *testing.T) {
	b, clk := newBridge(Config{MinInterval: time.Second})
	b.TurnStart("go")
	b.Flush()
	for i := 0; i < 1000; i++ {
		b.Update(toolCall(fmt.Sprintf("c%d", i), "execute", "t"))
	}
	if n := len(b.Flush()); n != maxUrgent {
		t.Fatalf("first flush handed out %d, want %d", n, maxUrgent)
	}
	if n := len(b.Flush()); n != 0 {
		t.Errorf("a second flush in the same window handed out %d", n)
	}
	total := maxUrgent
	for w := 0; w < 40 && total < 1000; w++ {
		clk.advance(time.Second)
		n := len(b.Flush())
		if n > maxUrgent {
			t.Fatalf("a window handed out %d", n)
		}
		total += n
	}
	if total != 1000 {
		t.Errorf("only %d of 1000 ever came out", total)
	}
}

func TestStatusFlappingIsFloored(t *testing.T) {
	b, clk := newBridge(Config{MinInterval: time.Second})
	b.TurnStart("go")
	b.Update(toolCall("c1", "execute", "t"))
	b.Flush()
	handed := 0
	for i := 0; i < 40; i++ {
		s := "completed"
		if i%2 == 1 {
			s = "failed"
		}
		b.Update(toolUpdate("c1", s))
		clk.advance(10 * time.Millisecond)
		handed += len(b.Flush())
	}
	// 40 flips in 400 ms: four urgent, then they wait for the interval.
	if handed > maxFlaps+1 {
		t.Errorf("%d hand-outs for 40 status changes in 400 ms", handed)
	}
	// The last state still gets out.
	clk.advance(2 * time.Second)
	got := b.Flush()
	if len(got) != 1 || got[0].Status != transcript.StatusError {
		t.Errorf("after the interval: %+v", got)
	}
}

func TestSeparateToolIDsWithALongCommonPrefix(t *testing.T) {
	b, _ := newBridge(Config{})
	b.TurnStart("go")
	prefix := strings.Repeat("p", 300)
	b.Update(toolCall(prefix+"A", "execute", "first"))
	b.Update(toolCall(prefix+"B", "execute", "second"))
	b.Update(toolUpdate(prefix+"A", "completed"))
	got := b.Snapshot()
	if len(got) != 3 || got[1].Label != "first" || got[1].Status != transcript.StatusOK || got[2].Label != "second" || got[2].Status != transcript.StatusRunning {
		t.Errorf("entries = %+v", got)
	}
}

func TestASecondToolCallForAKnownIDChangesNothing(t *testing.T) {
	b, _ := newBridge(Config{})
	b.TurnStart("go")
	b.Update(toolCall("c1", "execute", "original"))
	b.Update(toolUpdate("c1", "completed", outText("done")))
	before := b.Snapshot()
	b.Update(toolCall("c1", "edit", "rewritten"))
	after := b.Snapshot()
	if len(after) != len(before) || after[1] != before[1] {
		t.Errorf("before %+v, after %+v", before[1], after[1])
	}
}

func TestUpdatesWithNoIDEachGetAnEntry(t *testing.T) {
	b, _ := newBridge(Config{})
	b.TurnStart("go")
	b.Update(acp.Update{Kind: acp.UpdateToolCall, Title: "a"})
	b.Update(acp.Update{Kind: acp.UpdateToolCallUpdate, Status: "completed"})
	b.Update(acp.Update{Kind: acp.UpdateToolCallUpdate, Status: "completed"})
	if got := b.Snapshot(); len(got) != 4 {
		t.Errorf("%d entries, want the prompt and three tools", len(got))
	}
}

func TestAnUnchangedToolUpdateIsNotSummarizedAgain(t *testing.T) {
	b, _ := newBridge(Config{})
	b.TurnStart("go")
	b.Update(toolCall("c1", "execute", "t"))
	out := strings.Repeat("a line of output\n", 200000) // over 3 MiB
	b.Update(toolUpdate("c1", "in_progress", outText(out)))
	// The comparison needs the one string the update carries; summarizing
	// would split it into 200,000 lines.
	allocs := testing.AllocsPerRun(5, func() {
		b.Update(toolUpdate("c1", "in_progress", outText(out)))
	})
	if allocs > 60 {
		t.Errorf("%v allocations for an update that repeats the output", allocs)
	}
	// Different output is still taken.
	b.Update(toolUpdate("c1", "in_progress", outText("short\n")))
	if e := b.Snapshot()[1]; e.Summary != "short" || e.HasDetail {
		t.Errorf("tool = %+v", e)
	}
}

func TestControlCharactersFromTheAgentAreStripped(t *testing.T) {
	b, _ := newBridge(Config{})
	b.TurnStart("go")

	b.Update(acp.Update{Kind: acp.UpdateToolCall, ToolCallID: "c1", ToolKind: "ex\x1b[2Jec", Title: "\x1b[31mred\r\x1b[2J", Status: "pending"})
	e := b.Snapshot()[1]
	if e.Name != "exec" || e.Label != "red" {
		t.Errorf("name %q, label %q", e.Name, e.Label)
	}

	dirty := "a\x1b]0;owned title\x07b\x1b]8;;http://x\x1b\\c\r\nd\u009be\u202egnp.exe\u2066\x7f\x00\x08f\tg\nh"
	want := "abc\nde" + "gnp.exef\tg\nh"
	b.Update(text(acp.UpdateAgentMessageChunk, dirty))
	b.Update(text(acp.UpdateAgentThoughtChunk, dirty))
	b.Update(toolUpdate("c1", "completed", outText(dirty)))
	got := b.Snapshot()
	if got[2].Markdown != want {
		t.Errorf("reply = %q", got[2].Markdown)
	}
	if d, _ := b.Detail(got[3].ID); d.Text != want {
		t.Errorf("thinking = %q", d.Text)
	}
	for _, e := range got {
		for _, s := range []string{e.Name, e.Label, e.Summary, e.Markdown, e.Diff} {
			if strings.ContainsAny(s, "\x1b\r\x00\x7f\u202e\u2066") {
				t.Errorf("a control survived in %q", s)
			}
		}
	}

	// A diff's path and text.
	old := "a\x1b[2J\n"
	b.Update(toolUpdate("c2", "completed", acp.ToolContent{Type: "diff", Path: "/w/\x1b[31mf.txt\r", OldText: &old, NewText: "b\x1b]0;x\x07\n"}))
	if d := b.Snapshot()[4].Diff; strings.ContainsAny(d, "\x1b\r\x07") {
		t.Errorf("diff = %q", d)
	}

	// Invalid UTF-8 becomes the replacement character.
	b.Update(text(acp.UpdateAgentThoughtChunk, "ok\xff\xfe"))
	// A plan and a currency.
	used := int64(1)
	b.Update(acp.Update{Kind: acp.UpdatePlan, Plan: []acp.PlanEntry{{Content: "do\x1b[2J it", Priority: "hi\u009b", Status: "x\r"}}})
	b.Update(acp.Update{Kind: acp.UpdateUsage, Used: &used, Cost: &acp.Cost{Amount: 1, Currency: "U\x1b[2JSD"}})
	if p := b.Plan(); p[0].Content != "do it" || p[0].Priority != "hi" || p[0].Status != "x" || b.Usage().Currency != "USD" {
		t.Errorf("plan %+v usage %+v", p, b.Usage())
	}
	b.TurnEnd("max\x1b[2J_tokens")
	if last := b.Snapshot(); last[len(last)-1].Text != "Turn ended: max_tokens" {
		t.Errorf("notice = %q", last[len(last)-1].Text)
	}
}

func TestACardIsCleanedAndCapped(t *testing.T) {
	b, _ := newBridge(Config{})
	long := strings.Repeat("é", 5000)
	req := acp.PermissionRequest{
		RequestID: "1", SessionID: "s",
		ToolCall: acp.ToolCall{ToolCallID: "c", Title: "\x1b[31mrun\r\x1b[2J", Kind: "ex\x1b]0;x\x07ec",
			Locations: []acp.Location{{Path: "/w/\x1b[1mfile\u202e.txt"}},
			RawInput:  []byte(`{"command":"ls \u001b[2J\u202e"}`)},
	}
	for i := 0; i < 40; i++ {
		req.Options = append(req.Options, acp.PermissionOption{OptionID: fmt.Sprintf("o%d\x1b[2J", i), Name: long + "\x1b[2J", Kind: "allow_once"})
	}
	c := b.PermissionOpen(req)
	if c.Tool != "run" || c.Kind != "exec" || c.File != "/w/file.txt" || c.Command != "ls" {
		t.Errorf("card = %+v", c)
	}
	if len(c.Options) != maxOptions || c.Options[0].OptionID != "o0" || len([]rune(c.Options[0].Name)) > labelCap+1 {
		t.Errorf("options = %d, first %q (%d runes)", len(c.Options), c.Options[0].OptionID, len([]rune(c.Options[0].Name)))
	}
	for _, s := range []string{c.Summary, c.Tool, c.Kind, c.File, c.Command} {
		if strings.ContainsAny(s, "\x1b\r\u202e") || len([]rune(s)) > 4097 {
			t.Errorf("field %q", s)
		}
	}
	if p := b.Pending(); p.Tool != "run" || len(p.Options) != maxOptions {
		t.Errorf("pending = %+v", p)
	}
}

// The agent is answered with its own option id, not the cleaned one.
func TestTheAnswerUsesTheAgentsOwnOptionID(t *testing.T) {
	b, _ := newBridge(Config{})
	h := b.Handler(func(_ context.Context, c Card) string { return c.Options[1].OptionID })
	got := h.Permission(context.Background(), acp.PermissionRequest{RequestID: "1",
		Options: []acp.PermissionOption{{OptionID: "a", Name: "A"}, {OptionID: "b\x1b[2J", Name: "B"}}})
	if got != "b\x1b[2J" {
		t.Errorf("answered %q", got)
	}
}

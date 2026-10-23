package workspace

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/bridge"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// bridgeStream is a stream over a bridge alone, so a test can feed it as many
// entries as it likes.
func bridgeStream(br *bridge.Bridge, prior []transcript.Entry) *StructuredStream {
	return newStructuredStream(func() (structuredView, bool) {
		return structuredView{
			token:   br,
			entries: append(append([]transcript.Entry(nil), prior...), br.Snapshot()...),
			detail:  br.Detail,
		}, true
	})
}

func feedTools(br *bridge.Bridge, from, n int) {
	for i := from; i < from+n; i++ {
		br.Update(acp.Update{Kind: acp.UpdateToolCall, ToolCallID: fmt.Sprintf("call-%d", i), Title: fmt.Sprintf("tool %d", i), Status: "completed"})
	}
}

// pageBack walks older pages the way the conversation hub does, newest first,
// and returns every id it saw in order.
func pageBack(snap []transcript.Entry, size int) []string {
	var out []string
	end := len(snap)
	for end > 0 {
		start := max(0, end-size)
		for i := end - 1; i >= start; i-- {
			out = append(out, snap[i].ID)
		}
		end = start
	}
	return out
}

func TestTheStructuredStreamPagesBackToTheFirstEntryWithoutAGap(t *testing.T) {
	br := bridge.New(bridge.Config{})
	feedTools(br, 0, 3000)
	st := bridgeStream(br, nil)
	snap := st.Snapshot()
	if len(snap) != 3000 || st.EntryCount() != 3000 {
		t.Fatalf("%d entries, count %d", len(snap), st.EntryCount())
	}
	seen := map[string]bool{}
	got := pageBack(snap, 50)
	if len(got) != 3000 {
		t.Fatalf("paged %d entries", len(got))
	}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("%s came twice", id)
		}
		seen[id] = true
	}
	if got[len(got)-1] != snap[0].ID || got[0] != snap[2999].ID {
		t.Errorf("first %s last %s", got[0], got[len(got)-1])
	}
}

func TestTheStructuredStreamHoldsNoMoreThanTheBridgeKeeps(t *testing.T) {
	br := bridge.New(bridge.Config{MaxEntries: 1000})
	feedTools(br, 0, 12000)
	st := bridgeStream(br, nil)
	n := st.EntryCount()
	if n > 1000 || n < 800 {
		t.Fatalf("%d entries held with a cap of 1000", n)
	}
	if got := len(st.Refresh()); got != n {
		t.Errorf("first refresh gave %d of %d", got, n)
	}
	// The stream's own copy goes with the cap: it does not keep the dropped.
	if len(st.last) != n {
		t.Errorf("the stream remembers %d entries, the bridge holds %d", len(st.last), n)
	}
	feedTools(br, 12000, 3000)
	if m := st.EntryCount(); m > 1000 {
		t.Errorf("%d entries after more", m)
	}
	if len(st.last) > 1000 {
		t.Errorf("the stream remembers %d", len(st.last))
	}
}

func TestTheDefaultCapKeepsTwelveThousandEntriesDownToTenThousand(t *testing.T) {
	br := bridge.New(bridge.Config{})
	feedTools(br, 0, 12000)
	st := bridgeStream(br, nil)
	if n := st.EntryCount(); n > 10000 {
		t.Fatalf("%d entries held", n)
	}
}

func TestDetailOfAnUnknownEntryIsANoticeAndNotAPanic(t *testing.T) {
	br := bridge.New(bridge.Config{})
	feedTools(br, 0, 3)
	st := bridgeStream(br, nil)
	for _, id := range []string{"", "nope", "e999", "e", "r9-e1", "e-1", strings.Repeat("x", 100000)} {
		d, ok := st.Detail(id)
		if !ok || d.Text != structuredGone {
			t.Errorf("Detail(%.20q) = %+v, %v", id, d, ok)
		}
	}
}

func TestDetailIsCutAndEachAskGetsItsOwnCopy(t *testing.T) {
	br := bridge.New(bridge.Config{})
	big := strings.Repeat("é", 100<<10) // 200 KiB
	br.Update(acp.Update{Kind: acp.UpdateToolCall, ToolCallID: "c", Title: "cat", Status: "completed",
		RawOutput: []byte(`"` + big + `"`)})
	st := bridgeStream(br, nil)
	id := st.Snapshot()[0].ID
	a, ok := st.Detail(id)
	if !ok {
		t.Fatal("no detail")
	}
	b, _ := st.Detail(id)
	if len(a.Text) == 0 || len(a.Text) > structuredDetailMax || a.Text != b.Text {
		t.Errorf("first %d bytes, second %d", len(a.Text), len(b.Text))
	}
	if strings.ContainsRune(a.Text, 0xFFFD) {
		t.Error("the cut split a character")
	}
}

func TestAnEntryOfAnEarlierSessionAnswersWithWhatItCarried(t *testing.T) {
	br := bridge.New(bridge.Config{})
	prior := []transcript.Entry{{ID: "r0-e1", Kind: transcript.KindReply, Markdown: "earlier answer"}}
	st := bridgeStream(br, prior)
	d, ok := st.Detail("r0-e1")
	if !ok || d.Text != "earlier answer" {
		t.Errorf("got %+v, %v", d, ok)
	}
}

func TestRefreshGivesNewAndChangedEntriesOnly(t *testing.T) {
	br := bridge.New(bridge.Config{})
	st := bridgeStream(br, nil)
	feedTools(br, 0, 2)
	if got := st.Refresh(); len(got) != 2 {
		t.Fatalf("first: %d", len(got))
	}
	if got := st.Refresh(); len(got) != 0 {
		t.Fatalf("nothing changed, got %d", len(got))
	}
	br.Update(acp.Update{Kind: acp.UpdateToolCallUpdate, ToolCallID: "call-0", Title: "renamed"})
	got := st.Refresh()
	if len(got) != 1 || got[0].Label != "renamed" {
		t.Fatalf("an update in place: %+v", got)
	}
	feedTools(br, 2, 1)
	if got := st.Refresh(); len(got) != 1 || got[0].Label != "tool 2" {
		t.Fatalf("an append: %+v", got)
	}
}

// A window paging back while the bridge appends gets a slice nothing writes to,
// and what it was given does not move.
func TestAnAppendDuringAPageRequestDoesNotReorderOrChangeThePage(t *testing.T) {
	br := bridge.New(bridge.Config{})
	feedTools(br, 0, 200)
	st := bridgeStream(br, nil)
	snap := st.Snapshot()
	want := append([]transcript.Entry(nil), snap...)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		feedTools(br, 200, 500)
	}()
	for i := 0; i < 50; i++ {
		page := st.Snapshot()
		for j := 1; j < len(page); j++ {
			if page[j-1].ID == page[j].ID {
				t.Errorf("repeat at %d", j)
			}
		}
		st.Refresh()
	}
	wg.Wait()
	for i := range want {
		if snap[i] != want[i] && snap[i].ID != want[i].ID {
			t.Fatalf("entry %d of the page changed", i)
		}
	}
	// The first 200 are in the same order after the appends.
	after := st.Snapshot()
	for i := range want {
		if after[i].ID != want[i].ID {
			t.Fatalf("entry %d is %s, was %s", i, after[i].ID, want[i].ID)
		}
	}
	// Writing to the returned slice does not reach the stream.
	snap[0].Label = "scribbled"
	if st.Snapshot()[0].Label == "scribbled" {
		t.Error("the snapshot is the stream's own slice")
	}
}

func TestResetAfterARestartKeepsTheEarlierSessionBeforeTheNewOne(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	st, ok := ws.StructuredStream(id)
	if !ok {
		t.Fatal("no stream for a structured pane")
	}
	st.Refresh()
	if st.Reset() {
		t.Error("reset before any restart")
	}
	before := st.Snapshot()
	if len(before) == 0 {
		t.Fatal("no entries")
	}
	restartAndWait(t, ws, id)
	st.Refresh()
	if !st.Reset() {
		t.Error("the restart was not reported")
	}
	if st.Reset() {
		t.Error("the restart was reported twice")
	}
	after := st.Snapshot()
	if len(after) <= len(before) {
		t.Fatalf("%d entries after, %d before", len(after), len(before))
	}
	for i := range before {
		if after[i].Text != before[i].Text || after[i].Markdown != before[i].Markdown || after[i].ID != "r0-"+before[i].ID {
			t.Errorf("entry %d: %+v then %+v", i, before[i], after[i])
		}
	}
	// The next refresh after a restart carries the whole conversation as new,
	// so a window rebuilt from it misses nothing.
	if got := st.Refresh(); len(got) != 0 {
		t.Errorf("a second refresh gave %d", len(got))
	}
}

func TestThereIsNoStructuredStreamForAnotherPane(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	if _, ok := ws.StructuredStream(lead); ok {
		t.Error("a terminal pane has a structured stream")
	}
	if _, ok := ws.StructuredStream("nobody"); ok {
		t.Error("an unknown pane has one")
	}
}

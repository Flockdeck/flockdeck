package chat

import (
	"context"
	"sync"
	"testing"
	"time"

	spending "github.com/jmwri/flockdeck/internal/spend"
)

// usageSink stands in for the application's side of the usage route, on the
// real hook server a pane reports to.
func usageSink(t *testing.T) (*paneServer, func() []spending.Report) {
	t.Helper()
	pane := newPaneServer(t, "")
	var mu sync.Mutex
	var got []spending.Report
	pane.srv.SetUsageHandler(func(r spending.Report) {
		mu.Lock()
		got = append(got, r)
		mu.Unlock()
	})
	return pane, func() []spending.Report {
		mu.Lock()
		defer mu.Unlock()
		return append([]spending.Report{}, got...)
	}
}

// answersWith is a turn that says something and used u to say it.
func answersWith(u Usage) turnFunc {
	return func(_ context.Context, _ Request, emit func(Event)) error {
		emit(Event{Kind: EventText, Text: "done"})
		emit(Event{Kind: EventUsage, Usage: u})
		return nil
	}
}

// Every call's tokens reach the application, so the pane header can show what
// the conversation has cost: priced from the table, with the date the price
// was read, and sent as one call's worth for the application to add up.
func TestEachCallsUsageIsReported(t *testing.T) {
	pane, reports := usageSink(t)
	first := Usage{In: 1_000_000, Out: 100_000, CacheRead: 400_000}
	wire := &scriptedWire{turns: []turnFunc{answersWith(first), answersWith(Usage{In: 20, Out: 5})}}
	run(t, Options{
		Agent: "anthropic", Model: "claude-opus-5", Session: "pane-7",
		API: pane.srv.BaseURL(), Token: pane.srv.Token(),
	}, "one\ntwo\n", wire)

	got := reports()
	if len(got) != 2 {
		t.Fatalf("got %d usage reports, want one per call: %+v", len(got), got)
	}
	r := got[0]
	if r.Pane != "pane-7" || r.Model != "claude-opus-5" ||
		r.Tokens.In != 1_000_000 || r.Tokens.Out != 100_000 || r.Tokens.CacheRead != 400_000 {
		t.Errorf("first report = %+v, want pane-7's claude-opus-5 call with its tokens", r)
	}
	want, _ := cost("claude-opus-5", first)
	if !r.Cost.Known || r.Cost.USD != want || r.Cost.Source != "table" || r.Cost.Checked != pricesChecked {
		t.Errorf("first cost = %+v, want $%v from the table checked %s", r.Cost, want, pricesChecked)
	}
	if r.Cumulative {
		t.Error("a call's usage was sent as a running total, so the application would not add it up")
	}
	if got[1].Tokens.In != 20 || got[1].Tokens.Out != 5 {
		t.Errorf("second report = %+v, want the second call's own tokens", got[1])
	}
}

// A model with no price here -- a local one, or another vendor's -- is still
// counted, in tokens, and its cost is said to be unknown rather than zero.
func TestUnpricedCallsAreReportedInTokens(t *testing.T) {
	pane, reports := usageSink(t)
	wire := &scriptedWire{turns: []turnFunc{answersWith(Usage{In: 300, Out: 40})}}
	run(t, Options{
		Agent: "ollama", Model: "llama3", Session: "pane-8",
		API: pane.srv.BaseURL(), Token: pane.srv.Token(),
	}, "hello\n", wire)

	got := reports()
	if len(got) != 1 || got[0].Tokens.In != 300 || got[0].Cost.Known || got[0].Cost.USD != 0 {
		t.Errorf("got %+v, want llama3's 300 tokens in with no price", got)
	}
}

// Somebody can run `flockdeck chat` in a plain terminal, with no application to
// report to: nothing is sent, and nothing fails.
func TestUsageWithNowhereToReportIsSkipped(t *testing.T) {
	r := newReporter("", "", "pane", "")
	called := false
	r.report = func(string, string, spending.Report, time.Duration) error { called = true; return nil }
	r.usage("claude-opus-5", Usage{In: 1})
	if called {
		t.Error("a usage report was sent with no address to send it to")
	}
}

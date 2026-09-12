package hooks

import (
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/spend"
)

// A report reaches the handler whole, windows and all.
func TestUsageReportIsDelivered(t *testing.T) {
	srv, _ := newServer(t)
	got := make(chan spend.Report, 1)
	srv.SetUsageHandler(func(r spend.Report) { got <- r })

	resets := time.Date(2026, 9, 12, 16, 40, 0, 0, time.UTC)
	sent := spend.Report{
		Pane: "pane-1", Conversation: "conv", Model: "claude-opus-5", Account: "claude:/c",
		Tokens: spend.Tokens{In: 10, Out: 3}, Cost: spend.Cost{USD: 0.25, Known: true, Source: "agent"},
		Cumulative: true,
		Windows:    []spend.Window{{Account: "claude:/c", Name: "five_hour", Used: 72, Percent: true, ResetsAt: resets}},
	}
	if err := Report(srv.UsageEndpoint(), srv.Token(), sent, time.Second); err != nil {
		t.Fatalf("report: %v", err)
	}
	select {
	case r := <-got:
		if r.Pane != "pane-1" || r.Tokens.In != 10 || !r.Cost.Known || !r.Cumulative ||
			len(r.Windows) != 1 || !r.Windows[0].ResetsAt.Equal(resets) || r.Windows[0].Used != 72 {
			t.Errorf("got %+v, want what was sent", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the report never reached the handler")
	}
}

// Any local process can reach the port, and a figure it made up would be shown
// as though the pane's agent had said it; so a report is held to the same
// secret as a lifecycle event, and a refused one says so.
func TestUsageReportWithTheWrongTokenIsRefused(t *testing.T) {
	srv, _ := newServer(t)
	got := make(chan spend.Report, 1)
	srv.SetUsageHandler(func(r spend.Report) { got <- r })

	if err := Report(srv.UsageEndpoint(), "not-the-token", spend.Report{Pane: "pane-1"}, time.Second); err == nil {
		t.Error("a refused report said it was delivered")
	}
	select {
	case r := <-got:
		t.Fatalf("a report with the wrong token was accepted: %+v", r)
	case <-time.After(300 * time.Millisecond):
	}
}

// A Flockdeck that has gone costs the reporter an error, promptly, and never
// holds up the agent it is reporting for.
func TestUsageReportToAClosedServerReturns(t *testing.T) {
	srv, _ := newServer(t)
	endpoint, token := srv.UsageEndpoint(), srv.Token()
	_ = srv.Close()

	done := make(chan error, 1)
	go func() { done <- Report(endpoint, token, spend.Report{Pane: "p"}, time.Second) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Report blocked against a closed server")
	}
}

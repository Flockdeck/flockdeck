package server

import (
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/spend"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// usageRuntime is a pane's own runtime with what an ACP agent's usage_update
// said added, which is all the spend view reads of a structured pane.
type usageRuntime struct {
	workspace.PaneRuntime
	c    spend.Context
	said bool
}

func (u usageRuntime) ReportedSpend() (spend.Context, bool) { return u.c, u.said }

func (e *graphEnv) reportUsage(t *testing.T, id string, c spend.Context, said bool) {
	t.Helper()
	ask(e.srv, func() bool {
		p := e.ws.Pane(id)
		if !e.ws.SetRuntime(id, usageRuntime{PaneRuntime: e.ws.Runtime(p), c: c, said: said}) {
			t.Errorf("pane %s not found", id)
		}
		return true
	})
}

func (e *graphEnv) terminalSpend(id string, usd float64) {
	e.srv.book.Add(spend.Report{
		Pane: id, Cumulative: true,
		Tokens: spend.Tokens{In: 100, Out: 50},
		Cost:   spend.Cost{USD: usd, Known: true, Source: "agent"},
	}, time.Now())
}

// A team of terminal panes only: no partial reporter, the flag stays off.
func TestRollupOfTerminalPanesIsNotMarkedAsIncludingPartial(t *testing.T) {
	e := newGraphEnv(t)
	e.terminalSpend(e.boss, 1.25)
	tm := e.snapshotGraph(t).Teams[0]
	if tm.SpendIncludesPartial || tm.SpendUSD != 1.25 {
		t.Errorf("spend %v includesPartial %v, want 1.25 and no flag", tm.SpendUSD, tm.SpendIncludesPartial)
	}
}

// A structured pane's cost joins the dollars, and the team says it includes
// panes that report partly. Panes with no priced figure still make it a floor.
func TestRollupAddsAStructuredPanesCostAndSaysItIsPartial(t *testing.T) {
	e := newGraphEnv(t)
	e.terminalSpend(e.boss, 1.25)
	e.reportUsage(t, e.dev, spend.Context{Used: 1234, Size: 200000, Cost: 0.75, Currency: "USD"}, true)

	tm := e.snapshotGraph(t).Teams[0]
	if tm.SpendUSD != 2 || !tm.SpendIncludesPartial {
		t.Errorf("spend %v includesPartial %v, want 2 and the flag", tm.SpendUSD, tm.SpendIncludesPartial)
	}
	if !tm.SpendPartial {
		t.Error("the other panes report none, so the total is still a floor")
	}
}

// A structured agent that prices nothing (a free tier reports 0) still marks the
// team, adds no dollars, and no cost is made up from the context figure.
func TestRollupOfAStructuredPaneWithNoCost(t *testing.T) {
	e := newGraphEnv(t)
	e.reportUsage(t, e.dev, spend.Context{Used: 5000, Size: 100000, Cost: 0, Currency: "USD"}, true)
	tm := e.snapshotGraph(t).Teams[0]
	if tm.SpendUSD != 0 || !tm.SpendIncludesPartial || !tm.SpendPartial {
		t.Errorf("spend %v includesPartial %v partial %v", tm.SpendUSD, tm.SpendIncludesPartial, tm.SpendPartial)
	}
}

// An agent that has not sent a usage_update yet is not counted as partial: it
// reports nothing at all, as before.
func TestRollupIgnoresAStructuredPaneThatHasNotReported(t *testing.T) {
	e := newGraphEnv(t)
	e.reportUsage(t, e.dev, spend.Context{}, false)
	if tm := e.snapshotGraph(t).Teams[0]; tm.SpendIncludesPartial {
		t.Error("a pane that said nothing marks the team partial")
	}
}

// The pane header's view carries the context figures and the partial mark, and
// follows a size change.
func TestPaneSpendOfAStructuredPane(t *testing.T) {
	e := newGraphEnv(t)
	p := panePtr(e, e.dev)
	e.reportUsage(t, e.dev, spend.Context{Used: 100, Size: 1000, Cost: 0.01, Currency: "USD"}, true)
	v := e.srv.paneSpend(e.dev, e.ws.Runtime(p), time.Now())
	if v == nil || v.ContextUsed != 100 || v.ContextSize != 1000 || v.USD != 0.01 || !v.Partial {
		t.Fatalf("view = %+v", v)
	}
	e.reportUsage(t, e.dev, spend.Context{Used: 100, Size: 400, Cost: 0.01, Currency: "USD"}, true)
	v = e.srv.paneSpend(e.dev, e.ws.Runtime(p), time.Now())
	if v == nil || v.ContextSize != 400 {
		t.Errorf("after a size change: %+v", v)
	}
	// A new session starts again: a report of nothing used and no cost.
	e.reportUsage(t, e.dev, spend.Context{Used: 0, Size: 400}, true)
	v = e.srv.paneSpend(e.dev, e.ws.Runtime(p), time.Now())
	if v == nil || v.ContextUsed != 0 || v.USD != 0 || v.ContextSize != 400 {
		t.Errorf("after a new session: %+v", v)
	}
}

// A terminal pane's view is the book's alone and is not marked partial.
func TestPaneSpendOfATerminalPaneIsNotPartial(t *testing.T) {
	e := newGraphEnv(t)
	e.terminalSpend(e.boss, 2)
	v := e.srv.paneSpend(e.boss, e.ws.Runtime(panePtr(e, e.boss)), time.Now())
	if v == nil || v.USD != 2 || v.Partial || v.ContextUsed != 0 {
		t.Errorf("view = %+v", v)
	}
}

func panePtr(e *graphEnv, id string) *workspace.Pane {
	var p *workspace.Pane
	ask(e.srv, func() bool { p = e.ws.Pane(id); return true })
	return p
}

// The window's state carries a structured pane's context figures in the pane's
// spend, marked partial.
func TestStructuredPanesSpendReachesTheStateSnapshot(t *testing.T) {
	e := newGraphEnv(t)
	e.reportUsage(t, e.dev, spend.Context{Used: 1234, Size: 200000, Cost: 0.0042, Currency: "USD"}, true)
	msg, ok := ask(e.srv, e.srv.snapshot)
	if !ok {
		t.Fatal("no snapshot")
	}
	v := msg.Panes[e.dev].Spend
	if v == nil || v.ContextUsed != 1234 || v.ContextSize != 200000 || v.USD != 0.0042 || !v.Partial {
		t.Errorf("the pane's spend is %+v", v)
	}
}

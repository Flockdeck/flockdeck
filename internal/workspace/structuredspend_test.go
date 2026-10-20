package workspace

import (
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
)

// A structured pane says what its agent reported through usage_update: context
// used, its size and a cost. A pane whose agent reported none has nothing to
// say. The fake agent's "tool" turn ends with 1234 of 200000 and $0.0042.
func TestAStructuredPaneReportsItsAgentsUsage(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	plain := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, plain, session.StatusIdle)
	rep, ok := ws.Runtime(ws.Pane(plain)).(SpendReporter)
	if !ok {
		t.Fatal("a structured runtime is not a SpendReporter")
	}
	if c, said := rep.ReportedSpend(); said {
		t.Errorf("an agent that reported nothing has a report: %+v", c)
	}

	tool := spawnStructured(t, ws, lead, "tool")
	rep = ws.Runtime(ws.Pane(tool)).(SpendReporter)
	deadline := time.Now().Add(30 * time.Second)
	for {
		c, said := rep.ReportedSpend()
		if said {
			if c.Used != 1234 || c.Size != 200000 || c.Cost != 0.0042 || c.Currency != "USD" {
				t.Errorf("report = %+v", c)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the agent's usage_update never arrived")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// A terminal pane is not one.
func TestATerminalPaneIsNoSpendReporter(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	if _, ok := ws.Runtime(ws.Pane(lead)).(SpendReporter); ok {
		t.Error("a terminal runtime claims to report through usage_update")
	}
}

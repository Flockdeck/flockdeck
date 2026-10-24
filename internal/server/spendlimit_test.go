package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/team"
)

// A team under its limit is told nothing, at 80 percent its needs list says so,
// and once it is spent no helper starts. Nothing running is stopped.
func TestTeamSpendLimitWarnsAndRefusesNewHelpers(t *testing.T) {
	e := newGraphEnv(t)
	setLimit := func(usd float64) {
		t.Helper()
		e.srv.setTeamSpendLimit(e.c, command{Cmd: "teamSpendLimit", Root: e.root, Limit: usd})
	}
	spendItem := func(g graphMsg) *team.NeedsItem {
		for _, it := range g.Teams[0].Needs {
			if it.Kind == team.NeedSpend {
				it := it
				return &it
			}
		}
		return nil
	}
	room := func() error {
		err, _ := ask(e.srv, func() error { return e.srv.spendRoom(e.boss) })
		return err
	}

	e.terminalSpend(e.boss, 7)
	if g := e.snapshotGraph(t); g.Teams[0].LimitUSD != 0 || spendItem(g) != nil {
		t.Errorf("no limit set, but the team has %+v", g.Teams[0])
	}

	setLimit(10)
	g := e.snapshotGraph(t)
	if g.Teams[0].LimitUSD != 10 || spendItem(g) != nil {
		t.Errorf("$7 of $10: limit %v, spend item %+v", g.Teams[0].LimitUSD, spendItem(g))
	}
	if err := room(); err != nil {
		t.Errorf("a start under the limit was refused: %v", err)
	}

	e.terminalSpend(e.dev, 1.5)
	g = e.snapshotGraph(t)
	it := spendItem(g)
	if it == nil || it.Pane != e.boss || !strings.Contains(it.Text, "$8.50 of the $10.00 limit") || !strings.Contains(it.Text, "85%") {
		t.Fatalf("at 85 percent the spend item is %+v", it)
	}
	if err := room(); err != nil {
		t.Errorf("a start at 85 percent was refused: %v", err)
	}

	e.terminalSpend(e.eyes, 2)
	g = e.snapshotGraph(t)
	if it := spendItem(g); it == nil || !strings.Contains(it.Text, "over the $10.00 limit") {
		t.Errorf("over the limit the spend item is %+v", it)
	}
	err := room()
	if err == nil || !strings.Contains(err.Error(), "no new helper starts") || !strings.Contains(err.Error(), "not stopped") {
		t.Errorf("a start over the limit: %v", err)
	}
	// Every pane of the team is still there.
	if len(g.Nodes) < 4 {
		t.Errorf("%d nodes", len(g.Nodes))
	}

	// Raising or clearing the limit lets starts through again.
	setLimit(100)
	if err := room(); err != nil {
		t.Errorf("after raising the limit: %v", err)
	}
	setLimit(0)
	g = e.snapshotGraph(t)
	if g.Teams[0].LimitUSD != 0 || spendItem(g) != nil {
		t.Errorf("after clearing: %+v", g.Teams[0])
	}
	raw, _ := json.Marshal(g.Teams[0])
	if strings.Contains(string(raw), "limitUSD") {
		t.Errorf("a team with no limit sends it: %s", raw)
	}
}

// Bad figures are refused and the limit stays as it was. A viewer cannot set
// one, and the relay form carries neither the limit nor the item.
func TestTeamSpendLimitIsBoundedAndStaysOnTheDesk(t *testing.T) {
	e := newGraphEnv(t)
	e.srv.setTeamSpendLimit(e.c, command{Cmd: "teamSpendLimit", Root: e.root, Limit: 5})
	for _, bad := range []float64{-1, 2_000_000} {
		e.srv.setTeamSpendLimit(e.c, command{Cmd: "teamSpendLimit", Root: e.root, Limit: bad})
	}
	if g := e.snapshotGraph(t); g.Teams[0].LimitUSD != 5 {
		t.Errorf("limit = %v after refused figures, want 5", g.Teams[0].LimitUSD)
	}
	if commandAccess["teamSpendLimit"] != accessFull {
		t.Error("teamSpendLimit is not accessFull")
	}

	g := graphMsg{
		Type: "graph", Project: "p",
		Teams: []graphTeamView{{ID: "m", Manager: "m", Members: []string{"m"}, LimitUSD: 10, SpendUSD: 9, Needs: []team.NeedsItem{
			{ID: "spend:m", Kind: team.NeedSpend, Pane: "m", Text: "$9.00 of the $10.00 limit spent (90%)"},
		}}},
		Nodes: []graphNodeView{{Pane: "m", Team: "m", Activity: "working", Status: "working"}},
	}
	out, err := json.Marshal(relayProjectFromGraph(g))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"limit", "spend", "$9", "10.00"} {
		if strings.Contains(string(out), leak) {
			t.Errorf("the relay form has %q:\n%s", leak, out)
		}
	}
}

package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/spend"
	"github.com/jmwri/flockdeck/internal/store"
)

// What a pane's agent reports it has spent reaches that pane's header, through
// the hook server the pane was given; a report for a pane that has gone is
// kept nowhere.
func TestReportedSpendReachesThePaneHeader(t *testing.T) {
	srv, ws := newTestServer(t)
	pane := srv.firstPaneID(t)
	hookSrv := ws.HookServer()

	report := func(r spend.Report) {
		t.Helper()
		if err := hooks.Report(hookSrv.UsageEndpoint(), hookSrv.Token(), r, time.Second); err != nil {
			t.Fatalf("report: %v", err)
		}
	}
	report(spend.Report{Pane: pane, Model: "claude-opus-5", Tokens: spend.Tokens{In: 1000, Out: 50},
		Cost: spend.Cost{USD: 0.0123, Known: true, Source: "table", Checked: "2026-06-24"}})
	report(spend.Report{Pane: "gone", Tokens: spend.Tokens{In: 1}})

	snap := func() stateMsg {
		t.Helper()
		msg, ok := ask(srv, srv.snapshot)
		if !ok {
			t.Fatal("no snapshot")
		}
		return msg
	}
	v := snap().Panes[pane].Spend
	if v == nil || v.USD != 0.0123 || v.Tokens != 1050 || v.Source != "table" || v.Checked != "2026-06-24" {
		t.Fatalf("the header's spend is %+v, want $0.0123 over 1050 tokens from the table", v)
	}
	if srv.book.Pane("gone", time.Now()) != nil {
		t.Error("a report for a pane that does not exist was kept")
	}

	// Nothing new makes nothing different, so the state is not sent again. Only
	// the spend is compared: the pane is a real shell, and its status can move
	// on from starting between the two.
	a, _ := json.Marshal(snap().Panes[pane].Spend)
	b, _ := json.Marshal(snap().Panes[pane].Spend)
	if string(a) != string(b) {
		t.Errorf("two snapshots with nothing new between them differ:\n%s\n%s", a, b)
	}
}

// When a Claude pane's status line goes through Flockdeck is chosen in the
// window and kept. By default -- only where the user has a status line of
// their own -- it is kept as nothing, so prefs.json written before there was
// a choice reads the same, and a mode the panes do not know is refused.
func TestTheStatusLinePreferenceIsKept(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	sendCmd(t, conn, command{Cmd: "statusLine", Text: "on"})
	if got := nextPrefs(t, conn, func(p store.Prefs) bool { return p.Spend.StatusLine != "" }); got.Spend.StatusLine != "on" {
		t.Fatalf("the status line setting pushed to the windows is %q, want on", got.Spend.StatusLine)
	}
	if saved := store.LoadPrefs(); saved.Spend.StatusLine != "on" {
		t.Errorf("the status line setting did not reach the disk: %q", saved.Spend.StatusLine)
	}

	// Refused: nothing is pushed for it, so the next push is the one below.
	sendCmd(t, conn, command{Cmd: "statusLine", Text: "sometimes"})
	sendCmd(t, conn, command{Cmd: "statusLine", Text: "auto"})
	got := nextPrefs(t, conn, func(p store.Prefs) bool { return p.Spend.StatusLine != "on" })
	if got.Spend.StatusLine != "" {
		t.Fatalf("the default is kept as %q, want nothing", got.Spend.StatusLine)
	}
	if saved := store.LoadPrefs(); saved.Spend.StatusLine != "" {
		t.Errorf("going back to the default did not reach the disk: %q", saved.Spend.StatusLine)
	}
}

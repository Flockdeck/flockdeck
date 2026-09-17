package workspace

import (
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/layout"
	"github.com/jmwri/flockdeck/internal/session"
)

// fanoutTestWorkspace builds a bare workspace with no sessions behind its
// panes, the way benchWorkspace does: a pane with no Sess reads as
// StatusExited (see Pane.Status), which is all FanoutTabSettled needs to
// tell a pane that is not working or starting from one that is, without a
// real agent process behind it.
func fanoutTestWorkspace(panes ...*Pane) *Workspace {
	w := &Workspace{panes: map[string]*Pane{}}
	for _, p := range panes {
		w.panes[p.ID] = p
	}
	return w
}

func agentPane(id string) *Pane { return &Pane{ID: id, Kind: session.KindClaude, Name: id} }

func gridTab(id string, delegated bool, paneIDs ...string) *Tab {
	return &Tab{ID: id, Delegated: delegated, Tree: layout.Grid(paneIDs)}
}

// TestFanoutTabSettledMatchesTheLiveCardsOwnRules covers every rule the
// webui's tabSettleInfo applies to collapse a settled fan-out into its
// summary card, ported here so a tab closing can decide, the same way,
// whether it has a finished job worth remembering.
func TestFanoutTabSettledMatchesTheLiveCardsOwnRules(t *testing.T) {
	t.Run("two idle agent panes, delegated", func(t *testing.T) {
		w := fanoutTestWorkspace(agentPane("p1"), agentPane("p2"))
		tab := gridTab("t1", true, "p1", "p2")
		if !w.FanoutTabSettled(tab) {
			t.Error("a delegated tab of two idle agents was not read as settled")
		}
	})

	t.Run("not delegated", func(t *testing.T) {
		w := fanoutTestWorkspace(agentPane("p1"), agentPane("p2"))
		tab := gridTab("t1", false, "p1", "p2")
		if w.FanoutTabSettled(tab) {
			t.Error("an ordinary tab built by hand was read as a settled fan-out")
		}
	})

	t.Run("only one pane", func(t *testing.T) {
		w := fanoutTestWorkspace(agentPane("p1"))
		tab := gridTab("t1", true, "p1")
		if w.FanoutTabSettled(tab) {
			t.Error("a lone helper's tab was read as a settled fan-out")
		}
	})

	t.Run("a shell among them", func(t *testing.T) {
		shell := agentPane("p2")
		shell.Kind = session.KindShell
		w := fanoutTestWorkspace(agentPane("p1"), shell)
		tab := gridTab("t1", true, "p1", "p2")
		if w.FanoutTabSettled(tab) {
			t.Error("a tab holding a shell pane was read as a settled fan-out")
		}
	})

	t.Run("a helper split into its own manager's tab", func(t *testing.T) {
		helper := agentPane("p2")
		helper.Parent = "p1"
		w := fanoutTestWorkspace(agentPane("p1"), helper)
		tab := gridTab("t1", true, "p1", "p2")
		if w.FanoutTabSettled(tab) {
			t.Error("a tab holding a helper's own parent conversation was read as a settled fan-out")
		}
	})

	t.Run("a pane no longer in the workspace", func(t *testing.T) {
		w := fanoutTestWorkspace(agentPane("p1"))
		tab := gridTab("t1", true, "p1", "p2")
		if w.FanoutTabSettled(tab) {
			t.Error("a tab naming a pane that is already gone was read as settled")
		}
	})

	t.Run("nil tab", func(t *testing.T) {
		w := fanoutTestWorkspace()
		if w.FanoutTabSettled(nil) {
			t.Error("a nil tab was read as a settled fan-out")
		}
	})

	t.Run("still working", func(t *testing.T) {
		isolateConfig(t)
		writeAgentsFile(t, nil)
		ws := newTestWorkspace(t, t.TempDir())

		first, err := ws.Spawn("", SpawnOptions{Task: "one", Kind: session.KindClaude, Agent: "gocli"})
		if err != nil {
			t.Fatalf("spawn: %v", err)
		}
		tabID := ws.TabIDOf(first)
		second, err := ws.Spawn("", SpawnOptions{Task: "two", Kind: session.KindClaude, Agent: "gocli", Tab: tabID})
		if err != nil {
			t.Fatalf("spawn: %v", err)
		}
		p1, p2 := ws.Pane(first), ws.Pane(second)
		if p1.Sess == nil || p2.Sess == nil {
			t.Fatal("a pane never started")
		}
		p1.Sess.SetStatus(session.StatusIdle, "")
		p2.Sess.SetStatus(session.StatusWorking, "")

		tab := ws.Tab(tabID)
		if ws.FanoutTabSettled(tab) {
			t.Error("a tab with an agent still working was read as settled")
		}

		p2.Sess.SetStatus(session.StatusIdle, "")
		if !ws.FanoutTabSettled(tab) {
			t.Error("a tab with nothing left working was not read as settled once it caught up")
		}
	})
}

// TestAddFanoutHistoryOrdersAndCaps covers the bookkeeping AddFanoutHistory
// does for a project's history: newest first, per project, and bounded so a
// long-running project does not grow it without bound.
func TestAddFanoutHistoryOrdersAndCaps(t *testing.T) {
	w := &Workspace{}
	if got := w.FanoutHistory("/proj"); len(got) != 0 {
		t.Fatalf("a project with no history reported %d jobs", len(got))
	}

	w.AddFanoutHistory("/proj", FanoutJob{ID: "j1", Title: "first"})
	w.AddFanoutHistory("/proj", FanoutJob{ID: "j2", Title: "second"})
	got := w.FanoutHistory("/proj")
	if len(got) != 2 || got[0].ID != "j2" || got[1].ID != "j1" {
		t.Fatalf("got %+v, want j2 then j1", got)
	}

	// Another project's history is kept apart.
	w.AddFanoutHistory("/other", FanoutJob{ID: "o1"})
	if got := w.FanoutHistory("/proj"); len(got) != 2 {
		t.Fatalf("recording another project's job changed this one's history: %+v", got)
	}

	// Mutating what was handed back does not reach the workspace's own copy.
	got[0].Title = "tampered"
	if again := w.FanoutHistory("/proj"); again[0].Title != "second" {
		t.Error("FanoutHistory handed out its own slice rather than a copy")
	}

	for i := 0; i < fanoutHistoryLimit+10; i++ {
		w.AddFanoutHistory("/capped", FanoutJob{ID: string(rune('a' + i%26)), At: time.Now()})
	}
	if got := w.FanoutHistory("/capped"); len(got) != fanoutHistoryLimit {
		t.Errorf("history held %d jobs, want it capped at %d", len(got), fanoutHistoryLimit)
	}
}

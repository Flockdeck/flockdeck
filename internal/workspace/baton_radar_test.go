package workspace

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/radar"
	"github.com/jmwri/flockdeck/internal/session"
)

// A pane started from a baton keeps its BatonID while the conflict radar
// refreshes over it, the radar's Conflicts are not saved with the layout, and
// BatonID comes back after a restore while the radar finds the pair again.
func TestABatonPaneKeepsItsBatonThroughARadarRefreshAndARestore(t *testing.T) {
	batonAgents(t)
	r := newRadarRig(t)
	// Every checkout is dirty and touches shared.go, and any two conflict on it.
	gitStatus = func(string, time.Duration) (gitx.Status, error) {
		return gitx.Status{Branch: "work", Head: "abc1234", Dirty: 1}, nil
	}
	radarSnapshot = func(_ context.Context, dir string, _ *gitx.Scratch, _ string, _ bool) (gitx.Snap, error) {
		return gitx.Snap{Commit: "c-" + dir, Tree: "t-" + dir, Head: "h-" + dir, Paths: []string{"shared.go"}}, nil
	}
	radarEngine = func() *radar.Engine {
		return radar.NewEngineWith(radar.Options{
			MinGap: time.Second,
			Now:    func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
			Predict: func(context.Context, string, *gitx.Scratch, string, string) ([]string, bool, error) {
				return []string{"shared.go"}, false, nil
			},
		})
	}

	root, other := t.TempDir(), t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	parent := ws.CurrentTab().Focus
	b := testBaton()
	bp := prepared(t, ws, root, b, "do the refresh part")
	id, err := ws.Spawn(parent, SpawnOptions{Task: "do the refresh part", Agent: "goprompt", Baton: &bp})
	if err != nil {
		t.Fatal(err)
	}
	waitExited(t, ws.Pane(id).Sess)
	ws.NewTab(session.KindShell, other, "neighbour")
	neighbour := ws.CurrentTab().Focus

	r.refresh(ws)
	if got := conflictsOf(ws, id); len(got) != 0 {
		t.Fatalf("the first refresh showed %v, want nothing until a pair is seen twice", got)
	}
	r.refresh(ws)
	want := []PaneConflict{{With: neighbour, Paths: []string{"shared.go"}}}
	if got := conflictsOf(ws, id); !reflect.DeepEqual(got, want) {
		t.Fatalf("baton pane conflicts = %v, want %v", got, want)
	}
	if ws.Pane(id).BatonID != b.ID {
		t.Errorf("BatonID = %q after the radar refreshed, want %q", ws.Pane(id).BatonID, b.ID)
	}

	if err := ws.SaveAll(); err != nil {
		t.Fatal(err)
	}
	ws.Close()
	restored := newTestWorkspace(t, root)
	if ok, err := restored.Restore(); err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	p := restored.Pane(id)
	if p == nil {
		t.Fatal("the baton pane did not come back")
	}
	if p.BatonID != b.ID {
		t.Errorf("restored BatonID = %q, want %q", p.BatonID, b.ID)
	}
	if len(p.Conflicts) != 0 {
		t.Errorf("restored pane has conflicts %v, want none until the radar looks again", p.Conflicts)
	}
	r.refresh(restored)
	r.refresh(restored)
	if got := conflictsOf(restored, id); len(got) != 1 || got[0].With != neighbour {
		t.Errorf("after restore and two refreshes: %v", got)
	}
	if restored.Pane(id).BatonID != b.ID {
		t.Errorf("BatonID lost by the radar after restore: %q", restored.Pane(id).BatonID)
	}
}

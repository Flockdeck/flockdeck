package workspace

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/jmwri/perch/internal/layout"
	"github.com/jmwri/perch/internal/session"
)

// benchWorkspace builds workspace state by hand, with no sessions behind the
// panes. It is only for measuring the walks over tabs and panes; anything that
// needs a live process builds a workspace the ordinary way.
func benchWorkspace(projects, tabsPer, panesPer int) *Workspace {
	w := &Workspace{panes: map[string]*Pane{}, BroadcastSet: map[string]bool{}}
	for i := 0; i < projects; i++ {
		root := filepath.Join(string(filepath.Separator)+"projects", fmt.Sprintf("p%d", i))
		w.openRoots = append(w.openRoots, root)
		for j := 0; j < tabsPer; j++ {
			var tree *layout.Node
			var first string
			for k := 0; k < panesPer; k++ {
				p := &Pane{ID: uuid.NewString(), Kind: session.KindClaude, Cwd: root, Root: root}
				w.panes[p.ID] = p
				if tree == nil {
					tree, first = layout.NewLeaf(p.ID), p.ID
					continue
				}
				tree.Split(first, p.ID, layout.Horizontal)
			}
			w.Tabs = append(w.Tabs, &Tab{
				ID: uuid.NewString(), Root: root, Title: fmt.Sprintf("t%d", j), Tree: tree, Focus: first,
			})
		}
	}
	w.activeRoot = w.openRoots[0]
	w.activeTab = w.Tabs[0].ID
	return w
}

// BenchmarkProjects measures the summary the project switcher is redrawn from,
// which runs on every change in every pane.
func BenchmarkProjects(b *testing.B) {
	w := benchWorkspace(6, 8, 4)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = w.Projects()
	}
}

// TestProjectCountsFollowTheAgentsProject covers the switcher badge for a pane
// borrowed by another project's tab: the project whose work has stopped is the
// one that must be shown as waiting, not the project that lent the tab.
func TestProjectCountsFollowTheAgentsProject(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)
	ws.SplitPaneInProject(layout.Horizontal, session.KindShell, second)

	borrowed := borrowedPane(t, ws, ws.CurrentTab())
	if borrowed == nil {
		t.Fatal("no pane of the second project on the first project's tab")
	}
	if borrowed.Sess == nil {
		t.Fatal("the borrowed pane never started")
	}
	borrowed.Sess.SetStatus(session.StatusWaiting, "")

	for _, p := range ws.Projects() {
		switch {
		case sameDir(p.Root, second):
			if p.Waiting != 1 {
				t.Errorf("the project the waiting agent belongs to reports %d waiting, want 1", p.Waiting)
			}
		case sameDir(p.Root, first):
			if p.Waiting != 0 {
				t.Errorf("the project that only lent a tab reports %d waiting, want 0", p.Waiting)
			}
		}
	}
}

package workspace

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// TestProjectsAppliesNameArchivedAndOrderFromMeta checks that Projects shows
// a custom name and archived flag once ReloadProjectMeta has picked them up
// from the recent list, and that a project given a position by hand sorts
// ahead of one that has not been.
func TestProjectsAppliesNameArchivedAndOrderFromMeta(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)

	if err := store.SetProjectName(second, "Renamed"); err != nil {
		t.Fatalf("set name: %v", err)
	}
	if err := store.SetProjectArchived(second, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	// second was opened after first, so without an order first still leads;
	// giving second a position by hand should move it ahead.
	if err := store.ReorderProjects([]string{second, first}); err != nil {
		t.Fatalf("reorder: %v", err)
	}

	// Projects is built from a cache the workspace loaded at startup, so
	// nothing above is seen until it is told to look again.
	before := ws.Projects()
	if before[0].Root != first {
		t.Fatalf("projects before reload = %+v, want %s to still lead", before, first)
	}

	ws.ReloadProjectMeta()
	got := ws.Projects()
	if len(got) != 2 {
		t.Fatalf("projects = %+v, want 2", got)
	}
	if got[0].Root != second {
		t.Errorf("projects[0] = %s, want %s to lead once it has a position by hand", got[0].Root, second)
	}
	var renamed *Project
	for i := range got {
		if got[i].Root == second {
			renamed = &got[i]
		}
	}
	if renamed == nil {
		t.Fatal("second project missing from Projects")
	}
	if renamed.Name != "Renamed" {
		t.Errorf("name = %q, want %q", renamed.Name, "Renamed")
	}
	if !renamed.Archived {
		t.Error("archived project did not report itself archived")
	}
}

package store

import (
	"path/filepath"
	"testing"
)

// TestSetProjectNameShowsInRecentsAndClearsWithEmpty checks a name chosen by
// hand appears in Recents and that renaming to the empty string goes back to
// no name at all, rather than being stored as one.
func TestSetProjectNameShowsInRecentsAndClearsWithEmpty(t *testing.T) {
	isolateConfig(t)
	root := filepath.Join(t.TempDir(), "repo")
	if err := TouchRecent(root); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if err := SetProjectName(root, "  My Repo  "); err != nil {
		t.Fatalf("set name: %v", err)
	}
	list, err := Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	if len(list) != 1 || list[0].Name != "My Repo" {
		t.Fatalf("recents = %+v, want one project named %q", list, "My Repo")
	}

	if err := SetProjectName(root, ""); err != nil {
		t.Fatalf("clear name: %v", err)
	}
	list, err = Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	if len(list) != 1 || list[0].Name != "" {
		t.Fatalf("recents = %+v, want the name cleared", list)
	}
}

// TestSetProjectNameOnAProjectNotYetTouchedAddsIt checks a name chosen for a
// project before it has ever been opened -- so TouchRecent has not put it in
// the list yet -- is not silently lost.
func TestSetProjectNameOnAProjectNotYetTouchedAddsIt(t *testing.T) {
	isolateConfig(t)
	root := filepath.Join(t.TempDir(), "repo")
	if err := SetProjectName(root, "Fresh"); err != nil {
		t.Fatalf("set name: %v", err)
	}
	list, err := Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Fresh" || !sameRoot(list[0].Root, root) {
		t.Fatalf("recents = %+v, want one project named Fresh at %s", list, root)
	}
}

// TestArchiveKeepsAProjectListedButMarked checks archiving is reversible and
// never drops the project from the list -- it only marks it.
func TestArchiveKeepsAProjectListedButMarked(t *testing.T) {
	isolateConfig(t)
	root := filepath.Join(t.TempDir(), "repo")
	if err := TouchRecent(root); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if err := SetProjectArchived(root, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	list, err := Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	if len(list) != 1 || !list[0].Archived {
		t.Fatalf("recents = %+v, want the project archived", list)
	}
	if err := SetProjectArchived(root, false); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	if list, err = Recents(); err != nil {
		t.Fatalf("recents: %v", err)
	} else if len(list) != 1 || list[0].Archived {
		t.Fatalf("recents = %+v, want the project unarchived", list)
	}
}

// TestTouchRecentKeepsNameArchivedAndOrder checks that opening a project
// again -- which TouchRecent records every time a project is opened or
// switched to -- does not wipe out a name, an archived flag or a position
// already recorded for it.
func TestTouchRecentKeepsNameArchivedAndOrder(t *testing.T) {
	isolateConfig(t)
	root := filepath.Join(t.TempDir(), "repo")
	if err := TouchRecent(root); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if err := SetProjectName(root, "Kept"); err != nil {
		t.Fatalf("set name: %v", err)
	}
	if err := SetProjectArchived(root, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if err := ReorderProjects([]string{root}); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	if err := TouchRecent(root); err != nil {
		t.Fatalf("touch again: %v", err)
	}
	list, err := Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Kept" || !list[0].Archived || list[0].Order != 1 {
		t.Fatalf("recents = %+v, want name, archived and order kept across a touch", list)
	}
}

// TestReorderProjectsSortsByHandAheadOfRecency checks that giving projects a
// position by hand puts them ahead of ones that have none, in the order
// given, and that a project left out of the reorder keeps sorting by when it
// was last used.
func TestReorderProjectsSortsByHandAheadOfRecency(t *testing.T) {
	isolateConfig(t)
	base := t.TempDir()
	a, b, c := filepath.Join(base, "a"), filepath.Join(base, "b"), filepath.Join(base, "c")
	// c is used last, so recency alone would put it first.
	if err := TouchRecents(a, b, c); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if err := ReorderProjects([]string{b, a}); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	list, err := Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	var got []string
	for _, p := range list {
		got = append(got, filepath.Base(p.Root))
	}
	if want := []string{"b", "a", "c"}; !equalStrings(got, want) {
		t.Errorf("recents order = %v, want %v", got, want)
	}

	// Moving b and a again -- b to the back of the two -- keeps c, which has
	// no position of its own, sorting after both by recency.
	if err := ReorderProjects([]string{a, b}); err != nil {
		t.Fatalf("reorder again: %v", err)
	}
	if list, err = Recents(); err != nil {
		t.Fatalf("recents: %v", err)
	}
	got = got[:0]
	for _, p := range list {
		got = append(got, filepath.Base(p.Root))
	}
	if want := []string{"a", "b", "c"}; !equalStrings(got, want) {
		t.Errorf("recents order after moving = %v, want %v", got, want)
	}
}

// TestAllProjectMetaOnlyCarriesWhatWasChosen checks AllProjectMeta leaves out
// a project with nothing chosen for it, and that a lookup finds one under
// any spelling of its root MetaKey folds the same way normalizeRoot does.
func TestAllProjectMetaOnlyCarriesWhatWasChosen(t *testing.T) {
	isolateConfig(t)
	base := t.TempDir()
	plain, named := filepath.Join(base, "plain"), filepath.Join(base, "named")
	if err := TouchRecents(plain, named); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if err := SetProjectName(named, "Named"); err != nil {
		t.Fatalf("set name: %v", err)
	}
	meta, err := AllProjectMeta()
	if err != nil {
		t.Fatalf("all project meta: %v", err)
	}
	if _, ok := meta[MetaKey(plain)]; ok {
		t.Errorf("a project with nothing chosen for it was carried in the map")
	}
	got, ok := meta[MetaKey(named)]
	if !ok || got.Name != "Named" {
		t.Fatalf("meta[%s] = %+v, %v, want Name = Named", named, got, ok)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

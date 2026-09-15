package workspace

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/jmwri/flockdeck/internal/store"
)

// ProjectGroup is a named collection of one or more repo roots that the
// switcher, the command palette and an agent's own briefing treat as a
// single project. Every open root belongs to exactly one group; a project
// nobody has grouped is a group of one, created automatically the moment
// its root is opened (see ensureGroup) -- which is what makes this
// additive: a user who never groups anything sees the same switcher, the
// same names and the same behaviour this always had.
//
// A repo root stays the unit git operations, the default working
// directory and per-directory saved layouts key off -- none of that
// changes. A ProjectGroup is Flockdeck-side metadata layered above it, and
// could be deleted with no effect on any of the repos it names. See
// store.ProjectGroup, which this is persisted as.
type ProjectGroup struct {
	ID string
	// Name is user-editable; empty means the switcher derives one the way
	// it derives every ungrouped project's name, via projectNames -- see
	// groupDisplayNames.
	Name string
	// Roots are the member repos, in the order they were added.
	Roots []string
	// Primary is which member is "home": the default cwd for a new tab, the
	// default worktree-suggestion base, and the tie-breaker for naming.
	Primary string
}

// hasRoot reports whether root is a member, comparing the way sameDir does.
func (g *ProjectGroup) hasRoot(root string) bool {
	for _, r := range g.Roots {
		if sameDir(r, root) {
			return true
		}
	}
	return false
}

// loadSavedGroups reads groups.json once, at startup, so ensureGroup can
// cross-reference roots against it as each is opened over the run -- by a
// path on the command line, by RestoreSession reopening what was open
// before, or by a tab's own borrowed pane bringing its project with it.
// A damaged or absent file is not fatal: every root simply starts as its
// own group, which is a fresh install's behaviour anyway.
func loadSavedGroups() []store.ProjectGroup {
	groups, _ := store.LoadGroups()
	return groups
}

// savedGroupFor returns the recorded grouping root was last saved as a
// member of, if any.
func (w *Workspace) savedGroupFor(root string) (store.ProjectGroup, bool) {
	for _, g := range w.savedGroups {
		for _, r := range g.Roots {
			if sameDir(r, root) {
				return g, true
			}
		}
	}
	return store.ProjectGroup{}, false
}

// ensureGroup returns the group root belongs to, creating one the first
// time root is seen this run.
//
// A root already grouped, live, is returned as is. Failing that, a
// grouping recorded before this run is honoured: if another of its members
// is already open, root joins that live group; otherwise root starts a new
// live group under the saved group's own id, name and primary, ready for
// the rest of its members to join as they are opened too -- which is how a
// grouping made last run survives RestoreSession opening its members one
// at a time rather than all at once. A root in neither case starts a plain
// singleton group of its own, named nothing (see groupDisplayNames, which
// derives one the way projectNames always has).
func (w *Workspace) ensureGroup(root string) *ProjectGroup {
	if id, ok := w.rootGroup[root]; ok {
		if g := w.groups[id]; g != nil {
			return g
		}
	}
	if saved, ok := w.savedGroupFor(root); ok {
		for _, other := range saved.Roots {
			if sameDir(other, root) {
				continue
			}
			if id, ok := w.rootGroup[other]; ok {
				if g := w.groups[id]; g != nil {
					w.addToGroup(g, root)
					return g
				}
			}
		}
		// The saved Primary may name a member that never rejoins this run,
		// so the live group starts on the member actually in front of it,
		// root, rather than risk Primary naming a repo that is not open.
		g := &ProjectGroup{ID: saved.ID, Name: saved.Name, Primary: root, Roots: []string{root}}
		w.putGroup(g)
		return g
	}
	g := &ProjectGroup{ID: uuid.NewString(), Roots: []string{root}, Primary: root}
	w.putGroup(g)
	return g
}

// putGroup registers a group under its id and every member it already
// names, creating the lookup maps on first use.
func (w *Workspace) putGroup(g *ProjectGroup) {
	if w.groups == nil {
		w.groups = map[string]*ProjectGroup{}
	}
	if w.rootGroup == nil {
		w.rootGroup = map[string]string{}
	}
	w.groups[g.ID] = g
	for _, r := range g.Roots {
		w.rootGroup[r] = g.ID
	}
}

// addToGroup adds root to an existing live group.
func (w *Workspace) addToGroup(g *ProjectGroup, root string) {
	if !g.hasRoot(root) {
		g.Roots = append(g.Roots, root)
	}
	if w.rootGroup == nil {
		w.rootGroup = map[string]string{}
	}
	w.rootGroup[root] = g.ID
}

// groupOf reports the group a root belongs to, or nil for a root that has
// no live group -- which today means only a root that has never been
// opened, since every open root is put through ensureGroup the moment it
// is opened.
func (w *Workspace) groupOf(root string) *ProjectGroup {
	if root == "" {
		return nil
	}
	id, ok := w.rootGroup[root]
	if !ok {
		// Almost every caller already holds the spelling the root was
		// opened with; a caller that does not -- the way openRootFor's own
		// callers sometimes do not -- is matched by comparing directories.
		for r, gid := range w.rootGroup {
			if sameDir(r, root) {
				id, ok = gid, true
				break
			}
		}
	}
	if !ok {
		return nil
	}
	return w.groups[id]
}

// groupsInOrder returns every live group, ordered by the position of the
// earliest of its members in openRoots -- which is the order Projects()
// returned its entries in before groups existed, so a project nobody has
// grouped still appears exactly where it always did.
func (w *Workspace) groupsInOrder() []*ProjectGroup {
	order := make(map[string]int, len(w.groups))
	for i, r := range w.openRoots {
		id, ok := w.rootGroup[r]
		if !ok {
			continue
		}
		if _, seen := order[id]; !seen {
			order[id] = i
		}
	}
	out := make([]*ProjectGroup, 0, len(w.groups))
	for _, g := range w.groups {
		out = append(out, g)
	}
	sort.SliceStable(out, func(i, j int) bool { return order[out[i].ID] < order[out[j].ID] })
	return out
}

// groupDisplayNames names each group the way projectNames names roots: one
// given a name by hand keeps it outright, and the rest are named after
// their primary repo, growing enough of its path to tell it apart from any
// other unnamed group that would otherwise be called the same thing -- two
// same-named repos each opened as their own project, say.
func groupDisplayNames(groups []*ProjectGroup) map[string]string {
	out := make(map[string]string, len(groups))
	var autoIdx []int
	var autoRoots []string
	for i, g := range groups {
		if g.Name != "" {
			out[g.ID] = g.Name
			continue
		}
		autoIdx = append(autoIdx, i)
		autoRoots = append(autoRoots, g.Primary)
	}
	names := projectNames(autoRoots)
	for i, idx := range autoIdx {
		out[groups[idx].ID] = names[i]
	}
	return out
}

// repoLabel names a single repo the way it would read inside its own
// project's member list: disambiguated against its own group's other
// members (projectNames run over just that group's roots), which is a
// different question from what the whole project is called -- two
// same-named repos grouped together, or a repo and one of its own
// worktrees, need telling apart from each other, not from other projects.
// A root with no live group -- not open -- falls back to its bare
// directory name.
func (w *Workspace) repoLabel(root string) string {
	g := w.groupOf(root)
	if g == nil {
		return filepath.Base(root)
	}
	labels := projectNames(g.Roots)
	for i, r := range g.Roots {
		if sameDir(r, root) {
			return labels[i]
		}
	}
	return filepath.Base(root)
}

// persistGroups writes every live group to groups.json. It is called after
// every grouping change made from the picker or the command palette, the
// same immediacy RenameGroup's store-level counterpart already has for a
// project's own display name.
func (w *Workspace) persistGroups() error {
	out := make([]store.ProjectGroup, 0, len(w.groups))
	for _, g := range w.groups {
		out = append(out, store.ProjectGroup{
			ID: g.ID, Name: g.Name, Primary: g.Primary,
			Roots: append([]string(nil), g.Roots...),
		})
	}
	return store.SaveGroups(out)
}

// resolveGroup finds the live group a caller means by naming any one of
// its members -- every public grouping method is addressed this way, by a
// root string, the same as OpenProject, CloseProject and SelectProject
// are, rather than by a group id nothing outside this package has a use
// for.
func (w *Workspace) resolveGroup(projectRoot string) (*ProjectGroup, error) {
	open, ok := w.openRootFor(projectRoot)
	if !ok {
		return nil, fmt.Errorf("%s is not an open project", filepath.Base(projectRoot))
	}
	g := w.groupOf(open)
	if g == nil {
		return nil, fmt.Errorf("%s is not an open project", filepath.Base(projectRoot))
	}
	return g, nil
}

// noteActiveRoot records the repo just made active as the one to return to
// within its project, mirroring rememberTab: switching to a multi-repo
// project lands on the member last worked in, rather than always its
// Primary. It is called wherever activeRoot is set to a value naming a
// live repo -- OpenProject, SelectProject, SelectTab and landOn, which
// every pane move that can cross projects funnels through.
func (w *Workspace) noteActiveRoot() {
	g := w.groupOf(w.activeRoot)
	if g == nil {
		return
	}
	if w.lastRootInGroup == nil {
		w.lastRootInGroup = map[string]string{}
	}
	w.lastRootInGroup[g.ID] = w.activeRoot
}

// AddRepoToGroup opens path as another repo inside the project named by
// projectRoot -- any of its member roots names it -- rather than as a
// project of its own. The same OpenProject machinery runs underneath: the
// same directory validation, the same restoreProject/first-tab logic --
// only the newly opened root joins projectRoot's group instead of getting
// a singleton one. This is "Add Repo to This Project…" in the command
// palette.
func (w *Workspace) AddRepoToGroup(projectRoot, path string) error {
	g, err := w.resolveGroup(projectRoot)
	if err != nil {
		return err
	}
	if err := w.openProjectInto(path, g); err != nil {
		return err
	}
	return w.persistGroups()
}

// NewGroupFrom merges the groups of several already-open roots into one
// new named project -- "Group open projects…" in the command palette, for
// api and web already open side by side that the user only now wants
// treated as one. It returns the new project's own address: its Primary
// root, the same string every other grouping method takes.
//
// Every root given must already be open; an unopened or unknown one is
// refused rather than silently dropped, since the point is naming exactly
// the projects the user picked. Merging a root whose own group already has
// other members -- itself the result of an earlier grouping -- carries
// those members along rather than orphaning them.
func (w *Workspace) NewGroupFrom(roots []string, name string) (string, error) {
	if len(roots) < 2 {
		return "", errors.New("group at least two open projects together")
	}
	resolved := make([]string, 0, len(roots))
	seenRoot := map[string]bool{}
	for _, r := range roots {
		open, ok := w.openRootFor(r)
		if !ok {
			return "", fmt.Errorf("%s is not an open project", filepath.Base(r))
		}
		if !seenRoot[open] {
			seenRoot[open] = true
			resolved = append(resolved, open)
		}
	}

	g := &ProjectGroup{ID: uuid.NewString(), Name: strings.TrimSpace(name), Primary: resolved[0]}
	merged := map[string]bool{}
	old := map[string]bool{}
	for _, root := range resolved {
		if from := w.groupOf(root); from != nil {
			old[from.ID] = true
			for _, r := range from.Roots {
				if !merged[r] {
					merged[r] = true
					g.Roots = append(g.Roots, r)
				}
			}
			continue
		}
		if !merged[root] {
			merged[root] = true
			g.Roots = append(g.Roots, root)
		}
	}
	for id := range old {
		delete(w.groups, id)
	}
	w.putGroup(g)
	w.noteActiveRoot()
	w.wake()
	if err := w.persistGroups(); err != nil {
		return g.Primary, err
	}
	return g.Primary, nil
}

// RemoveRepoFromGroup splits one member back out of its project into a
// singleton group of its own -- ungrouping it, without closing it or
// touching any of its tabs. It refuses on a group's last member, the same
// shape as CloseProject's refusal to close the last open project: a group
// of zero members is not a project at all.
//
// Closing that one repo's own tabs and panes as well -- "Remove Repo from
// Project" as the worktrees panel or project settings offers it -- is this
// plus CloseProject(root): once root is its own singleton group, closing
// it closes only itself, through the very rescue-or-destroy logic
// CloseProject already runs for any other single-member group, reused
// rather than duplicated.
func (w *Workspace) RemoveRepoFromGroup(root string) error {
	open, ok := w.openRootFor(root)
	if !ok {
		return fmt.Errorf("%s is not an open project", filepath.Base(root))
	}
	g := w.groupOf(open)
	if g == nil || len(g.Roots) <= 1 {
		return errors.New("a project's last repo cannot be removed from it")
	}
	g.Roots = slices.DeleteFunc(g.Roots, func(r string) bool { return sameDir(r, open) })
	if sameDir(g.Primary, open) {
		g.Primary = g.Roots[0]
	}
	delete(w.rootGroup, open)

	ng := &ProjectGroup{ID: uuid.NewString(), Roots: []string{open}, Primary: open}
	w.putGroup(ng)
	w.wake()
	return w.persistGroups()
}

// RenameGroup gives a project a display name chosen by hand, which the
// switcher and the picker show in place of the one projectNames would
// derive for it. An empty name goes back to that -- the same shape as
// clearing a tab's name goes back to its automatic title.
func (w *Workspace) RenameGroup(projectRoot, name string) error {
	g, err := w.resolveGroup(projectRoot)
	if err != nil {
		return err
	}
	g.Name = strings.TrimSpace(name)
	w.wake()
	return w.persistGroups()
}

// ProjectRepos lists every repo in projectRoot's own project, root and
// display label, with projectRoot's own open spelling first: a project
// spans more than one repo once it has been grouped, and this is how the
// worktrees panel finds the rest of them to fan its listing out across. An
// unopened root gets itself back alone.
func (w *Workspace) ProjectRepos(projectRoot string) []RepoSummary {
	open, ok := w.openRootFor(projectRoot)
	if !ok {
		return []RepoSummary{{Root: projectRoot, Name: filepath.Base(projectRoot)}}
	}
	g := w.groupOf(open)
	if g == nil {
		return []RepoSummary{{Root: open, Name: filepath.Base(open)}}
	}
	labels := projectNames(g.Roots)
	out := make([]RepoSummary, 0, len(g.Roots))
	out = append(out, RepoSummary{Root: open, Name: w.repoLabel(open)})
	for i, r := range g.Roots {
		if !sameDir(r, open) {
			out = append(out, RepoSummary{Root: r, Name: labels[i]})
		}
	}
	return out
}

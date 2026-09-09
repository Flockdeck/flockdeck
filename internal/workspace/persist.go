package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"

	"github.com/jmwri/perch/internal/layout"
	"github.com/jmwri/perch/internal/session"
	"github.com/jmwri/perch/internal/store"
)

// SaveAll writes the layout of every open project and the list of which ones
// were open.
//
// Every project is attempted even when one fails, and each failure is named
// after the project it belongs to: this runs on the way out, so the message
// printed to the terminal is the only thing the user gets, and "could not save
// layout" is no help at all when several projects are open.
func (w *Workspace) SaveAll() error {
	var errs []error
	for _, root := range w.openRoots {
		if err := w.SaveProject(root); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", root, err))
		}
	}
	if err := w.SaveSession(); err != nil {
		errs = append(errs, fmt.Errorf("open projects: %w", err))
	}
	return errors.Join(errs...)
}

// SaveProject writes one project's tabs to its own layout file, so projects
// are restored independently of one another.
func (w *Workspace) SaveProject(root string) error {
	tabs := w.tabsOf(root)
	st := &store.State{Active: w.rememberedActive(root, len(tabs))}
	for i, t := range tabs {
		if t.ID == w.activeTab {
			st.Active = i
		}
		st.Tabs = append(st.Tabs, store.Tab{
			Title: t.Title,
			Focus: t.Focus,
			Root:  w.encodeNode(t.Tree, t.Root),
		})
	}
	return store.Save(root, st)
}

// rememberedActive is the tab a project should come back on when none of its
// tabs is the one on screen — which is true of every project but the one being
// looked at when the window closes.
//
// Defaulting to the first tab there would quietly move the user off the tab
// they had been on in every project except the last one they looked at, so the
// index from the previous save is carried forward instead, clamped to the tabs
// that are still there.
func (w *Workspace) rememberedActive(root string, tabs int) int {
	if tabs == 0 {
		return 0
	}
	st, err := store.Load(root)
	if err != nil || st == nil || st.Active <= 0 {
		return 0
	}
	if st.Active >= tabs {
		return tabs - 1
	}
	return st.Active
}

// encodeNode converts a live layout node into its persisted form.
func (w *Workspace) encodeNode(n *layout.Node, tabRoot string) *store.Node {
	if n == nil {
		return nil
	}
	out := &store.Node{Weight: n.Weight}
	if n.IsLeaf() {
		p := w.Pane(n.Pane)
		if p == nil {
			return nil
		}
		out.Pane = &store.Pane{
			ID:   p.ID,
			Kind: kindName(p.Kind),
			Cwd:  p.Cwd,
			Name: p.Name,
			Task: p.Task,
		}
		// Only a pane borrowed from another project needs its project written
		// down; leaving it out otherwise keeps the file as it has always been
		// for the ordinary case, which is every pane in a one-project tab.
		if p.Root != "" && !sameDir(p.Root, tabRoot) {
			out.Pane.Root = p.Root
		}
		return out
	}
	out.Dir = dirName(n.Dir)
	for _, c := range n.Children {
		if enc := w.encodeNode(c, tabRoot); enc != nil {
			out.Children = append(out.Children, enc)
		}
	}
	if len(out.Children) == 0 {
		return nil
	}
	return out
}

// Restore rebuilds the initial project from its saved layout. It reports
// whether anything was restored; when it returns false the caller should
// create a fresh tab.
func (w *Workspace) Restore() (bool, error) {
	return w.restoreProject(w.activeRoot) > 0, nil
}

// restoreProject loads a project's saved tabs and appends them, relaunching
// each pane. Claude panes resume their previous conversation. It returns how
// many tabs were restored.
func (w *Workspace) restoreProject(root string) int {
	st, err := store.Load(root)
	if err != nil || st == nil || len(st.Tabs) == 0 {
		return 0
	}

	branches := branchesOf(paneDirs(st))
	added := 0
	// nearest is the last tab restored at or before the saved active one, so a
	// tab that cannot be restored hands the window over to its neighbour.
	var firstTab, activeTab, nearest string
	for i, t := range st.Tabs {
		tree := w.decodeNode(t.Root, root, branches)
		if tree == nil {
			continue
		}
		tab := &Tab{
			ID:    uuid.NewString(),
			Root:  root,
			Title: t.Title,
			Tree:  tree,
			Focus: t.Focus,
		}
		// The saved focus may name a pane that failed to decode.
		if tab.Tree.Find(tab.Focus) == nil {
			if panes := tab.Tree.Panes(); len(panes) > 0 {
				tab.Focus = panes[0]
			}
		}
		if tab.Title == "" {
			tab.Title = filepath.Base(root)
		}
		tab.AutoTitle = w.stillAutoTitled(tab)
		w.Tabs = append(w.Tabs, tab)
		if firstTab == "" {
			firstTab = tab.ID
		}
		if i == st.Active {
			activeTab = tab.ID
		}
		if i <= st.Active {
			nearest = tab.ID
		}
		added++
	}
	if added == 0 {
		return 0
	}

	// The tab that was on screen may be one of the ones that could not be
	// restored — its panes' directories deleted since, say. Land on the tab
	// beside where it was rather than jumping to the front of the bar, which is
	// what closing a tab does with the same problem.
	if activeTab == "" {
		activeTab = nearest
	}
	if activeTab == "" {
		activeTab = firstTab
	}
	w.activeTab = activeTab
	return added
}

// stillAutoTitled reports whether a restored tab should go on renaming itself
// after the first thing its agent is asked.
//
// The saved layout carries the title but nothing saying who chose it, so the
// test is whether it is still the name the tab would have been given
// automatically: the directory it was opened on. Without this, a tab created
// but never prompted before a restart would keep its directory name forever,
// while an identical tab created after one would rename itself — the same tab
// behaving differently for no reason the user can see.
//
// A tab the user deliberately named after its own directory loses nothing much
// by being renamed once more; a tab named after a prompt keeps that name.
func (w *Workspace) stillAutoTitled(t *Tab) bool {
	if t.Title != filepath.Base(t.Root) {
		return false
	}
	// Only Claude panes report the prompts a rename would come from.
	for _, id := range t.Tree.Panes() {
		if p := w.Pane(id); p != nil && p.Kind == session.KindClaude {
			return true
		}
	}
	return false
}

// ensureProjectOpen opens the project a restored pane belongs to, so a tab
// showing agents from two projects brings both projects back with it.
//
// A project whose directory has since gone is not opened. The pane itself is
// still started in the directory it recorded — losing the project it was
// counted against is no reason to lose the conversation.
func (w *Workspace) ensureProjectOpen(root string) {
	if root == "" {
		return
	}
	if _, ok := w.openRootFor(root); ok {
		return
	}
	if w.opening[root] {
		return
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return
	}
	if w.opening == nil {
		w.opening = make(map[string]bool)
	}
	w.opening[root] = true
	defer delete(w.opening, root)

	w.openRoots = append(w.openRoots, root)
	_ = store.TouchRecent(root)
	// Its own tabs come back too. A project with no saved layout is left
	// without tabs of its own, which is right: it is open because one of its
	// agents is being shown on another project's tab.
	w.restoreProject(root)
}

// paneDirs lists, once each, the directories a saved layout puts panes in.
func paneDirs(st *store.State) []string {
	seen := map[string]bool{}
	var dirs []string
	var walk func(n *store.Node)
	walk = func(n *store.Node) {
		if n == nil {
			return
		}
		if n.Pane != nil {
			if d := n.Pane.Cwd; d != "" && !seen[d] {
				seen[d] = true
				dirs = append(dirs, d)
			}
			return
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	for _, t := range st.Tabs {
		walk(t.Root)
	}
	return dirs
}

// branchLookups is how many branches are asked for at once. Each one is a git
// process, so this is not about CPU: it is about not queueing twenty process
// starts behind each other while the window has nothing to draw.
const branchLookups = 8

// branchesOf works out which branch each directory is on.
//
// Every answer costs a git process, and starting one on Windows takes about
// 80ms on the machine this was measured on. Asked one after another, a window
// coming back with twenty agents spent a second and a half of its startup
// waiting for them before anything was drawn. Nothing about them depends on
// anything else, so they are asked together.
func branchesOf(dirs []string) map[string]string {
	out := make(map[string]string, len(dirs))
	if len(dirs) == 0 {
		return out
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	queue := make(chan string)
	workers := branchLookups
	if len(dirs) < workers {
		workers = len(dirs)
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for dir := range queue {
				branch := branchOf(dir)
				mu.Lock()
				out[dir] = branch
				mu.Unlock()
			}
		}()
	}
	for _, dir := range dirs {
		queue <- dir
	}
	close(queue)
	wg.Wait()
	return out
}

// decodeNode rebuilds a layout subtree, starting a session for every pane.
// branches holds the git branch of every directory the subtree puts a pane in,
// worked out ahead of time so the restore does not stop for each one.
func (w *Workspace) decodeNode(n *store.Node, tabRoot string, branches map[string]string) *layout.Node {
	if n == nil {
		return nil
	}
	if n.Pane != nil {
		p := &Pane{
			ID:   n.Pane.ID,
			Kind: parseKind(n.Pane.Kind),
			Cwd:  n.Pane.Cwd,
			Name: n.Pane.Name,
			Task: n.Pane.Task,
			Root: n.Pane.Root,
		}
		if p.Root == "" {
			p.Root = tabRoot
		}
		if p.ID == "" || p.Cwd == "" {
			return nil
		}
		if p.Name == "" {
			p.Name = filepath.Base(p.Cwd)
		}
		p.Branch = branches[p.Cwd]
		// A borrowed pane names a project that may not be open yet. Restoring
		// the window as it was left means opening it: the alternative is a
		// pane belonging to nothing, which nothing would stop when its project
		// was closed and which would be counted against the tab's project
		// everywhere it was shown.
		w.ensureProjectOpen(p.Root)
		// A layout naming the same pane twice would put two processes on one
		// Claude transcript, and only the second would be reachable in the map:
		// the first could never be focused, resized or closed, and would run on
		// until the application exits. Ids are UUIDs, so this only happens to a
		// file that has been damaged or edited by hand; the later occurrence is
		// dropped, the way a leaf that fails to decode is.
		w.mu.Lock()
		_, duplicate := w.panes[p.ID]
		if !duplicate {
			w.panes[p.ID] = p
		}
		w.mu.Unlock()
		if duplicate {
			return nil
		}
		// Resuming reattaches the pane to the same Claude conversation it had
		// before, which is the point of persisting pane ids as session UUIDs.
		w.startPane(p, p.Kind == session.KindClaude)

		leaf := layout.NewLeaf(p.ID)
		if n.Weight > 0 {
			leaf.Weight = n.Weight
		}
		return leaf
	}

	node := layout.NewSplit(parseDir(n.Dir))
	// An absent weight means "an equal share", which is what NewSplit already
	// set; writing the zero back over it would only work by accident, because
	// the tree treats a non-positive weight as one.
	if n.Weight > 0 {
		node.Weight = n.Weight
	}
	for _, c := range n.Children {
		if dec := w.decodeNode(c, tabRoot, branches); dec != nil {
			node.Children = append(node.Children, dec)
		}
	}
	switch len(node.Children) {
	case 0:
		return nil
	case 1:
		// A split that lost all but one child collapses into that child, which
		// takes over the split's share of the surrounding space rather than
		// keeping the share it held inside the split. Otherwise a narrow pane
		// stacked inside a wide column would come back at its own old width and
		// the column's other neighbours would silently grow.
		//
		// This is what layout.Remove does when a live split collapses.
		only := node.Children[0]
		only.Weight = node.Weight
		return only
	}
	return node
}

func kindName(k session.Kind) string {
	if k == session.KindShell {
		return "shell"
	}
	return "claude"
}

func parseKind(s string) session.Kind {
	if s == "shell" {
		return session.KindShell
	}
	return session.KindClaude
}

func dirName(d layout.Dir) string {
	if d == layout.Vertical {
		return "v"
	}
	return "h"
}

func parseDir(s string) layout.Dir {
	if s == "v" {
		return layout.Vertical
	}
	return layout.Horizontal
}

// RestoreSession reopens the projects that were open when the application last
// exited, alongside the one it was started on. It returns how many extra
// projects were opened.
//
// The project the user asked for stays active: reopening the rest is meant to
// bring back context, not to move them somewhere they did not ask to be.
func (w *Workspace) RestoreSession() int {
	sess, err := store.LoadSession()
	if err != nil || sess == nil {
		return 0
	}
	// Each restoreProject below moves the focus onto the tabs it has just
	// brought back, so the tab the window was left on has to be noted before
	// any of them runs.
	wasOn := w.activeTab
	opened := 0
	for _, saved := range sess.Open {
		// Every other way into the workspace puts a project through
		// filepath.Abs and the window then compares roots exactly — isOpen,
		// tabsOf, the project switch. A path out of the saved session has been
		// through neither, so it is put into the same form here; left as it
		// was, opening the very same directory from the picker later would open
		// a second copy of a project that is already on screen.
		root, err := filepath.Abs(saved)
		if err != nil {
			continue
		}
		if w.sessionRootIsOpen(root) {
			continue
		}
		// A project whose directory has since been deleted or moved is simply
		// dropped rather than reported as an error at startup.
		if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
			continue
		}
		w.openRoots = append(w.openRoots, root)
		// A project reopened every run is one the user is working in, and the
		// recent list is capped: without this, the projects that are always
		// open are exactly the ones that age out of the picker, because only
		// the one named on the command line and the ones opened by hand are
		// ever recorded as used.
		_ = store.TouchRecent(root)
		if w.restoreProject(root) == 0 {
			// It was open but had no saved tabs; give it one so switching to
			// it shows something.
			active := w.activeRoot
			w.activeRoot = root
			kind := session.KindClaude
			if !w.ClaudeAvailable() {
				kind = session.KindShell
			}
			w.NewTab(kind, root, "")
			w.activeRoot = active
		}
		opened++
	}
	// Putting the focus back means the tab it was actually on, not merely a tab
	// of the right project: focusFirstTabOf would land on the first one and
	// quietly discard the tab the user quit from.
	if t := w.Tab(wasOn); t != nil && t.Root == w.activeRoot {
		w.activeTab = wasOn
	} else {
		w.focusFirstTabOf(w.activeRoot)
	}
	return opened
}

// sessionRootIsOpen reports whether a root recorded in the saved session names
// a project that is already open.
//
// The comparison has to be looser than isOpen's exact one: the path in the
// saved session was written by an earlier run, while the project the window
// started on came off the command line, and the two can spell the same
// directory differently — a trailing separator, or a different case on Windows
// and macOS. Reopening it would put a second copy of the project in the
// switcher, with a second set of panes trying to resume the very conversations
// the first set is already in.
func (w *Workspace) sessionRootIsOpen(root string) bool {
	for _, r := range w.openRoots {
		if sameDir(r, root) {
			return true
		}
	}
	return false
}

// SaveSession records which projects are open for the next run.
func (w *Workspace) SaveSession() error {
	return store.SaveSession(&store.Session{
		Open:   append([]string(nil), w.openRoots...),
		Active: w.activeRoot,
	})
}

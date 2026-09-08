package workspace

import (
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/jmwri/agent-wrapper/internal/layout"
	"github.com/jmwri/agent-wrapper/internal/session"
	"github.com/jmwri/agent-wrapper/internal/store"
)

// SaveAll writes the layout of every open project and the list of which ones
// were open.
func (w *Workspace) SaveAll() error {
	var firstErr error
	for _, root := range w.openRoots {
		if err := w.SaveProject(root); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := w.SaveSession(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// SaveProject writes one project's tabs to its own layout file, so projects
// are restored independently of one another.
func (w *Workspace) SaveProject(root string) error {
	st := &store.State{}
	for i, t := range w.tabsOf(root) {
		if t.ID == w.activeTab {
			st.Active = i
		}
		st.Tabs = append(st.Tabs, store.Tab{
			Title: t.Title,
			Focus: t.Focus,
			Root:  w.encodeNode(t.Tree),
		})
	}
	return store.Save(root, st)
}

// encodeNode converts a live layout node into its persisted form.
func (w *Workspace) encodeNode(n *layout.Node) *store.Node {
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
		return out
	}
	out.Dir = dirName(n.Dir)
	for _, c := range n.Children {
		if enc := w.encodeNode(c); enc != nil {
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

	added := 0
	var firstTab, activeTab string
	for i, t := range st.Tabs {
		tree := w.decodeNode(t.Root)
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
		w.Tabs = append(w.Tabs, tab)
		if firstTab == "" {
			firstTab = tab.ID
		}
		if i == st.Active {
			activeTab = tab.ID
		}
		added++
	}
	if added == 0 {
		return 0
	}

	if activeTab == "" {
		activeTab = firstTab
	}
	w.activeTab = activeTab
	return added
}

// decodeNode rebuilds a layout subtree, starting a session for every pane.
func (w *Workspace) decodeNode(n *store.Node) *layout.Node {
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
		}
		if p.ID == "" || p.Cwd == "" {
			return nil
		}
		if p.Name == "" {
			p.Name = filepath.Base(p.Cwd)
		}
		p.Branch = branchOf(p.Cwd)
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
		if dec := w.decodeNode(c); dec != nil {
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
	opened := 0
	for _, root := range sess.Open {
		if w.isOpen(root) {
			continue
		}
		// A project whose directory has since been deleted or moved is simply
		// dropped rather than reported as an error at startup.
		if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
			continue
		}
		w.openRoots = append(w.openRoots, root)
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
	// restoreProject moves focus to the tabs it restored, so put it back.
	w.focusFirstTabOf(w.activeRoot)
	return opened
}

// SaveSession records which projects are open for the next run.
func (w *Workspace) SaveSession() error {
	return store.SaveSession(&store.Session{
		Open:   append([]string(nil), w.openRoots...),
		Active: w.activeRoot,
	})
}

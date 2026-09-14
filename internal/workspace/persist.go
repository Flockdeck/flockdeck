package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"

	"github.com/jmwri/flockdeck/internal/layout"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
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
	if err := w.SaveLayouts(); err != nil {
		errs = append(errs, err)
	}
	if err := w.SaveSession(); err != nil {
		errs = append(errs, fmt.Errorf("open projects: %w", err))
	}
	return errors.Join(errs...)
}

// SaveLayouts writes the layout of every open project and leaves the list of
// which ones are open alone. It is what is saved while the app runs; SaveAll,
// which records that list as well, is what is saved as it stops.
func (w *Workspace) SaveLayouts() error {
	var errs []error
	for _, root := range w.openRoots {
		if err := w.SaveProject(root); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", root, err))
		}
	}
	return errors.Join(errs...)
}

// SaveProject writes one project's tabs to its own layout file, so projects
// are restored independently of one another.
func (w *Workspace) SaveProject(root string) error {
	tabs := w.tabsOf(root)
	st := &store.State{HandNames: true}
	onScreen, leftOn := -1, -1
	for i, t := range tabs {
		if t.ID == w.activeTab {
			onScreen = i
		}
		if t.ID == w.lastTab[root] {
			leftOn = i
		}
		st.Tabs = append(st.Tabs, store.Tab{
			Title: t.Title,
			Focus: t.Focus,
			Root:  w.encodeNode(t.Tree, t.Root),
			Named: t.Named,
			Auto:  t.auto,
		})
	}
	// A project that is not on screen is saved on the tab it was last left on,
	// which is where switching back to it would land.
	//
	// Reading the last saved index back is a whole file read, and it is only
	// an answer for a project nobody has been in since it was opened. Asking
	// for it first and then throwing it away put that read on the way out of
	// every run and on every project closed, for projects it can never apply
	// to.
	st.Active = onScreen
	if st.Active < 0 {
		st.Active = leftOn
	}
	if st.Active < 0 {
		st.Active = w.rememberedActive(root, len(tabs))
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
//
// It is read with Peek, not Load. The layout is read here only for that index,
// and what it holds is about to be written over, so whether this read works
// says nothing about the tabs in memory. Through Load it said they were the
// saved ones: a layout that could not be read at start, whose project came up
// on one fresh tab, was then written over without being kept.
func (w *Workspace) rememberedActive(root string, tabs int) int {
	if tabs == 0 {
		return 0
	}
	st, err := store.Peek(root)
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
			ID:    p.ID,
			Kind:  kindName(p.Kind),
			Cwd:   p.Cwd,
			Name:  p.Name,
			Task:  p.Task,
			Agent: p.Agent,
			Model: p.Model,
			// Written only for a routed pane, like Root below.
			Routed:          p.Routed,
			RoutedFrom:      p.RoutedFrom,
			RoutedFromAgent: p.RoutedFromAgent,
			// Zero, and so left out, for a pane no window has measured.
			Cols: p.Cols,
			Rows: p.Rows,
			// Empty for a pane the user started themselves, which is left out
			// by omitempty like every other field here.
			Parent: p.Parent,
		}
		// Only a pane borrowed from another project needs its project written
		// down; leaving it out otherwise keeps the file as it has always been
		// for the ordinary case, which is every pane in a one-project tab.
		if p.Root != "" && !sameDir(p.Root, tabRoot) {
			out.Pane.Root = p.Root
		}
		// A conversation the agent has moved on to since the pane started is
		// the one a restore has to resume; a pane still in its first one is
		// written as panes always have been.
		if c := w.conversationOf(p); c != p.ID {
			out.Pane.Conversation = c
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
// create a fresh tab. The error is every saved layout it could not read, this
// project's and those of the projects its panes brought back with it; each of
// those projects has still been opened, without its tabs.
func (w *Workspace) Restore() (bool, error) {
	n := w.restoreProject(w.activeRoot)
	return n > 0, w.RestoreErrors()
}

// RestoreErrors returns what the restores since it was last asked could not
// read, and forgets it: saved layouts, and the list of open projects that
// RestoreSession reads.
//
// A file that cannot be read is restored as nothing, which should never stop
// the app starting, and so was never said at all. The project came up on one
// fresh tab, and the save half a minute later moved the user's file aside
// under a new name, which nothing said either.
func (w *Workspace) RestoreErrors() error {
	err := errors.Join(w.restoreErrs...)
	w.restoreErrs = nil
	return err
}

// restoreProject loads a project's saved tabs and appends them, relaunching
// each pane. An agent pane resumes its previous conversation where its agent
// can. It returns how many tabs were restored; a layout that could not be read
// is noted for RestoreErrors.
func (w *Workspace) restoreProject(root string) int {
	st, err := store.Load(root)
	if err != nil {
		w.restoreErrs = append(w.restoreErrs, fmt.Errorf("the saved layout for %s could not be read, so it opens without it; the file is kept, and moved aside rather than written over when the layout is next saved: %w", root, err))
	}
	if err != nil || st == nil || len(st.Tabs) == 0 {
		return 0
	}

	r := &restoring{branches: branchesOf(paneDirs(st))}
	added := 0
	// nearest is the last tab restored at or before the saved active one, so a
	// tab that cannot be restored hands the window over to its neighbour.
	var firstTab, activeTab, nearest string
	for i, t := range st.Tabs {
		tree := w.decodeNode(t.Root, root, r)
		if tree == nil {
			continue
		}
		// A layout saved by an older build can nest a row in a row, which
		// looks like one row and splits and resizes like two.
		tree.Flatten()
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
		w.restoreTitle(tab, t, st.HandNames)
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
	if w.deferLaunch != nil {
		*w.deferLaunch = append(*w.deferLaunch, r.waiting...)
	} else {
		w.launch(r.waiting)
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
	// Restoring a project alongside the one on screen hands the focus straight
	// back to that one, so this is also what switching to it should land on.
	if w.lastTab == nil {
		w.lastTab = map[string]string{}
	}
	w.lastTab[root] = activeTab
	return added
}

// restoreTitle says who chose a restored tab's title, and so whether a prompt
// may still rename it and what giving up a name chosen by hand puts back.
//
// A layout written before names chosen by hand were marked (handNames absent)
// cannot say whether a title was typed or came from a prompt. A title that is
// not the one the tab would be given now is taken for a name, with the title
// behind it not known: giving it up then gives the tab the title it would be
// given if it were opened now. Taking it for automatic instead would have left
// a tab named by hand before the upgrade with no way back to naming itself.
func (w *Workspace) restoreTitle(tab *Tab, saved store.Tab, marked bool) {
	switch {
	case saved.Named:
		tab.Named = true
		tab.auto = saved.Auto
		tab.autoOpen = saved.Auto != "" && w.isOpenAutoTitle(tab, saved.Auto)
	case marked:
		tab.AutoTitle = w.stillAutoTitled(tab)
	default:
		tab.AutoTitle = w.stillAutoTitled(tab)
		if !tab.AutoTitle {
			fresh, _ := w.freshTitle(tab)
			tab.Named = tab.Title != fresh
		}
	}
}

// stillAutoTitled reports whether a restored tab should go on renaming itself
// after the first thing its agent is asked.
//
// The saved layout carries the title but nothing saying who chose it, so the
// test is whether it is still the name the tab would have been given
// automatically: the name of the pane it was opened with, which is that pane's
// directory — the project's only for a tab opened in the project itself, and
// the worktree's for one opened on a worktree. Without this, a tab created but
// never prompted before a restart would keep its directory name forever, while
// an identical tab created after one would rename itself — the same tab
// behaving differently for no reason the user can see.
//
// A tab the user deliberately named after its own directory loses nothing much
// by being renamed once more; a tab named after a prompt keeps that name.
func (w *Workspace) stillAutoTitled(t *Tab) bool { return w.isOpenAutoTitle(t, t.Title) }

// isOpenAutoTitle is stillAutoTitled for any title of t's, which for a tab
// named by hand is the automatic title kept behind the name.
func (w *Workspace) isOpenAutoTitle(t *Tab, title string) bool {
	// Only an agent pane reports the prompts a rename would come from.
	agents := false
	for _, id := range t.Tree.Panes() {
		p := w.Pane(id)
		if p == nil || !p.IsAgent() {
			continue
		}
		if title == p.Name {
			return true
		}
		agents = true
	}
	return agents && title == filepath.Base(t.Root)
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

// branchLookup asks for one directory's branch. It is a variable so a test can
// see how many lookups are in flight at once, which is the property worth
// checking and one a clock cannot see on a machine where git starts quickly.
var branchLookup = branchOf

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
				branch := branchLookup(dir)
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

// restoring is what one restore carries down through the tree: the branch of
// every directory it will use, and the panes it has built and not yet started.
type restoring struct {
	branches map[string]string
	waiting  []*Pane
}

// paneLaunches is how many panes are started at once. They are processes, and
// they are all going to be started either way; the bound is there so a window
// coming back with thirty agents does not ask the machine for thirty terminals
// in the same instant.
const paneLaunches = 8

// launchPane starts one pane a restore built. It is a variable for the same
// reason branchLookup is: a shell starts in a couple of milliseconds on Linux,
// so whether panes are started together only shows up by counting them.
var launchPane = (*Workspace).startPane

// launch starts every pane a restore built.
//
// Starting one costs about 170ms on this machine — a settings file, a look for
// the conversation to resume, and a terminal — and they were started one after
// another as the tree was read. Twenty agents meant three and a half seconds
// in which the window had nothing to show. Nothing about one start depends on
// another, so they go together.
//
// This is safe to do here and nowhere else. The server funnels every read and
// write of the workspace through one goroutine, and that goroutine is the one
// waiting below: while it waits, nothing else reads a pane's error or session,
// and each worker only ever touches the pane it was given. The hook server's
// goroutine can look up a pane, but it takes the lock and it gives up on one
// with no session yet, which is what a pane about to be started has.
func (w *Workspace) launch(panes []*Pane) {
	if len(panes) == 0 {
		return
	}
	if len(panes) == 1 {
		launchPane(w, panes[0], panes[0].IsAgent())
		return
	}
	queue := make(chan *Pane)
	var wg sync.WaitGroup
	workers := paneLaunches
	if len(panes) < workers {
		workers = len(panes)
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range queue {
				// Resuming reattaches the pane to the same conversation it had
				// before, which is the point of persisting pane ids as session
				// UUIDs. Whether the agent can be reattached at all is
				// startPane's to decide, from its Spec and its transcript.
				launchPane(w, p, p.IsAgent())
			}
		}()
	}
	for _, p := range panes {
		queue <- p
	}
	close(queue)
	wg.Wait()
}

// decodeNode rebuilds a layout subtree, building a pane for every leaf and
// noting it down to be launched once the whole layout has been read.
func (w *Workspace) decodeNode(n *store.Node, tabRoot string, r *restoring) *layout.Node {
	if n == nil {
		return nil
	}
	if n.Pane != nil {
		p := &Pane{
			ID:              n.Pane.ID,
			Kind:            parseKind(n.Pane.Kind),
			Cwd:             n.Pane.Cwd,
			Name:            n.Pane.Name,
			Task:            n.Pane.Task,
			Root:            n.Pane.Root,
			Agent:           n.Pane.Agent,
			Model:           n.Pane.Model,
			Routed:          n.Pane.Routed,
			RoutedFrom:      n.Pane.RoutedFrom,
			RoutedFromAgent: n.Pane.RoutedFromAgent,
			Conversation:    n.Pane.Conversation,
			Parent:          n.Pane.Parent,
		}
		if p.Root == "" {
			p.Root = tabRoot
		}
		if p.ID == "" || p.Cwd == "" {
			return nil
		}
		// The size the pane was last drawn at. A restore starts every pane
		// before any window has opened to measure one, and an agent resuming a
		// conversation prints it straight away: at the default size it came out
		// wrapped at eighty columns, and what it had printed stayed that way
		// once the window resized it. A size no terminal has is a file edited
		// by hand, and the default is kept.
		if c, r := n.Pane.Cols, n.Pane.Rows; c > 0 && r > 0 && c <= maxRestoredSize && r <= maxRestoredSize {
			// Never smaller than the default, though. The size saved is the
			// one the pane last followed, which can be a phone reached
			// through the relay and asked to fit the pane to its screen, and
			// a pane started at a phone's forty columns prints its resumed
			// conversation narrower than the default ever did, before the
			// window on the desk has measured it. A pane that really is
			// narrow shrinks a moment later, as every pane used to.
			p.Cols, p.Rows = max(c, defaultCols), max(r, defaultRows)
		}
		if p.Name == "" {
			p.Name = filepath.Base(p.Cwd)
		}
		p.Branch = r.branches[p.Cwd]
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
		// Launching is left until the whole layout has been read, so every
		// pane can be started at once rather than the next one waiting on the
		// last. Every pane noted here reaches a tab: a leaf is only noted once
		// it is going to be used, and a split that loses every child had no
		// child to note.
		r.waiting = append(r.waiting, p)

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
		if dec := w.decodeNode(c, tabRoot, r); dec != nil {
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

// maxRestoredSize bounds the columns and rows a restored pane is started at.
// It is far beyond any screen, and well inside what a terminal's size can be
// told in.
const maxRestoredSize = 4096

// defaultCols and defaultRows are the size a pane starts at when nothing has
// measured it.
const defaultCols, defaultRows = 80, 24

func kindName(k session.Kind) string {
	if k == session.KindShell {
		return "shell"
	}
	return "agent"
}

// parseKind reads a saved pane kind. Anything that is not a shell is an agent,
// which takes in "claude" from a layout a build before this one wrote: those
// files are migrated on the way out of the store, and a hand-edited one that
// escaped that still restores as the pane its author meant.
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
//
// Each restoreProject below builds its tabs as it always did, but collects
// every pane it would have started rather than starting it there; they are
// all started together once every project has been read, so a window with
// several other projects open waits once, for whichever project has the most
// to start, rather than once per project. See restoreProject's deferLaunch.
//
// A list that could not be read, and the layout of any project reopened that
// could not be, are noted for RestoreErrors.
func (w *Workspace) RestoreSession() int {
	sess, err := store.LoadSession()
	if err != nil {
		w.restoreErrs = append(w.restoreErrs, fmt.Errorf("the list of open projects could not be read, so no others are reopened; the file is kept, and moved aside rather than written over when the list is next saved: %w", err))
	}
	if err != nil || sess == nil {
		return 0
	}
	// Each restoreProject below moves the focus onto the tabs it has just
	// brought back, so the tab the window was left on has to be noted before
	// any of them runs.
	wasOn := w.activeTab
	opened := 0
	var reopened []string
	var pending []*Pane
	w.deferLaunch = &pending
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
		// A project whose directory is not there is not opened, and not
		// reported as an error at startup. It may be back, though: a USB
		// stick, a network drive not yet connected. Dropping it here dropped
		// it from the list at the next save, for good, so it is carried
		// forward instead, for maxAwayStarts starts in a row, and only then
		// let go.
		if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
			if starts := sess.Away[saved] + 1; starts <= maxAwayStarts {
				w.away = append(w.away, awayRoot{root: root, starts: starts})
			}
			continue
		}
		w.openRoots = append(w.openRoots, root)
		// A project reopened every run is one the user is working in, and the
		// recent list is capped: without this, the projects that are always
		// open are exactly the ones that age out of the picker, because only
		// the one named on the command line and the ones opened by hand are
		// ever recorded as used. They are recorded together, below.
		reopened = append(reopened, root)
		if w.restoreProject(root) == 0 {
			// It was open but had no saved tabs; give it one so switching to
			// it shows something.
			active := w.activeRoot
			w.activeRoot = root
			w.NewTab(w.firstPaneKind(), root, "")
			w.activeRoot = active
		}
		opened++
	}
	w.deferLaunch = nil
	w.launch(pending)
	// Putting the focus back means the tab it was actually on, not merely a tab
	// of the right project: focusFirstTabOf would land on the first one and
	// quietly discard the tab the user quit from.
	if t := w.Tab(wasOn); t != nil && t.Root == w.activeRoot {
		w.activeTab = wasOn
	} else {
		w.focusFirstTabOf(w.activeRoot)
	}
	// The projects reopened here are recorded as used all at once, behind the
	// one this run was started on. Recorded as each came back they went ahead
	// of it, so the picker led with whichever happened to be last in the saved
	// session rather than the one the user is in, and each was a rewrite of
	// the list of its own. A start that reopens what the one before did finds
	// them in this order already, and writes nothing.
	_ = store.TouchRecents(append([]string{w.activeRoot}, reopened...)...)
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
	// A short name, a junction or a symlink of an open folder is that folder
	// too; see openRootOnDisk.
	_, ok := w.openRootOnDisk(root)
	return ok
}

// SaveSession records which projects are open for the next run.
func (w *Workspace) SaveSession() error {
	return store.SaveSession(w.Session())
}

// Session is the list of open projects SaveSession records: the ones open,
// and after them the ones whose folders were away at start and are still
// being kept for their return, with how many starts each has been away. One
// whose folder came back and was opened during the run is among the open.
func (w *Workspace) Session() *store.Session {
	s := &store.Session{
		Open:   append([]string(nil), w.openRoots...),
		Active: w.activeRoot,
	}
	for _, a := range w.away {
		if w.sessionRootIsOpen(a.root) {
			continue
		}
		s.Open = append(s.Open, a.root)
		if s.Away == nil {
			s.Away = map[string]int{}
		}
		s.Away[a.root] = a.starts
	}
	return s
}

// awayRoot is a project whose folder was not there when this run started,
// with how many starts in a row, this one included, have found it missing.
type awayRoot struct {
	root   string
	starts int
}

// maxAwayStarts is how many starts in a row a project's folder may be found
// missing before the project is let go of: enough for a drive left unplugged
// for a week or two of daily use, and not so many that a folder deleted for
// good haunts the list for ever.
const maxAwayStarts = 10

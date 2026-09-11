// Package workspace holds the mutable state of the application: the projects
// that are open, the tabs in each, the pane tree in each tab, and the live
// sessions behind those panes.
//
// It deliberately knows nothing about rendering or key bindings, so the layout
// and session lifecycle can be exercised without a user interface.
//
// A tab belongs to a project and so does a pane, and the two need not agree: a
// tab can show agents from more than one project side by side. The tab's
// project decides which tab bar draws it. The pane's project decides what the
// agent is told it is working in, whose summary counts it as waiting, and what
// stops it when a project closes — so anything grouping panes by project asks
// rootOf, never the tab the pane happens to sit on.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/creds"
	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/layout"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/store"
)

// orphanedSettingsAge is how old a generated settings file must be before it is
// assumed to belong to a run that no longer exists.
const orphanedSettingsAge = 24 * time.Hour

// Pane couples a live session with the configuration needed to restart it.
type Pane struct {
	ID   string
	Kind session.Kind
	Cwd  string
	Name string
	// Agent is the id of the agent this pane runs, and Model the model it was
	// asked for. Both are empty on a shell pane; an empty Agent on an agent
	// pane means the catalog's default, so a pane opened with one keystroke
	// and a layout written before panes could choose both still mean the same
	// thing. An empty Model means whatever the agent is already set to, which
	// is not the same as naming its default model.
	Agent string
	Model string
	// Root is the open project this pane belongs to. It is usually the project
	// of the tab the pane sits on, but need not be: a tab can show agents from
	// more than one project side by side, and this is what says which one an
	// agent is working in. Cwd may be deeper still, since a pane can be put
	// into a worktree inside its project.
	Root string
	// Branch is the git branch checked out in Cwd, shown in the pane header so
	// agents running in parallel worktrees can be told apart at a glance.
	Branch string
	// Git is a periodically refreshed summary of the checkout the pane works
	// in: whether it has uncommitted work and how it stands against upstream.
	Git gitx.Status

	// Cols and Rows are the terminal size the viewer last measured for this
	// pane. They are remembered so a restart comes back at the size the pane
	// is really being drawn at, instead of reflowing at the default until the
	// window happens to be resized again.
	Cols int
	Rows int

	Sess *session.Session
	// Err records why a pane failed to start, so the UI can show it in place
	// rather than the app dying at launch.
	Err error
	// initial is the opening prompt a spawned child is started with. It is used
	// once: a restart resumes the conversation instead of asking again.
	initial string
	// Task is what the pane was spawned to do, kept after the opening prompt
	// has been spent so the agent can still be told why it exists — after a
	// restart, or after its context has been compacted away.
	Task string
}

// Alive reports whether the pane has a running process.
func (p *Pane) Alive() bool { return p.Sess != nil && !p.Sess.Exited() }

// IsAgent reports whether the pane runs an agent rather than a plain shell.
//
// A pane is one or the other, so this is written as "not a shell": which agent
// it runs is the Spec's business, and everything the workspace does with the
// distinction — resuming on restart, renaming a tab after the first prompt,
// counting a project as waiting — is true of every agent and of none of them.
func (p *Pane) IsAgent() bool { return p.Kind != session.KindShell }

// Status returns the pane's status and detail, accounting for panes that never
// started.
func (p *Pane) Status() (session.Status, string) {
	if p.Sess == nil {
		return session.StatusExited, ""
	}
	return p.Sess.Status()
}

// Tab is one tab: a pane tree, which pane has focus, and the project it
// belongs to.
type Tab struct {
	ID    string
	Root  string // the project this tab belongs to
	Title string
	Tree  *layout.Node
	Focus string
	// Zoom temporarily gives the focused pane the whole window.
	Zoom bool
	// AutoTitle is set while the title is still the directory name the tab was
	// given automatically, so the first prompt may replace it. Renaming a tab
	// by hand clears it and the title is then left alone.
	AutoTitle bool
}

// Project is an open project and a summary of what is happening inside it.
type Project struct {
	Root    string
	Name    string
	Active  bool
	Tabs    int
	Waiting int
	Working int
}

// Workspace is the whole application state.
type Workspace struct {
	// Tabs holds every tab across every open project, in creation order.
	Tabs []*Tab

	// Broadcast, when enabled, mirrors typed input into every pane in the
	// broadcast set as well as the focused one.
	Broadcast    bool
	BroadcastSet map[string]bool
	// broadcastAuto marks a set that was filled in by default rather than
	// chosen pane by pane, so it can be dropped when broadcast is switched
	// off instead of following the user into the next tab.
	broadcastAuto bool

	mu    sync.RWMutex
	panes map[string]*Pane
	// pendingTitles holds titles a pane asked for from the hook goroutine,
	// waiting to be applied to its tab on the interface's own goroutine.
	pendingTitles map[string]string

	// openRoots are the projects currently open, in the order they were
	// opened; activeRoot is the one being shown.
	openRoots []string
	// opening guards against a restore that reopens itself. A tab can show
	// panes from another project, so restoring one project can open a second,
	// whose own layout may hold a pane belonging back to the first.
	opening    map[string]bool
	activeRoot string
	activeTab  string
	// lastTab remembers which tab each project was left on, so coming back to
	// a project comes back to what you were doing in it.
	lastTab map[string]string

	selfExe     string
	spawnCmd    string
	settingsDir string
	hookSrv     *hooks.Server
	claudeExe   string

	// catalogMu guards the catalog, which is read from the goroutine that
	// starts panes and replaced by whoever asks for it to be read again.
	catalogMu sync.RWMutex
	// catalog is the agents this workspace can run: the built-in specs
	// overlaid with the user's agents.json. It is held rather than read per
	// pane because starting twelve of them would otherwise open the same file
	// twelve times, and ReloadAgents is how an edit made by hand takes effect
	// without a restart.
	catalog *agent.Catalog
	// runAgent is the agent -agent named for this run, which outranks the
	// catalog's defaults and is outranked by a pane that names one itself.
	runAgent string

	onWake func()
}

// Options configures a new workspace.
type Options struct {
	// Root is the project to open first.
	Root string
	// OnWake is called whenever any session changes and the interface should
	// refresh. It may be called from any goroutine.
	OnWake func()
	// HookBinary overrides the executable Claude panes invoke to report their
	// lifecycle. It defaults to this process, and exists so tests and the
	// development harness can point at a built binary.
	HookBinary string
}

// New creates a workspace, starting the hook server that panes report to.
func New(opts Options) (*Workspace, error) {
	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}
	selfExe := opts.HookBinary
	if selfExe == "" {
		if selfExe, err = os.Executable(); err != nil {
			return nil, fmt.Errorf("locate own binary: %w", err)
		}
	}
	settingsDir, err := store.SessionsDir()
	if err != nil {
		return nil, err
	}

	w := &Workspace{
		panes:        map[string]*Pane{},
		BroadcastSet: map[string]bool{},
		openRoots:    []string{root},
		activeRoot:   root,
		selfExe:      selfExe,
		settingsDir:  settingsDir,
		onWake:       opts.OnWake,
	}
	_ = store.TouchRecent(root)

	// Clear out settings files orphaned by runs that were killed rather than
	// closed, so the state directory does not grow without bound.
	_, _ = store.SweepSessions(orphanedSettingsAge)

	// A missing claude CLI is not fatal: shell panes still work, and the pane
	// shows the reason it could not start.
	w.claudeExe, _ = session.LookClaude()
	w.spawnCmd = spawnCommand(selfExe)
	w.catalog = agent.Load()

	srv, err := hooks.Serve(w.handleHook)
	if err != nil {
		return nil, err
	}
	w.hookSrv = srv
	return w, nil
}

// HookServer is the loopback API panes call back on, so the server can install
// the handler that lets an agent spawn helpers.
func (w *Workspace) HookServer() *hooks.Server { return w.hookSrv }

// SetWake installs the refresh callback. It exists so the server, which needs
// the workspace to construct, can register itself afterwards.
func (w *Workspace) SetWake(fn func()) { w.onWake = fn }

// ActiveRoot returns the project currently being shown.
func (w *Workspace) ActiveRoot() string { return w.activeRoot }

// ClaudeAvailable reports whether the claude CLI was found.
func (w *Workspace) ClaudeAvailable() bool { return w.claudeExe != "" }

func (w *Workspace) wake() {
	if w.onWake != nil {
		w.onWake()
	}
}

// handleHook applies a lifecycle event from a pane.
func (w *Workspace) handleHook(ev hooks.Event) {
	// The session is taken once, under the lock. This runs on the hook
	// server's goroutine while the interface may be restarting the very pane
	// the event is about, and a pane mid-restart has no session at all: read
	// the field twice and the second read can be the nil.
	w.mu.RLock()
	p := w.panes[ev.SessionID]
	var sess *session.Session
	if p != nil {
		sess = p.Sess
	}
	w.mu.RUnlock()
	if sess == nil {
		return
	}
	// A tab named after its directory is not much help once several are open;
	// the first thing the agent was asked is far more recognisable.
	if ev.Prompt != "" {
		w.nameTabAfterPrompt(p.ID, ev.Prompt)
	}

	st, detail, ok := session.StatusForEvent(ev.Event, ev.Tool)
	if !ok {
		return
	}
	// SessionEnd means the conversation ended, but the process may still be
	// drawing its exit screen; let the PTY reader observe the real exit.
	if st == session.StatusExited {
		return
	}
	sess.SetStatus(st, detail)
}

// ----------------------------------------------------------------- projects

// Projects returns the open projects with a summary of each.
//
// An agent is counted against the project it belongs to rather than the
// project of the tab it is drawn on. The switcher's badge is what says a
// project is waiting on you, and a borrowed pane counted against the tab it
// sits on lights up a project that is not the one whose work has stopped —
// while the project that is actually blocked shows nothing.
func (w *Workspace) Projects() []Project {
	w.applyPendingTitles()
	names := projectNames(w.openRoots)
	out := make([]Project, len(w.openRoots))
	byRoot := make(map[string]int, len(w.openRoots))
	for i, root := range w.openRoots {
		out[i] = Project{Root: root, Name: names[i], Active: root == w.activeRoot}
		byRoot[root] = i
	}
	// Tabs and panes name their project with the string it was opened under,
	// so the map answers almost every lookup; a layout written elsewhere can
	// spell it in another case, and that falls back to comparing the paths.
	indexOf := func(root string) int {
		if i, ok := byRoot[root]; ok {
			return i
		}
		for i, r := range w.openRoots {
			if sameDir(r, root) {
				return i
			}
		}
		return -1
	}

	// One pass over the tabs under one lock, rather than a walk of every tab
	// once per open project and a separately locked lookup for every pane in
	// it: this is rebuilt every time any pane changes status.
	w.mu.RLock()
	defer w.mu.RUnlock()
	for _, t := range w.Tabs {
		if i := indexOf(t.Root); i >= 0 {
			out[i].Tabs++
		}
		for _, id := range t.Tree.Panes() {
			pane := w.panes[id]
			if pane == nil || pane.Sess == nil {
				continue
			}
			root := pane.Root
			if root == "" {
				root = t.Root
			}
			i := indexOf(root)
			if i < 0 {
				continue
			}
			switch st, _ := pane.Sess.Status(); st {
			case session.StatusWaiting:
				out[i].Waiting++
			case session.StatusWorking:
				out[i].Working++
			}
		}
	}
	return out
}

// maxNameDepth bounds how much of a path a project's name may grow to. Four
// elements is already more than a label, and a name that long has stopped
// helping long before it stops fitting.
const maxNameDepth = 4

// projectNames names each root by its own directory, and gives the ones whose
// names would collide enough of the path to tell them apart.
//
// Two checkouts of the same repository, or the same service in two of them,
// are called the same thing. The project switcher shows the name and nothing
// else, and so does the list of other open projects an agent is given, so
// without this both are asked to tell two identical labels apart.
//
// One parent is not always enough. Trees laid out the same way — a mirror, a
// backup, a second machine's copy — agree for as many elements as the layout
// is deep, so the names grow an element at a time until they differ or the
// paths run out.
func projectNames(roots []string) []string {
	names := make([]string, len(roots))
	depth := make([]int, len(roots))
	for i, root := range roots {
		names[i], depth[i] = pathTail(root, 1), 1
	}
	for round := 1; round < maxNameDepth; round++ {
		seen := make(map[string]int, len(names))
		for _, n := range names {
			seen[n]++
		}
		grew := false
		for i, root := range roots {
			if seen[names[i]] < 2 {
				continue
			}
			// A root with nothing left to prepend keeps the name it has; the
			// project it collides with is the one that grows.
			longer := pathTail(root, depth[i]+1)
			if longer == names[i] {
				continue
			}
			names[i], depth[i] = longer, depth[i]+1
			grew = true
		}
		if !grew {
			break
		}
	}
	return names
}

// pathTail returns the last n elements of a path, or as much of it as there is.
func pathTail(path string, n int) string {
	rest := filepath.Clean(path)
	out := ""
	for i := 0; i < n; i++ {
		base, parent := filepath.Base(rest), filepath.Dir(rest)
		// The top of a path names nothing worth borrowing: Base of "C:\" and of
		// "/" is a separator, and Dir stops moving once it is reached.
		if base == "" || base == "." || (len(base) == 1 && os.IsPathSeparator(base[0])) {
			break
		}
		if out == "" {
			out = base
		} else {
			out = filepath.Join(base, out)
		}
		if parent == rest {
			break
		}
		rest = parent
	}
	if out == "" {
		out = filepath.Clean(path)
	}
	return out
}

// OpenProject opens a directory as a project and makes it active. A project
// that is already open is simply selected. Its saved layout is restored the
// first time it is opened; if it has none, a single tab is created.
func (w *Workspace) OpenProject(path string) error {
	root, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", path, err)
	}
	// Say which of the ways this can go wrong actually happened. The path is
	// typed by hand or picked from a list of directories that may since have
	// moved, and "is not a directory" sends the user looking for the wrong
	// thing when the answer is that it is gone.
	switch fi, err := os.Stat(root); {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("%s does not exist", root)
	case err != nil:
		return fmt.Errorf("open %s: %w", root, err)
	case !fi.IsDir():
		return fmt.Errorf("%s is not a directory", root)
	}

	if open, ok := w.openRootFor(root); ok {
		// Select it under the spelling it was opened with: the tabs already
		// hold that one, and they are matched against it by string.
		w.SelectProject(open)
		return nil
	}

	// Restoring the new project's tabs, or making it one, moves the focus onto
	// them, so the tab this project is being left on has to be noted first.
	w.rememberTab()
	w.openRoots = append(w.openRoots, root)
	w.activeRoot = root
	_ = store.TouchRecent(root)

	if n := w.restoreProject(root); n == 0 {
		kind := session.KindClaude
		if !w.ClaudeAvailable() {
			kind = session.KindShell
		}
		w.NewTab(kind, root, "")
	} else {
		w.focusFirstTabOf(root)
	}
	w.wake()
	return nil
}

// SelectProject shows an already open project.
func (w *Workspace) SelectProject(root string) {
	root, ok := w.openRootFor(root)
	if !ok {
		return
	}
	w.activeRoot = root
	_ = store.TouchRecent(root)
	w.focusFirstTabOf(root)
	w.wake()
}

// CloseProject saves a project's layout, closes its tabs and removes it. The
// last open project cannot be closed, since the window would have nothing to
// show.
func (w *Workspace) CloseProject(root string) {
	root, ok := w.openRootFor(root)
	if !ok || len(w.openRoots) <= 1 {
		return
	}
	_ = w.SaveProject(root)

	kept := make([]*Tab, 0, len(w.Tabs))
	// Tabs made to hold agents that were being shown here but belong to a
	// project that stays open. They are appended once the surviving tabs are
	// known, so they come out last among their own project's tabs.
	var rescued []*Tab
	for _, t := range w.Tabs {
		if t.Root == root {
			// The tab goes, and this project's agents go with it. One borrowed
			// from a project that is still open is not this project's to stop:
			// closing a window onto an agent is not the same as ending it, so
			// it is given a tab of its own back in the project it works in.
			for _, id := range t.Tree.Panes() {
				if home := w.rootOf(id); !sameDir(home, root) && w.isOpen(home) {
					if moved := w.tabHolding(id, home); moved != nil {
						rescued = append(rescued, moved)
						continue
					}
				}
				w.destroyPane(id)
			}
			continue
		}
		// A tab belonging to another project may still be showing this one's
		// agents. Closing a project stops its agents, so they have to be found
		// where they are rather than only among its own tabs.
		//
		// Which panes are going is settled before any of them do. A tree
		// refuses to give up its last pane, so taking them out one at a time
		// would leave a tab whose whole contents were borrowed still drawing
		// the last of them — a pane with no process and nothing behind it,
		// which only closing the tab could get rid of.
		var going []string
		keeping := 0
		for _, id := range t.Tree.Panes() {
			if p := w.Pane(id); p != nil && !sameDir(w.rootOf(id), root) {
				keeping++
				continue
			}
			going = append(going, id)
		}
		if keeping == 0 {
			for _, id := range going {
				w.destroyPane(id)
			}
			// Nothing of this tab is left once the closing project's panes
			// have gone.
			continue
		}
		for _, id := range going {
			// Losing the pane you were typing into leaves the focus beside the
			// space it left, the way closing it by hand would, rather than
			// throwing it to the front of the tab.
			if t.Focus == id {
				t.Focus = paneBesideTheGap(t, id)
			}
			t.Tree.Remove(id)
			w.destroyPane(id)
		}
		panes := t.Tree.Panes()
		if len(panes) == 0 {
			continue
		}
		if len(going) > 0 {
			// A tab that lost panes underneath it comes back showing what is
			// left of it, not one survivor filling the window.
			t.Zoom = false
		}
		if t.Tree.Find(t.Focus) == nil {
			t.Focus = panes[0]
		}
		kept = append(kept, t)
	}
	w.Tabs = append(kept, rescued...)

	roots := make([]string, 0, len(w.openRoots))
	for _, r := range w.openRoots {
		if r != root {
			roots = append(roots, r)
		}
	}
	w.openRoots = roots

	if w.activeRoot == root {
		w.activeRoot = w.openRoots[0]
	}
	w.focusFirstTabOf(w.activeRoot)
	w.wake()
}

func (w *Workspace) isOpen(root string) bool {
	_, ok := w.openRootFor(root)
	return ok
}

// openRootFor finds the open project a path names and returns it as it was
// opened.
//
// The comparison ignores case, because the same directory reaches the
// application spelled several ways: from the command line, from the recent
// list, and from a drag onto the window. Matching by string alone opened a
// second copy of a project that was already there, with its own tabs and its
// own idea of the layout to save.
func (w *Workspace) openRootFor(root string) (string, bool) {
	// Almost every caller already holds the spelling the project was opened
	// with, and this runs once per pane on every redraw, so the exact match is
	// tried before cleaning and folding both paths.
	for _, r := range w.openRoots {
		if r == root {
			return r, true
		}
	}
	for _, r := range w.openRoots {
		if sameDir(r, root) {
			return r, true
		}
	}
	return "", false
}

// focusFirstTabOf moves focus to a tab of root, unless the focused tab already
// belongs to it.
//
// Which tab depends on whether the project has been visited before. Landing on
// the first tab every time is fine for a project of two and no help at all for
// one of ten: switching to another project to look something up and coming
// back would put you somewhere other than where you were working, with the
// tab you had open still to find.
func (w *Workspace) focusFirstTabOf(root string) {
	if t := w.CurrentTab(); t != nil && t.Root == root {
		return
	}
	w.rememberTab()
	if t := w.Tab(w.lastTab[root]); t != nil && t.Root == root {
		w.activeTab = t.ID
		return
	}
	w.activeTab = ""
	for _, t := range w.Tabs {
		if t.Root == root {
			w.activeTab = t.ID
			return
		}
	}
}

// VisibleTabs returns the tabs of the active project, in order.
func (w *Workspace) VisibleTabs() []*Tab {
	w.applyPendingTitles()
	return w.tabsOf(w.activeRoot)
}

func (w *Workspace) tabsOf(root string) []*Tab {
	out := make([]*Tab, 0, len(w.Tabs))
	for _, t := range w.Tabs {
		if t.Root == root {
			out = append(out, t)
		}
	}
	return out
}

// --------------------------------------------------------------------- tabs

// nameTabAfterPrompt asks for the tab holding a pane to be named after what
// was asked of it, unless the user has named it themselves.
//
// It is called from the hook server's goroutine, which must not walk the tab
// list: the interface adds and removes tabs on its own goroutine and without a
// lock, so reading the slice from here could see it half-replaced. The title is
// left on the workspace instead and picked up by applyPendingTitles.
func (w *Workspace) nameTabAfterPrompt(paneID, prompt string) {
	title := summarisePrompt(prompt)
	if title == "" {
		return
	}
	w.mu.Lock()
	if w.pendingTitles == nil {
		w.pendingTitles = map[string]string{}
	}
	w.pendingTitles[paneID] = title
	w.mu.Unlock()
	w.wake()
}

// applyPendingTitles moves requested titles onto their tabs. It is called at
// the top of the tab and project readers, which is where the interface always
// arrives before it draws, and so is the safe moment to change a tab.
func (w *Workspace) applyPendingTitles() {
	w.mu.RLock()
	n := len(w.pendingTitles)
	w.mu.RUnlock()
	if n == 0 {
		return
	}
	w.mu.Lock()
	pending := w.pendingTitles
	w.pendingTitles = nil
	w.mu.Unlock()

	for paneID, title := range pending {
		for _, t := range w.Tabs {
			if !t.AutoTitle || t.Tree.Find(paneID) == nil {
				continue
			}
			t.Title = title
			t.AutoTitle = false
			break
		}
	}
}

// summarisePrompt reduces a prompt to something that fits in a tab.
func summarisePrompt(prompt string) string {
	s := strings.Join(strings.Fields(prompt), " ")
	// Slash commands and the synthetic messages Claude records are not what
	// the tab should be called.
	if s == "" || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "<") {
		return ""
	}
	const limit = 28
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	// Stop at a word boundary if there is one worth using, so the tab reads as
	// the start of a sentence rather than breaking off mid-word. Fields has
	// already collapsed the whitespace, so a space is the only separator left.
	cut := limit
	for i := limit - 1; i > limit*2/3; i-- {
		if r[i] == ' ' {
			cut = i
			break
		}
	}
	return strings.TrimSpace(string(r[:cut])) + "…"
}

// tabHolding builds a tab in project root holding a single pane, named the way
// a pane pulled out into a tab of its own is named.
func (w *Workspace) tabHolding(paneID, root string) *Tab {
	p := w.Pane(paneID)
	if p == nil {
		return nil
	}
	title, auto := paneTabTitle(p)
	return &Tab{
		ID:        uuid.NewString(),
		Root:      root,
		Title:     title,
		Tree:      layout.NewLeaf(paneID),
		Focus:     paneID,
		AutoTitle: auto,
	}
}

// paneTabTitle names a tab that exists to hold one pane.
//
// A tab named after its directory tells the user nothing once several are
// open, and a pane ends up alone in a tab precisely when several are. What the
// agent was spawned to do names it far better; failing that, leave the tab
// open to being named by the next thing it is asked, exactly as a tab created
// from scratch would be.
func paneTabTitle(p *Pane) (title string, auto bool) {
	if title = summarisePrompt(p.Task); title != "" {
		return title, false
	}
	return p.Name, p.IsAgent()
}

// Pane returns the pane with the given id.
func (w *Workspace) Pane(id string) *Pane {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.panes[id]
}

// CurrentTab returns the active tab, or nil when there is none.
func (w *Workspace) CurrentTab() *Tab { return w.Tab(w.activeTab) }

// Tab returns the tab with the given id, or nil.
func (w *Workspace) Tab(id string) *Tab {
	for _, t := range w.Tabs {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// TabIDOf returns the id of the tab a pane is drawn in, or "" when the pane is
// not on screen. It is what a caller starting several panes into one tab needs
// after the first of them, since that first one is what created the tab.
func (w *Workspace) TabIDOf(paneID string) string {
	t := w.tabOf(paneID)
	if t == nil {
		return ""
	}
	return t.ID
}

// ActiveTabID returns the id of the focused tab.
func (w *Workspace) ActiveTabID() string { return w.activeTab }

// FocusedPane returns the pane with focus in the active tab.
func (w *Workspace) FocusedPane() *Pane {
	t := w.CurrentTab()
	if t == nil {
		return nil
	}
	return w.Pane(t.Focus)
}

// startPane launches a session for a pane description.
func (w *Workspace) startPane(p *Pane, resume bool) {
	// A pane that has been on screen knows what size it is; one starting for
	// the first time gets a conventional default until the viewer measures it.
	cols, rows := p.Cols, p.Rows
	if cols <= 0 || rows <= 0 {
		cols, rows = 80, 24
	}

	var argv, env []string
	if !p.IsAgent() {
		argv = session.ShellArgs()
		env = session.Env(w.paneEnv(p, "", "")...)
	} else {
		// An empty agent is not a missing one: it means whichever agent this
		// pane would be opened with today -- the project's default, or the
		// installation's -- which is how a layout written before panes could
		// choose still restores, and how a project that has settled on another
		// agent gets it without every pane having to record the name.
		agentID := p.Agent
		spec, ok := w.specFor(agentID)
		if !ok {
			// A layout can name an agent this machine has no entry for — one
			// removed from the user's agents.json, or a layout carried over
			// from a machine that had it. That is this pane's problem and no
			// other's, so it is reported in place.
			if agentID == "" {
				agentID = w.agents().DefaultsFor(w.activeRoot).Agent
			}
			p.Err = fmt.Errorf("no agent named %q is configured", agentID)
			return
		}
		// A CLI runner is somebody else's program and may simply not be on the
		// machine; an API runner is this binary, which is by definition here.
		if spec.Runner != agent.RunnerAPI && spec.Exe != "" {
			if _, err := exec.LookPath(spec.Exe); err != nil {
				p.Err = fmt.Errorf("the `%s` CLI was not found on PATH", spec.Exe)
				return
			}
		}
		// The model the pane was asked for, falling back to the agent's own
		// default. Both may be empty, which leaves the choice to the agent --
		// how a CLI keeps whatever it was already configured with.
		model := p.Model
		if model == "" {
			model = spec.DefaultModel
		}
		tokens := agent.Tokens{
			Session: p.ID,
			Model:   model,
			Cwd:     p.Cwd,
			// A pane's id is also its conversation id, so the two tokens hold
			// the same value here. They are separate because they answer
			// different questions — which conversation to reattach, and which
			// pane to report a lifecycle event against — and an agent whose
			// two ids are not the same thing would need them apart.
			Pane: p.ID,
		}
		// Only an agent that reports its own lifecycle has anything to do with
		// a settings file; for the rest the pane's status comes from watching
		// what it prints, and writing one would leave a file behind per pane
		// that nothing ever reads.
		if spec.Caps.Hooks {
			settings, err := session.WriteHookSettings(
				w.settingsDir, p.ID, w.selfExe, w.hookSrv.Endpoint(), w.hookSrv.Token())
			if err != nil {
				p.Err = err
				return
			}
			tokens.Settings = settings
		}
		// Resuming an agent that has no transcript for this session fails
		// immediately — `claude --resume` prints "No conversation found" and
		// exits — so a pane that was never prompted must start fresh instead.
		resuming := resume && spec.Caps.Resume && w.transcriptExists(spec, p.ID)
		if !resuming {
			// Handing the task over as an opening argument is far more
			// reliable than typing into the terminal, which would mean
			// guessing when the interface is ready to accept it.
			//
			// An agent with no hooks to answer is briefed here or nowhere, so
			// the briefing goes in front of the task. One with hooks is briefed
			// when it fires its first, and gets the task untouched.
			tokens.Prompt = w.OpeningPrompt(p.ID, p.initial, spec.Caps.Context)
		}
		argv = agent.BuildArgv(spec, resuming, tokens)
		extra := append([]string{}, spec.Env...)
		if spec.Runner == agent.RunnerAPI {
			// An API agent is Flockdeck's own chat client. BuildArgv leaves the
			// program off because only this side knows where the running
			// binary is.
			argv = append([]string{w.selfExe, "chat"}, argv...)
			// A key Flockdeck is keeping for this agent goes into the environment
			// of this pane and no other. One the user has already exported is
			// inherited and needs nothing added, so nothing is: a second copy
			// of a secret is a second place it can be read from.
			extra = append(extra, creds.Env(spec)...)
		}
		env = session.Env(append(extra, w.paneEnv(p, spec.ID, model)...)...)
	}

	s, err := session.Start(session.Config{
		ID:   p.ID,
		Kind: p.Kind,
		Name: p.Name,
		Cwd:  p.Cwd,
		Argv: argv,
		Env:  env,
		Cols: cols,
		Rows: rows,
	})
	if err != nil {
		// This is shown in the pane in place of a terminal, so it has to name
		// the directory: a worktree removed under a pane is the usual reason a
		// session will not start, and the error itself never says which one.
		p.Err = fmt.Errorf("%w (working directory %s)", err, p.Cwd)
		return
	}
	s.OnChange = w.wake
	// Lifecycle hooks read this field from the hook server's goroutine, so it
	// is only ever swapped under the lock.
	w.mu.Lock()
	p.Sess = s
	p.Err = nil
	w.mu.Unlock()
	// The opening prompt is spent; a later restart resumes instead.
	p.initial = ""
}

// paneEnv gives a pane what it needs to call back into the application, so an
// agent can spawn helpers of its own with `flockdeck spawn`.
//
// The agent id and model are passed in rather than read off the pane because
// the model a pane actually runs under is the one left after the agent's
// default has been applied, which is not always the one the pane recorded.
func (w *Workspace) paneEnv(p *Pane, agentID, model string) []string {
	if w.hookSrv == nil {
		return nil
	}
	vars := [][2]string{
		{"API", w.hookSrv.BaseURL()},
		{"TOKEN", w.hookSrv.Token()},
		{"PANE", p.ID},
		// Shell panes get no lifecycle hooks, so what is known at launch is put
		// in the environment, where a prompt or a script can read it.
		{"PANE_NAME", p.Name},
		{"PROJECT", w.rootOf(p.ID)},
	}
	// Both spellings go into the environment for now. The pane name and the
	// project are documented for the user's own shell prompt to read, so a
	// prompt written against the names an earlier build used would go blank on
	// upgrade with nothing on screen to say why. The old set can be dropped a
	// release after the rename.
	env := make([]string, 0, 2*len(vars)+2)
	for _, v := range vars {
		env = append(env, "FLOCKDECK_"+v[0]+"="+v[1], "PERCH_"+v[0]+"="+v[1])
	}
	// Which agent and model the pane is running only under the name in use
	// now: they are new, so there is no prompt or script written against an
	// older spelling of them to keep working. A shell pane is neither and is
	// told nothing, rather than being told it is an agent with no name.
	if agentID != "" {
		env = append(env, "FLOCKDECK_AGENT="+agentID)
	}
	if model != "" {
		env = append(env, "FLOCKDECK_MODEL="+model)
	}
	return env
}

// Agents returns the catalog this workspace runs from: every agent that is not
// hidden, and the id of the one a pane takes when nothing has chosen.
func (w *Workspace) Agents() ([]agent.Spec, string) {
	return w.agents().Visible(), w.defaultAgent()
}

// Catalog is the agents this workspace runs from, for a caller that needs more
// of it than Agents reports -- the defaults a project was given, and what the
// user's file had to say for itself. It is safe to call from any goroutine, so
// that probing a catalog full of agents need not happen on the one goroutine
// that owns the workspace.
func (w *Workspace) Catalog() *agent.Catalog { return w.agents() }

// UseAgent fixes which agent a new pane starts as for the rest of this run,
// which is what -agent asks for.
//
// It sits between the two answers that already exist: above the catalog's own
// defaults, because naming an agent on the command line is a more immediate
// statement than a file written weeks ago, and below a pane that names one
// itself, because a restored layout knows what it was.
func (w *Workspace) UseAgent(id string) {
	w.catalogMu.Lock()
	w.runAgent = id
	w.catalogMu.Unlock()
}

// defaultAgent is the agent a pane with nothing recorded on it runs.
func (w *Workspace) defaultAgent() string {
	w.catalogMu.RLock()
	run := w.runAgent
	w.catalogMu.RUnlock()
	if run != "" {
		return run
	}
	return w.agents().DefaultsFor(w.activeRoot).Agent
}

// ReloadAgents reads agents.json again, so that an edit made by hand takes
// effect without restarting. It also returns what the file had to say for
// itself, which is empty unless it could not be read.
func (w *Workspace) ReloadAgents() string {
	c := agent.Load()
	w.catalogMu.Lock()
	w.catalog = c
	w.catalogMu.Unlock()
	return c.Notice
}

// agents returns the catalog, reading it once if the workspace was built
// without one -- which is how every test that assembles a Workspace directly
// arrives here.
func (w *Workspace) agents() *agent.Catalog {
	w.catalogMu.RLock()
	c := w.catalog
	w.catalogMu.RUnlock()
	if c != nil {
		return c
	}
	c = agent.Load()
	w.catalogMu.Lock()
	if w.catalog == nil {
		w.catalog = c
	}
	c = w.catalog
	w.catalogMu.Unlock()
	return c
}

// specFor resolves the agent id recorded on a pane to the Spec that says how
// to start it, reporting whether the catalog has one. An empty id is the
// project's default rather than a missing answer, which is how a layout
// written before panes could choose still restores.
func (w *Workspace) specFor(id string) (agent.Spec, bool) {
	c := w.agents()
	if id == "" {
		id = w.defaultAgent()
	}
	return c.Find(id)
}

// transcriptExists reports whether the agent has a stored conversation for a
// session id, which is what decides whether resuming one is worth trying.
//
// This is the question the transcript Reader will answer for every agent. Only
// two answers are known here: an agent that records nothing has nothing to
// resume, and Claude's transcripts are where they have always been.
func (w *Workspace) transcriptExists(spec agent.Spec, sessionID string) bool {
	// Where an agent keeps what it said is the agent's own arrangement, so the
	// question goes to its reader. Asking Claude Code's store about every agent
	// answers "no" for each of them -- an API pane's record is in Flockdeck's own
	// state directory -- and a pane that has a conversation would be started
	// fresh on top of it, every restart, for as long as it existed.
	path := transcript.For(spec).Path(spec, sessionID)
	if path == "" {
		return false
	}
	// The file existing is not enough: a session interrupted before it recorded
	// anything leaves an empty one behind, and an agent asked to resume that
	// refuses it the same way it refuses a missing one.
	fi, err := os.Stat(path)
	return err == nil && fi.Size() > 0
}

// RootOf reports the project a pane belongs to, for callers outside the
// package that need to name it rather than the tab it is drawn on.
func (w *Workspace) RootOf(paneID string) string { return w.rootOf(paneID) }

// rootOf reports the project a pane belongs to.
//
// The pane's own project is the answer wherever it has one: a tab may show
// agents from several projects, so the tab it happens to sit on no longer says
// which project an agent is working in. The tab is still the fallback for a
// pane restored from a layout written before panes recorded their own.
func (w *Workspace) rootOf(paneID string) string {
	if p := w.Pane(paneID); p != nil && p.Root != "" {
		// Under the spelling the project is open with. A pane restored from a
		// layout carries the spelling that was saved, and everything that
		// groups tabs and panes by project — which tabs are shown, which tabs
		// belong to what — compares those strings directly.
		if open, ok := w.openRootFor(p.Root); ok {
			return open
		}
		return p.Root
	}
	if t := w.tabOf(paneID); t != nil {
		return t.Root
	}
	return w.activeRoot
}

// projectFor reports the open project a directory belongs to: the innermost
// one containing it, so a pane put into a worktree nested inside a project is
// counted against that project rather than against whichever one happens to be
// on screen. A directory under no open project belongs to the active one.
func (w *Workspace) projectFor(cwd string) string {
	best := ""
	for _, r := range w.openRoots {
		if underDir(cwd, r) && len(r) > len(best) {
			best = r
		}
	}
	if best == "" {
		return w.activeRoot
	}
	return best
}

// Choice is what a new pane should run: a shell, or an agent and one of its
// models. An empty Agent means the default one, and an empty Model whatever
// that agent is already set to, so a caller with nothing to say passes the
// kind alone and gets exactly what one keystroke has always given.
type Choice struct {
	Kind  session.Kind
	Agent string
	Model string
}

// newPane creates and starts a pane, registering it in the workspace. An empty
// root works the project out from the directory.
func (w *Workspace) newPane(c Choice, cwd, name, root string) *Pane {
	if cwd == "" {
		cwd = w.activeRoot
	}
	if name == "" {
		name = filepath.Base(cwd)
	}
	if root == "" {
		root = w.projectFor(cwd)
	}
	p := &Pane{
		ID: uuid.NewString(), Kind: c.Kind, Cwd: cwd, Name: name, Root: root,
		Branch: branchOf(cwd),
	}
	// A shell runs no agent, so it is never given one to remember: a kind and
	// an agent that disagreed would be written to the layout and read back as
	// a pane that is somehow both.
	if p.IsAgent() {
		p.Agent, p.Model = c.Agent, c.Model
	}
	w.mu.Lock()
	w.panes[p.ID] = p
	w.mu.Unlock()
	w.startPane(p, false)
	return p
}

// spawnCommand is the command an agent is told to run to start a helper.
//
// It is `flockdeck` when that is on PATH, which is how an installed copy is
// reached. It is this binary's own path when it is not: a build that has not
// been installed still serves panes that can spawn, and an agent told to run a
// command that is not there has no way of finding that out but to try it.
func spawnCommand(selfExe string) string {
	if _, err := exec.LookPath("flockdeck"); err == nil {
		return "flockdeck"
	}
	if selfExe == "" {
		return "flockdeck"
	}
	return selfExe
}

// branchOf reports the branch checked out in dir, or "" when dir is not a
// repository or git is unavailable.
func branchOf(dir string) string {
	if !gitx.Available() {
		return ""
	}
	return gitx.CurrentBranch(dir)
}

// NewTab appends a tab to the active project and focuses it.
//
// The tab belongs to the active project, since that is the tab bar it is drawn
// on, but the pane belongs to whichever open project its directory is in — the
// same rule a split follows. Opening a checkout that is itself open as a
// project, which is how a worktree usually reaches a new tab, otherwise gave an
// agent working in one project every reason to think it was in another.
func (w *Workspace) NewTab(kind session.Kind, cwd, title string) *Tab {
	return w.NewTabWith(Choice{Kind: kind}, cwd, title)
}

// NewTabWith is NewTab with the agent and model to run in the new tab's pane
// named, which is what the agent picker asks for.
func (w *Workspace) NewTabWith(c Choice, cwd, title string) *Tab {
	p := w.newPane(c, cwd, "", "")
	// A tab with no title of its own is named after the directory for now, and
	// renames itself when the agent is first asked something.
	autoTitle := title == "" && p.IsAgent()
	if title == "" {
		title = p.Name
	}
	t := &Tab{
		ID:        uuid.NewString(),
		Root:      w.activeRoot,
		Title:     title,
		Tree:      layout.NewLeaf(p.ID),
		Focus:     p.ID,
		AutoTitle: autoTitle,
	}
	w.Tabs = append(w.Tabs, t)
	w.activeTab = t.ID
	return t
}

// CloseTab closes a tab and every session in it.
func (w *Workspace) CloseTab(id string) {
	closing := w.Tab(id)
	if closing == nil {
		return
	}
	// Note the position among its own project's tabs before removing it, so
	// focus can land on a neighbour rather than jumping to another project.
	siblings := w.tabsOf(closing.Root)
	pos := 0
	for i, t := range siblings {
		if t.ID == id {
			pos = i
		}
	}

	for _, pid := range closing.Tree.Panes() {
		w.destroyPane(pid)
	}
	kept := make([]*Tab, 0, len(w.Tabs))
	for _, t := range w.Tabs {
		if t.ID != id {
			kept = append(kept, t)
		}
	}
	w.Tabs = kept

	if w.activeTab != id {
		return
	}
	w.activeTab = ""
	remaining := w.tabsOf(closing.Root)
	if len(remaining) > 0 {
		if pos >= len(remaining) {
			pos = len(remaining) - 1
		}
		w.activeTab = remaining[pos].ID
	}
}

// destroyPane terminates and forgets a pane.
func (w *Workspace) destroyPane(id string) {
	w.mu.Lock()
	p := w.panes[id]
	delete(w.panes, id)
	delete(w.BroadcastSet, id)
	w.mu.Unlock()
	if p != nil && p.Sess != nil {
		_ = p.Sess.Close()
	}
	// The generated settings file is only meaningful while the pane lives.
	_ = os.Remove(filepath.Join(w.settingsDir, id+".settings.json"))
}

// rememberTab notes which tab the project on screen is being left on, so that
// coming back to it comes back there. Everything that moves to another project
// calls it before the focus moves, since afterwards the tab being left is no
// longer the one on screen and there is nothing left to ask.
func (w *Workspace) rememberTab() {
	t := w.CurrentTab()
	if t == nil {
		return
	}
	if w.lastTab == nil {
		w.lastTab = map[string]string{}
	}
	w.lastTab[t.Root] = t.ID
}

// SelectTab focuses a tab, switching project if it belongs to another.
func (w *Workspace) SelectTab(id string) {
	t := w.Tab(id)
	if t == nil {
		return
	}
	if t.Root != w.activeRoot && w.isOpen(t.Root) {
		w.rememberTab()
		w.activeRoot = t.Root
	}
	w.activeTab = id
}

// NextTab and PrevTab cycle through the active project's tabs, wrapping.
func (w *Workspace) NextTab() { w.cycleTab(1) }

// PrevTab moves to the previous tab.
func (w *Workspace) PrevTab() { w.cycleTab(-1) }

func (w *Workspace) cycleTab(delta int) {
	tabs := w.VisibleTabs()
	if len(tabs) == 0 {
		return
	}
	cur := 0
	for i, t := range tabs {
		if t.ID == w.activeTab {
			cur = i
		}
	}
	w.activeTab = tabs[((cur+delta)%len(tabs)+len(tabs))%len(tabs)].ID
}

// -------------------------------------------------------------------- panes

// SplitPane splits the focused pane, starting a new session beside it in the
// same directory.
func (w *Workspace) SplitPane(dir layout.Dir, kind session.Kind) {
	w.SplitPaneIn(dir, kind, "")
}

// SplitPaneWith is SplitPane with the agent and model to run named, which is
// what the agent picker asks for.
func (w *Workspace) SplitPaneWith(dir layout.Dir, c Choice) {
	w.splitPaneIn(dir, c, "", "")
}

// SplitPaneInProject splits the focused pane and starts the new session in
// another open project, which is how two projects come to be worked on side by
// side in one tab. An unknown or empty project falls back to an ordinary
// split.
func (w *Workspace) SplitPaneInProject(dir layout.Dir, kind session.Kind, root string) {
	w.SplitPaneInProjectWith(dir, Choice{Kind: kind}, root)
}

// SplitPaneInProjectWith is SplitPaneInProject with the agent and model named.
func (w *Workspace) SplitPaneInProjectWith(dir layout.Dir, c Choice, root string) {
	open, ok := w.openRootFor(root)
	if !ok {
		w.splitPaneIn(dir, c, "", "")
		return
	}
	w.splitPaneIn(dir, c, open, open)
}

// SplitPaneIn splits the focused pane, starting the new session in cwd. An
// empty cwd inherits the focused pane's directory, which is what a plain split
// should do; the worktree panel passes a directory to put an agent straight
// into another checkout.
func (w *Workspace) SplitPaneIn(dir layout.Dir, kind session.Kind, cwd string) {
	w.splitPaneIn(dir, Choice{Kind: kind}, cwd, "")
}

// SplitPaneInWith is SplitPaneIn with the agent and model named.
func (w *Workspace) SplitPaneInWith(dir layout.Dir, c Choice, cwd string) {
	w.splitPaneIn(dir, c, cwd, "")
}

// splitPaneIn is the whole of the split, with the project the new pane belongs
// to given separately from its directory: the two differ when an agent is put
// into a worktree, which sits under the project it was made from.
func (w *Workspace) splitPaneIn(dir layout.Dir, c Choice, cwd, root string) {
	t := w.CurrentTab()
	if t == nil {
		return
	}
	if cwd == "" {
		cwd = t.Root
		// A plain split inherits the focused pane's directory, and with it the
		// project that pane belongs to, so splitting a borrowed pane gives
		// another pane of the same project rather than of the tab's.
		if p := w.Pane(t.Focus); p != nil {
			cwd = p.Cwd
			if root == "" {
				root = p.Root
			}
		}
	}
	np := w.newPane(c, cwd, "", root)
	if !t.Tree.Split(t.Focus, np.ID, dir) {
		w.destroyPane(np.ID)
		return
	}
	t.Focus = np.ID
	t.Zoom = false
}

// ClosePane closes the focused pane. Closing the last pane in a tab closes the
// tab.
func (w *Workspace) ClosePane() {
	t := w.CurrentTab()
	if t == nil {
		return
	}
	id := t.Focus
	if t.Tree.Count() <= 1 {
		w.CloseTab(t.ID)
		return
	}
	// Choose the pane to focus next before mutating the tree.
	next := paneBesideTheGap(t, id)

	t.Tree.Remove(id)
	w.destroyPane(id)
	if next == "" {
		if panes := t.Tree.Panes(); len(panes) > 0 {
			next = panes[0]
		}
	}
	t.Focus = next
	t.Zoom = false
}

// RestartPane relaunches the focused pane's process. Claude panes resume the
// same conversation when there is one.
func (w *Workspace) RestartPane() {
	p := w.FocusedPane()
	if p == nil {
		return
	}
	if p.Sess != nil {
		_ = p.Sess.Close()
		w.mu.Lock()
		p.Sess = nil
		w.mu.Unlock()
	}
	w.startPane(p, p.IsAgent())
	w.wake()
}

// FocusDir moves focus to the adjacent pane in a direction.
func (w *Workspace) FocusDir(dir layout.Direction) {
	t := w.CurrentTab()
	if t == nil || t.Zoom {
		return
	}
	computeTab(t)
	if next := t.Tree.Neighbor(t.Focus, dir); next != "" {
		t.Focus = next
	}
}

// FocusPane focuses a pane by id if it is in the active tab.
func (w *Workspace) FocusPane(id string) {
	t := w.CurrentTab()
	if t == nil || id == "" {
		return
	}
	if t.Tree.Find(id) != nil {
		t.Focus = id
	}
}

// CyclePane moves focus to the next pane in tree order.
func (w *Workspace) CyclePane() {
	t := w.CurrentTab()
	if t == nil {
		return
	}
	panes := t.Tree.Panes()
	for i, id := range panes {
		if id == t.Focus {
			t.Focus = panes[(i+1)%len(panes)]
			return
		}
	}
}

// ToggleZoom expands or restores the focused pane.
func (w *Workspace) ToggleZoom() {
	if t := w.CurrentTab(); t != nil {
		t.Zoom = !t.Zoom
	}
}

// ResizePaneTerminal records the terminal size the viewer measured for a pane
// and applies it to the PTY. Geometry is decided by the browser, which knows
// the font metrics, so the server only forwards the result.
func (w *Workspace) ResizePaneTerminal(id string, cols, rows int) {
	p := w.Pane(id)
	if p == nil || cols <= 0 || rows <= 0 {
		return
	}
	p.Cols, p.Rows = cols, rows
	if p.Sess != nil {
		p.Sess.Resize(cols, rows)
	}
}

// ---------------------------------------------------------------- broadcast

// ToggleBroadcast turns broadcast mode on or off.
//
// With no selection of the user's own, broadcast means every Claude pane in
// the tab in front of you. That default is not written down as a set of panes:
// it is answered from whichever tab is on screen at the time, so switching
// tabs with broadcast still on broadcasts to the tab you are now looking at
// rather than to the one you switched it on in — where it would have gone to
// panes that are not on screen, which looks exactly like broadcast having
// stopped working.
func (w *Workspace) ToggleBroadcast() {
	w.Broadcast = !w.Broadcast
	w.mu.Lock()
	defer w.mu.Unlock()
	// Turning it off drops the default; a selection the user made by hand is
	// theirs and stays.
	w.broadcastAuto = w.Broadcast && len(w.BroadcastSet) == 0
}

// ToggleBroadcastMember adds or removes the focused pane from the broadcast set.
func (w *Workspace) ToggleBroadcastMember() {
	t := w.CurrentTab()
	if t == nil {
		return
	}
	// Changing the default turns it into a selection, and the selection starts
	// as what the default covered: removing one pane from it has to leave the
	// others in. Membership is worked out before the lock, since the pane
	// lookups take the same one.
	var adopt []string
	if w.broadcastAuto {
		adopt = w.autoBroadcastMembers(t)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, id := range adopt {
		w.BroadcastSet[id] = true
	}
	if w.BroadcastSet[t.Focus] {
		delete(w.BroadcastSet, t.Focus)
	} else {
		w.BroadcastSet[t.Focus] = true
	}
	// The selection is the user's now, so it survives broadcast being turned
	// off and on again.
	w.broadcastAuto = false
}

// autoBroadcastMembers lists the panes the default set covers in a tab.
func (w *Workspace) autoBroadcastMembers(t *Tab) []string {
	var out []string
	for _, id := range t.Tree.Panes() {
		if p := w.Pane(id); p != nil && p.IsAgent() {
			out = append(out, id)
		}
	}
	return out
}

// InBroadcast reports whether a pane receives broadcast input.
func (w *Workspace) InBroadcast(id string) bool {
	if w.broadcastAuto {
		t := w.CurrentTab()
		if t == nil || t.Tree.Find(id) == nil {
			return false
		}
		p := w.Pane(id)
		return p != nil && p.IsAgent()
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.BroadcastSet[id]
}

// BroadcastTargets returns the panes that should receive broadcast input in
// the active tab, always including the focused pane.
func (w *Workspace) BroadcastTargets() []*Pane {
	t := w.CurrentTab()
	if t == nil {
		return nil
	}
	var out []*Pane
	seen := map[string]bool{}
	for _, id := range t.Tree.Panes() {
		if !w.InBroadcast(id) && id != t.Focus {
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		if p := w.Pane(id); p != nil && p.Alive() {
			out = append(out, p)
		}
	}
	return out
}

// SendPrompt types text into every broadcast target and submits it.
func (w *Workspace) SendPrompt(text string, submit bool) {
	for _, p := range w.BroadcastTargets() {
		_ = p.Sess.WriteString(text)
		if submit {
			_ = p.Sess.WriteString("\r")
		}
	}
}

// ---------------------------------------------------------------- attention

// AttentionCount returns how many panes across every open project are waiting
// on the user, and how many are actively working.
func (w *Workspace) AttentionCount() (waiting, working int) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	for _, p := range w.panes {
		if p.Sess == nil {
			continue
		}
		switch st, _ := p.Sess.Status(); st {
		case session.StatusWaiting:
			waiting++
		case session.StatusWorking:
			working++
		}
	}
	return waiting, working
}

// RefreshGit updates every pane's git summary.
//
// The git calls are made off the caller's goroutine and only the results are
// applied, so a slow repository cannot stall the interface. apply is invoked
// with the collected results and must run where mutating panes is safe.
func (w *Workspace) RefreshGit(apply func(func())) {
	if !gitx.Available() {
		return
	}
	// One status call per distinct directory, not per pane: several panes
	// commonly share a checkout.
	var cwds []string
	seen := map[string]bool{}
	w.mu.RLock()
	for _, p := range w.panes {
		if p.Cwd == "" || seen[p.Cwd] {
			continue
		}
		seen[p.Cwd] = true
		cwds = append(cwds, p.Cwd)
	}
	w.mu.RUnlock()
	if len(cwds) == 0 {
		return
	}

	dirs := make(map[string]gitx.Status, len(cwds))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, cwd := range cwds {
		wg.Add(1)
		go func(cwd string) {
			defer wg.Done()
			st := gitx.StatusOf(cwd)
			mu.Lock()
			dirs[cwd] = st
			mu.Unlock()
		}(cwd)
	}
	wg.Wait()

	apply(func() {
		changed := false
		w.mu.Lock()
		for _, p := range w.panes {
			st, ok := dirs[p.Cwd]
			if !ok || p.Git == st {
				continue
			}
			p.Git = st
			if st.Branch != "" && st.Branch != p.Branch {
				p.Branch = st.Branch
			}
			changed = true
		}
		w.mu.Unlock()
		if changed {
			w.wake()
		}
	})
}

// TabNeedsAttention reports whether any pane in a tab is waiting on the user.
func (w *Workspace) TabNeedsAttention(t *Tab) bool {
	for _, id := range t.Tree.Panes() {
		if p := w.Pane(id); p != nil {
			if st, _ := p.Status(); st == session.StatusWaiting {
				return true
			}
		}
	}
	return false
}

// Close terminates every session and stops the hook server.
func (w *Workspace) Close() {
	for _, t := range w.Tabs {
		for _, id := range t.Tree.Panes() {
			w.destroyPane(id)
		}
	}
	// Anything still registered belongs to no tab: a pane whose split was
	// rejected, or one left over from a tab that was rebuilt. Its process is
	// just as real, so it is shut down rather than outliving the window.
	w.mu.RLock()
	orphans := make([]string, 0, len(w.panes))
	for id := range w.panes {
		orphans = append(orphans, id)
	}
	w.mu.RUnlock()
	for _, id := range orphans {
		w.destroyPane(id)
	}
	if w.hookSrv != nil {
		_ = w.hookSrv.Close()
	}
}

// Conversations lists the stored Claude conversations for the active project.
func (w *Workspace) Conversations(cwd string) ([]session.Conversation, error) {
	if cwd == "" {
		cwd = w.activeRoot
	}
	return session.Conversations(cwd)
}

// OpenConversation opens a stored conversation in a new tab.
//
// The pane takes the conversation's own id, which is what makes `--resume`
// reattach to it and what lets the layout remember it afterwards. A
// conversation that is already open is focused instead of being started twice:
// two panes on one conversation would fight over the same transcript.
func (w *Workspace) OpenConversation(id, cwd, title string) error {
	if id == "" {
		return fmt.Errorf("no conversation given")
	}
	if existing := w.Pane(id); existing != nil {
		for _, t := range w.Tabs {
			if t.Tree.Find(id) != nil {
				w.SelectTab(t.ID)
				t.Focus = id
				w.wake()
				return nil
			}
		}
		// Registered but on screen nowhere. Reusing the id below would drop it
		// from the registry with its process still running, so end it first.
		w.destroyPane(id)
	}
	if cwd == "" {
		cwd = w.activeRoot
	}
	if !session.ConversationExists(id) {
		return fmt.Errorf("that conversation is no longer stored")
	}

	p := &Pane{
		ID:   id,
		Kind: session.KindClaude,
		// The conversation being reopened is one Claude recorded, so the pane
		// has to run Claude to reattach to it, whichever agent this project
		// opens new panes with.
		Agent:  "claude",
		Cwd:    cwd,
		Name:   filepath.Base(cwd),
		Root:   w.projectFor(cwd),
		Branch: branchOf(cwd),
	}
	w.mu.Lock()
	w.panes[p.ID] = p
	w.mu.Unlock()
	w.startPane(p, true)

	if title == "" {
		title = "resumed"
	}
	t := &Tab{
		ID:    uuid.NewString(),
		Root:  w.activeRoot,
		Title: title,
		Tree:  layout.NewLeaf(p.ID),
		Focus: p.ID,
	}
	w.Tabs = append(w.Tabs, t)
	w.activeTab = t.ID
	w.wake()
	return nil
}

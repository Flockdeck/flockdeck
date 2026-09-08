// Package workspace holds the mutable state of the application: the projects
// that are open, the tabs in each, the pane tree in each tab, and the live
// sessions behind those panes.
//
// It deliberately knows nothing about rendering or key bindings, so the layout
// and session lifecycle can be exercised without a user interface.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmwri/agent-wrapper/internal/gitx"
	"github.com/jmwri/agent-wrapper/internal/hooks"
	"github.com/jmwri/agent-wrapper/internal/layout"
	"github.com/jmwri/agent-wrapper/internal/session"
	"github.com/jmwri/agent-wrapper/internal/store"
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

	// openRoots are the projects currently open, in the order they were
	// opened; activeRoot is the one being shown.
	openRoots  []string
	activeRoot string
	activeTab  string

	selfExe     string
	settingsDir string
	hookSrv     *hooks.Server
	claudeExe   string

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
	w.mu.RLock()
	p := w.panes[ev.SessionID]
	w.mu.RUnlock()
	if p == nil || p.Sess == nil {
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
	p.Sess.SetStatus(st, detail)
}

// ----------------------------------------------------------------- projects

// Projects returns the open projects with a summary of each.
func (w *Workspace) Projects() []Project {
	out := make([]Project, 0, len(w.openRoots))
	for _, root := range w.openRoots {
		p := Project{Root: root, Name: filepath.Base(root), Active: root == w.activeRoot}
		for _, t := range w.Tabs {
			if t.Root != root {
				continue
			}
			p.Tabs++
			for _, id := range t.Tree.Panes() {
				pane := w.Pane(id)
				if pane == nil {
					continue
				}
				switch st, _ := pane.Status(); st {
				case session.StatusWaiting:
					p.Waiting++
				case session.StatusWorking:
					p.Working++
				}
			}
		}
		out = append(out, p)
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
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", root)
	}

	if w.isOpen(root) {
		w.SelectProject(root)
		return nil
	}

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
	if !w.isOpen(root) {
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
	if len(w.openRoots) <= 1 || !w.isOpen(root) {
		return
	}
	_ = w.SaveProject(root)

	kept := make([]*Tab, 0, len(w.Tabs))
	for _, t := range w.Tabs {
		if t.Root == root {
			for _, id := range t.Tree.Panes() {
				w.destroyPane(id)
			}
			continue
		}
		kept = append(kept, t)
	}
	w.Tabs = kept

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
	for _, r := range w.openRoots {
		if r == root {
			return true
		}
	}
	return false
}

// focusFirstTabOf moves focus to a tab belonging to root, unless the focused
// tab already does.
func (w *Workspace) focusFirstTabOf(root string) {
	if t := w.CurrentTab(); t != nil && t.Root == root {
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
func (w *Workspace) VisibleTabs() []*Tab { return w.tabsOf(w.activeRoot) }

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

// nameTabAfterPrompt titles the tab holding a pane after what was asked of it,
// unless the user has named it themselves.
func (w *Workspace) nameTabAfterPrompt(paneID, prompt string) {
	title := summarisePrompt(prompt)
	if title == "" {
		return
	}
	for _, t := range w.Tabs {
		if !t.AutoTitle || t.Tree.Find(paneID) == nil {
			continue
		}
		t.Title = title
		t.AutoTitle = false
		w.wake()
		return
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
	switch p.Kind {
	case session.KindClaude:
		if w.claudeExe == "" {
			p.Err = fmt.Errorf("the `claude` CLI was not found on PATH")
			return
		}
		settings, err := session.WriteHookSettings(
			w.settingsDir, p.ID, w.selfExe, w.hookSrv.Endpoint(), w.hookSrv.Token())
		if err != nil {
			p.Err = err
			return
		}
		// Resuming a session Claude has no transcript for fails immediately, so
		// a pane that was never prompted must start fresh instead.
		resuming := resume && session.ConversationExists(p.ID)
		var extra []string
		if !resuming && p.initial != "" {
			// Handing the task to Claude as its opening argument is far more
			// reliable than typing into the terminal, which would mean guessing
			// when the interface is ready to accept it.
			extra = []string{p.initial}
		}
		argv = session.ClaudeArgs(p.ID, settings, resuming, extra)
		env = session.Env(w.paneEnv(p)...)
	default:
		argv = session.ShellArgs()
		env = session.Env(w.paneEnv(p)...)
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
		p.Err = err
		return
	}
	s.OnChange = w.wake
	p.Sess = s
	p.Err = nil
	// The opening prompt is spent; a later restart resumes instead.
	p.initial = ""
}

// paneEnv gives a pane what it needs to call back into the application, so an
// agent can spawn helpers of its own with `agent-wrapper spawn`.
func (w *Workspace) paneEnv(p *Pane) []string {
	if w.hookSrv == nil {
		return nil
	}
	return []string{
		"AGENT_WRAPPER_API=" + w.hookSrv.BaseURL(),
		"AGENT_WRAPPER_TOKEN=" + w.hookSrv.Token(),
		"AGENT_WRAPPER_PANE=" + p.ID,
		// Shell panes get no lifecycle hooks, so what is known at launch is put
		// in the environment, where a prompt or a script can read it.
		"AGENT_WRAPPER_PANE_NAME=" + p.Name,
		"AGENT_WRAPPER_PROJECT=" + w.rootOf(p.ID),
	}
}

// rootOf reports the project a pane belongs to, falling back to the active one
// for a pane that has not been placed in a tab yet.
func (w *Workspace) rootOf(paneID string) string {
	if t := w.tabOf(paneID); t != nil {
		return t.Root
	}
	return w.activeRoot
}

// newPane creates and starts a pane, registering it in the workspace.
func (w *Workspace) newPane(kind session.Kind, cwd, name string) *Pane {
	if cwd == "" {
		cwd = w.activeRoot
	}
	if name == "" {
		name = filepath.Base(cwd)
	}
	p := &Pane{ID: uuid.NewString(), Kind: kind, Cwd: cwd, Name: name, Branch: branchOf(cwd)}
	w.mu.Lock()
	w.panes[p.ID] = p
	w.mu.Unlock()
	w.startPane(p, false)
	return p
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
func (w *Workspace) NewTab(kind session.Kind, cwd, title string) *Tab {
	p := w.newPane(kind, cwd, "")
	// A tab with no title of its own is named after the directory for now, and
	// renames itself when the agent is first asked something.
	autoTitle := title == "" && kind == session.KindClaude
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

// SelectTab focuses a tab, switching project if it belongs to another.
func (w *Workspace) SelectTab(id string) {
	t := w.Tab(id)
	if t == nil {
		return
	}
	w.activeTab = id
	if t.Root != w.activeRoot && w.isOpen(t.Root) {
		w.activeRoot = t.Root
	}
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

// SplitPaneIn splits the focused pane, starting the new session in cwd. An
// empty cwd inherits the focused pane's directory, which is what a plain split
// should do; the worktree panel passes a directory to put an agent straight
// into another checkout.
func (w *Workspace) SplitPaneIn(dir layout.Dir, kind session.Kind, cwd string) {
	t := w.CurrentTab()
	if t == nil {
		return
	}
	if cwd == "" {
		cwd = t.Root
		if p := w.Pane(t.Focus); p != nil {
			cwd = p.Cwd
		}
	}
	np := w.newPane(kind, cwd, "")
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
	// Choose the pane to focus next before mutating the tree. Neighbour lookup
	// is geometric, so the tree needs its rectangles first.
	computeTab(t)
	next := ""
	for _, dir := range []layout.Direction{layout.Right, layout.Left, layout.Down, layout.Up} {
		if next = t.Tree.Neighbor(id, dir); next != "" {
			break
		}
	}

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
		p.Sess = nil
	}
	w.startPane(p, p.Kind == session.KindClaude)
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
func (w *Workspace) ToggleBroadcast() {
	w.Broadcast = !w.Broadcast
	if !w.Broadcast {
		// A set nobody picked describes the tab it was built from and nothing
		// else. Keeping it would leave the next tab broadcasting to panes that
		// are not in it, which looks like broadcast doing nothing at all.
		if w.broadcastAuto {
			w.mu.Lock()
			clear(w.BroadcastSet)
			w.broadcastAuto = false
			w.mu.Unlock()
		}
		return
	}
	t := w.CurrentTab()
	if t == nil {
		return
	}
	// Which panes would be selected is decided before taking the lock: pane
	// lookups take the same one.
	var claude []string
	for _, id := range t.Tree.Panes() {
		if p := w.Pane(id); p != nil && p.Kind == session.KindClaude {
			claude = append(claude, id)
		}
	}
	// The set is read and written under the lock everywhere else — a closing
	// pane removes itself from it — so this must hold it too.
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.BroadcastSet) > 0 {
		return
	}
	// With no explicit selection, broadcasting to every Claude pane in the tab
	// is the useful default.
	for _, id := range claude {
		w.BroadcastSet[id] = true
	}
	w.broadcastAuto = len(claude) > 0
}

// ToggleBroadcastMember adds or removes the focused pane from the broadcast set.
func (w *Workspace) ToggleBroadcastMember() {
	t := w.CurrentTab()
	if t == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.BroadcastSet[t.Focus] {
		delete(w.BroadcastSet, t.Focus)
	} else {
		w.BroadcastSet[t.Focus] = true
	}
	// The selection is the user's now, so it survives broadcast being turned
	// off and on again.
	w.broadcastAuto = false
}

// InBroadcast reports whether a pane receives broadcast input.
func (w *Workspace) InBroadcast(id string) bool {
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
		ID:     id,
		Kind:   session.KindClaude,
		Cwd:    cwd,
		Name:   filepath.Base(cwd),
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

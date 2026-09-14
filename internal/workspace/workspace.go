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
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/creds"
	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/layout"
	"github.com/jmwri/flockdeck/internal/review"
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
	// asked for. Both are empty on a shell pane. A new agent pane records what
	// its choice resolved to, so a restart and a saved layout come back as the
	// same agent; an empty Agent, which only a layout written before panes
	// could choose holds, means the catalog's default. An empty Model means
	// whatever the agent is already set to, which is not the same as naming
	// its default model.
	Agent string
	Model string
	// Routed names the routing rule that chose Model, and RoutedFrom the
	// model the pane would otherwise have run. Both are empty for a model
	// chosen by hand or by default. RoutedFromAgent names the agent
	// RoutedFrom belongs to, and is set only when routing moved the pane to
	// another agent, not only another model.
	Routed          string
	RoutedFrom      string
	RoutedFromAgent string
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
	// GitTimedOut is set while the last refresh of the checkout gave up before
	// git answered. Git then still holds what was read before, which is kept
	// for when git answers again but must not be shown as though it were
	// current: nothing says it still is.
	GitTimedOut bool

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
	// Conversation is the agent's own id for the conversation the pane is in,
	// once that is no longer the pane's id; empty means the pane's id. It is
	// set from the hook goroutine, so it is only touched under the lock — read
	// it through conversationOf.
	Conversation string
	// launch names the current start of the pane's process, which the pane's
	// environment carries as hooks.LaunchEnv and its hooks send back. A hook
	// naming another is from the process before a restart, and is dropped. It
	// is read from the hook goroutine, so it is only written under the lock.
	launch string
	// Parent is the pane whose agent started this one with its own `flockdeck
	// spawn`, so this pane is that agent's helper rather than the user's own.
	// Empty for a pane the user started themselves -- opened by hand, or by a
	// fan-out run from the window. It is never cleared once set, even once
	// that pane closes; a reader wanting to know whether the parent is still
	// open checks separately, as internal/server's agents.go does.
	//
	// It is persisted with the layout, so a restored helper still knows whose
	// it is; see encodeNode and decodeNode.
	Parent string
	// Muted is set from a phone that would rather not be pushed to about this
	// pane's waits while it is chatty, though it still shows waiting
	// everywhere else -- the desk's markers, the phone's list, and the desk's
	// own notification. See SetPaneMuted and push.go's pushDue, which is the
	// only thing that reads it.
	//
	// It is not persisted, so it does not survive a restart of Flockdeck, and
	// there is nowhere it needs clearing when the pane closes: the Pane it
	// lives on is simply gone.
	Muted bool
	// AutoReview opts this pane into auto-review approvals: a PreToolUse call
	// is put to reviewTool before Claude Code would otherwise show its own
	// permission prompt, and one the reviewer is confident is safe is let
	// through without ever reaching that prompt or turning the pane amber.
	// See SetPaneAutoReview.
	//
	// It answers only what is already going to be asked about -- Claude
	// Code's own permission settings are never touched, and nothing here can
	// ever say "deny" -- so turning it off simply goes back to asking about
	// everything, exactly as every pane without it does today.
	//
	// It defaults to off, like every pane's, but a pane a fan-out or `flockdeck
	// spawn` starts is the exception: it is given its parent's AutoReview, on
	// or off, so a dozen children of a pane a person already trusted do not
	// each have to be found and switched on by hand. See Spawn.
	//
	// Like Muted it is not persisted: a restart starts over asking about
	// everything until the user turns it back on.
	AutoReview bool
	// AutoApproved counts the calls auto-review has let through for this pane
	// without a prompt, so a person who turned it on has something to see for
	// it. It is not persisted either.
	AutoApproved int
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
	// Named is set while the tab carries a name chosen by hand. The title it
	// would otherwise have goes on being kept behind that name, in auto and
	// autoOpen, so that giving the name up puts back what the tab would be
	// called had it never been renamed: renaming used to clear AutoTitle for
	// good, and a tab named once could never go back.
	Named bool
	// auto is that title while Named is set, and autoOpen says whether it is
	// still the directory name waiting for the first prompt to replace it —
	// AutoTitle, for a title that is not on show. An empty auto is not known,
	// which is a tab named in a layout written before it was kept.
	auto     string
	autoOpen bool
}

// Project is an open project and a summary of what is happening inside it.
type Project struct {
	Root    string
	Name    string
	Active  bool
	Tabs    int
	Waiting int
	Working int
	// Panes counts every pane in the project's tabs, idle ones included, so
	// that a split or a closed pane changes the summary as it changes the
	// agents list.
	Panes int
}

// Workspace is the whole application state.
type Workspace struct {
	// Tabs holds every tab across every open project, in creation order.
	Tabs []*Tab

	// Broadcast, when enabled, sends what is written in the prompt bar to every
	// pane in the broadcast set as well as the focused one; with it off the
	// prompt bar reaches the focused pane and any panes picked by hand, which
	// stay picked. Typing into a pane's own terminal is never copied anywhere.
	Broadcast    bool
	BroadcastSet map[string]bool
	// broadcastAuto marks a set that was filled in by default rather than
	// chosen pane by pane, so it can be dropped when broadcast is switched
	// off instead of following the user into the next tab.
	broadcastAuto bool

	// gitMu guards what RefreshGit keeps across refreshes, which overlap now
	// that one no longer waits for another: gitBusy holds, by pathKey, the
	// checkouts git is being asked about, and gitSlots bounds how many it is
	// asked about at once. Both are made on first use, as is gitIndexAt,
	// which holds when each checkout's index was last refreshed.
	gitMu      sync.Mutex
	gitBusy    map[string]bool
	gitSlots   chan struct{}
	gitIndexAt map[string]time.Time

	mu    sync.RWMutex
	panes map[string]*Pane
	// pendingTitles holds titles a pane asked for from the hook goroutine,
	// waiting to be applied to its tab on the interface's own goroutine.
	pendingTitles map[string]string

	// openRoots are the projects currently open, in the order they were
	// opened; activeRoot is the one being shown.
	openRoots []string
	// closedRoots are the projects closed during this run. See ClosedRoots.
	closedRoots []string
	// away are the projects in the saved list whose folders were not there
	// when this run started, kept for their return. See RestoreSession.
	away []awayRoot
	// opening guards against a restore that reopens itself. A tab can show
	// panes from another project, so restoring one project can open a second,
	// whose own layout may hold a pane belonging back to the first.
	opening    map[string]bool
	activeRoot string
	activeTab  string
	// lastTab remembers which tab each project was left on, so coming back to
	// a project comes back to what you were doing in it.
	lastTab map[string]string
	// restoreErrs is what the restores since RestoreErrors was last asked
	// could not read. See RestoreErrors.
	restoreErrs []error
	// lastKeyMove is the keyboard move just made, so that the opposite arrow
	// can undo it.
	lastKeyMove keyMove

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

	// reviewer decides auto-review approvals for a pane that has them turned
	// on -- see reviewTool. It defaults to review.Decide and is only ever
	// swapped by a test, so that a policy smarter than a fixed allowlist (a
	// model asked to look at the call, say) can be dropped in later without
	// reviewTool or anything upstream of it having to change.
	reviewer func(tool, toolInputJSON string) review.Decision

	// onWake is swapped rather than assigned. The server installs itself once
	// the workspace is built, by which time the panes restored with it are
	// already running and calling it from their readers.
	onWake atomic.Pointer[func()]

	// onConversation is called with a pane's id whenever a hook event about it
	// arrives -- which is also, per the design of the phone's chat view, the
	// signal that its transcript may have grown a line worth tailing. Swapped
	// the same way onWake is, and nil until the server installs itself.
	onConversation atomic.Pointer[func(string)]
}

// Options configures a new workspace.
type Options struct {
	// Root is the project to open first.
	Root string
	// OnWake is called whenever any session changes and the interface should
	// refresh. It may be called from any goroutine.
	OnWake func()
	// HookBinary overrides the executable agent panes invoke to report their
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
		reviewer:     review.Decide,
	}
	w.SetWake(opts.OnWake)
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
	srv.SetReviewHandler(w.reviewTool)
	w.hookSrv = srv
	return w, nil
}

// HookServer is the loopback API panes call back on, so the server can install
// the handler that lets an agent spawn helpers.
func (w *Workspace) HookServer() *hooks.Server { return w.hookSrv }

// SetWake installs the refresh callback. It exists so the server, which needs
// the workspace to construct, can register itself afterwards.
func (w *Workspace) SetWake(fn func()) { w.onWake.Store(&fn) }

// SetConversationHook installs the callback told a pane's id whenever a hook
// event about it arrives, for the same reason SetWake exists: the server
// needs the workspace built before it can register itself.
func (w *Workspace) SetConversationHook(fn func(paneID string)) { w.onConversation.Store(&fn) }

func (w *Workspace) wakeConversation(paneID string) {
	if paneID == "" {
		return
	}
	if fn := w.onConversation.Load(); fn != nil && *fn != nil {
		(*fn)(paneID)
	}
}

// ActiveRoot returns the project currently being shown.
func (w *Workspace) ActiveRoot() string { return w.activeRoot }

// ClaudeAvailable reports whether the claude CLI was found.
func (w *Workspace) ClaudeAvailable() bool { return w.claudeExe != "" }

func (w *Workspace) wake() {
	if fn := w.onWake.Load(); fn != nil && *fn != nil {
		(*fn)()
	}
}

// handleHook applies a lifecycle event from a pane.
func (w *Workspace) handleHook(ev hooks.Event) {
	// The session is taken once, under the lock. This runs on the hook
	// server's goroutine while the interface may be restarting the very pane
	// the event is about, and a pane mid-restart has no session at all: read
	// the field twice and the second read can be the nil.
	w.mu.Lock()
	p := w.panes[ev.SessionID]
	// A restarted pane keeps its id, so an event from the process before it --
	// a hook that ran late, which on Windows can outlive the process that ran
	// it -- names the pane like any other. Which start it names is what tells
	// the two apart. A hook that names none predates the distinction.
	if p != nil && ev.Launch != "" && ev.Launch != p.launch {
		p = nil
	}
	var sess *session.Session
	var paneID string
	if p != nil {
		sess = p.Sess
		paneID = p.ID
		// Every event says which conversation the agent is in, and after
		// /clear that is a new one. Following it is what lets a restart, a
		// restore and a fan-out go on finding the conversation on screen
		// rather than the one before it.
		if ev.Conversation != "" {
			p.Conversation = ev.Conversation
			if ev.Conversation == p.ID {
				p.Conversation = ""
			}
		}
	}
	w.mu.Unlock()
	// Every hook event is also, for the phone's chat view, "this pane's
	// transcript may have grown a line" -- SessionStart included, which is
	// how a /clear or a resume is noticed as soon as it happens rather than
	// on the next fallback poll.
	w.wakeConversation(paneID)
	if sess == nil {
		return
	}
	// A tab named after its directory is not much help once several are open;
	// the first thing the agent was asked is far more recognisable.
	if ev.Prompt != "" {
		w.nameTabAfterPrompt(p.ID, ev.Prompt)
	}

	st, detail, ok := session.StatusForEvent(ev.Event, ev.Tool, ev.NotificationType)
	if !ok {
		return
	}
	// SessionEnd means the conversation ended, but the process may still be
	// drawing its exit screen; let the PTY reader observe the real exit.
	if st == session.StatusExited {
		return
	}
	sess.SetStatusFull(st, detail, ev.ToolInput)
}

// reviewTool answers a PreToolUse hook's request for a permission decision --
// see hooks.SetReviewHandler, which this is installed as. Only a pane with
// AutoReview on is put to the reviewer at all, and answering false here is
// answered to Claude Code exactly as no handler being installed at all would
// be: the request goes on to its own permission prompt, unchanged.
func (w *Workspace) reviewTool(sessionID string, ev hooks.Event) (allow bool, reason string) {
	w.mu.Lock()
	p := w.panes[sessionID]
	// A hook from the process before a restart, on the pane's own id: the
	// launch mismatch handleHook guards against applies here for the same
	// reason -- this must never decide for the wrong incarnation of a pane's
	// own AutoReview choice.
	if p != nil && ev.Launch != "" && ev.Launch != p.launch {
		p = nil
	}
	on := p != nil && p.AutoReview
	w.mu.Unlock()
	if !on {
		return false, ""
	}

	d := w.reviewer(ev.Tool, ev.ToolInput)
	if !d.Allow {
		return false, ""
	}

	w.mu.Lock()
	if p := w.panes[sessionID]; p != nil {
		p.AutoApproved++
	}
	w.mu.Unlock()
	w.wake()
	return true, d.Reason
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
			if pane == nil {
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
			out[i].Panes++
			if pane.Sess == nil {
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
			// Copies on two drives agree on every element above the drive, so
			// once those have run out the drive is what is left to tell them
			// apart. A path with no volume has nothing more to give.
			if out != "" && filepath.VolumeName(rest) != "" {
				out = filepath.Join(rest, out)
			}
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
		w.NewTab(w.firstPaneKind(), root, "")
	} else {
		w.focusFirstTabOf(root)
	}
	w.wake()
	return nil
}

// firstPaneKind is what a project with no saved tabs is opened on: the agent
// it would run if that can be started here, and a shell otherwise, so the tab
// comes up on something that works. It must be asked with the project active,
// since the default agent is the project's own.
//
// Asking whether Claude is installed answered a different question once the
// default could be another agent: somebody running only Codex got a shell in
// every project they opened, and somebody with Claude whose default is an
// agent they had not installed got a pane that could only report it missing.
func (w *Workspace) firstPaneKind() session.Kind {
	if _, err := w.AgentSpec(""); err != nil {
		return session.KindShell
	}
	return session.KindClaude
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
//
// A layout that cannot be saved does not keep the project open: closing it is
// what was asked for, and its agents are stopped either way. It is reported
// instead. The save was the one record of what the project had open, and
// dropped without a word the project simply came back as it was the time
// before, with nothing to say why.
func (w *Workspace) CloseProject(root string) error {
	root, ok := w.openRootFor(root)
	if !ok || len(w.openRoots) <= 1 {
		return nil
	}
	var saveErr error
	if err := w.SaveProject(root); err != nil {
		saveErr = fmt.Errorf("%s was closed, but its layout could not be saved, so it will not open as it was left: %w", filepath.Base(root), err)
	}

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
	w.closedRoots = append(w.closedRoots, root)
	// A project whose folder was away at start, and came back and was opened,
	// is closed for good: it is not kept for its return any more.
	w.away = slices.DeleteFunc(w.away, func(a awayRoot) bool { return sameDir(a.root, root) })

	if w.activeRoot == root {
		w.activeRoot = w.openRoots[0]
	}
	w.focusFirstTabOf(w.activeRoot)
	w.wake()
	return saveErr
}

// ClosedRoots are the projects closed during this run, in the order they were
// closed, under the spelling each was open with. A -new run puts back the
// projects that were open before it on its way out, and without these it put
// back the very ones the user had closed. One closed and opened again is still
// among them; it is in Session's list as well.
func (w *Workspace) ClosedRoots() []string {
	return append([]string(nil), w.closedRoots...)
}

func (w *Workspace) isOpen(root string) bool {
	_, ok := w.openRootFor(root)
	return ok
}

// OpenRootFor is openRootFor for callers outside the package: it resolves a
// path to the open project it names, spelled as that project was opened.
// StartAgent uses it to refuse a root that is not an already open project,
// rather than open one itself -- a phone is never let create a project the
// person at the desk never asked for.
func (w *Workspace) OpenRootFor(root string) (string, bool) { return w.openRootFor(root) }

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
	return w.openRootOnDisk(root)
}

// openRootOnDisk finds the open project that is the directory root names,
// asking the file system rather than comparing strings.
//
// Some spellings of one directory no comparison of strings can see through:
// an 8.3 short name on Windows (PROGRA~1), a junction or a symlink, macOS's
// /tmp for /private/tmp. Each opened a second project onto a folder that was
// already open, with its own panes resuming the very conversations the first
// set was in. It is asked only once the strings have failed to match, which
// for a folder that has gone costs one failed Stat; the project keeps the
// spelling it was opened with, and with it the name of its layout file.
func (w *Workspace) openRootOnDisk(root string) (string, bool) {
	fi, err := os.Stat(root)
	if err != nil || !fi.IsDir() {
		return "", false
	}
	for _, r := range w.openRoots {
		if open, err := os.Stat(r); err == nil && os.SameFile(fi, open) {
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
			if t.Tree.Find(paneID) == nil {
				continue
			}
			switch {
			case t.AutoTitle:
				t.Title = title
				t.AutoTitle = false
			case t.Named && t.autoOpen:
				// A tab named by hand before its first prompt keeps its name,
				// but the title it would have had follows the prompt behind
				// it, for as long as the name is kept.
				t.auto = title
				t.autoOpen = false
			}
			break
		}
	}
}

// NameTab gives a tab a name chosen by hand, which no prompt replaces. The
// title the tab had been giving itself is kept, for UseAutoTitle to put back.
func (w *Workspace) NameTab(id, name string) {
	t := w.Tab(id)
	if t == nil {
		return
	}
	if !t.Named {
		t.Named = true
		t.auto, t.autoOpen = t.Title, t.AutoTitle
	}
	t.Title = name
	t.AutoTitle = false
}

// UseAutoTitle gives up a name chosen by hand, and gives the tab the title it
// would have now had it never been renamed: the directory name, still waiting
// for the first prompt to replace it, or what the first prompt made of it. It
// reports whether anything changed, which it does not for a tab that already
// has its automatic title.
func (w *Workspace) UseAutoTitle(id string) bool {
	t := w.Tab(id)
	if t == nil || !t.Named {
		return false
	}
	if t.auto != "" {
		t.Title, t.AutoTitle = t.auto, t.autoOpen
	} else {
		t.Title, t.AutoTitle = w.freshTitle(t)
	}
	t.Named = false
	t.auto, t.autoOpen = "", false
	return true
}

// freshTitle is the title a tab holding t's panes would be given if it were
// opened now, for a tab whose own automatic title is not known. A tab is named
// after the pane it was opened with, and that is not recorded either, so the
// first agent stands in for it: an agent is what a tab is named after.
func (w *Workspace) freshTitle(t *Tab) (title string, auto bool) {
	var first *Pane
	for _, id := range t.Tree.Panes() {
		p := w.Pane(id)
		if p == nil {
			continue
		}
		if p.IsAgent() {
			return paneTabTitle(p)
		}
		if first == nil {
			first = p
		}
	}
	if first != nil {
		return paneTabTitle(first)
	}
	return filepath.Base(t.Root), false
}

// summarisePrompt reduces a prompt to something that fits in a tab.
func summarisePrompt(prompt string) string {
	raw := strings.TrimSpace(prompt)
	// Slash commands and the synthetic messages Claude records are not what
	// the tab should be called.
	if raw == "" || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "<") {
		return ""
	}
	s := strings.Join(strings.Fields(plainPrompt(raw)), " ")
	if s == "" {
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

// plainPrompt drops the markdown a prompt may be written in — heading and
// quote marks, code fences, a bullet and its checkbox, bold — which only meant
// anything rendered. A tab has room for a few words, and "``` panic: runtime"
// or "## Task Refactor" spends them on punctuation.
func plainPrompt(prompt string) string {
	var words []string
	for _, line := range strings.Split(prompt, "\n") {
		line = strings.TrimSpace(line)
		if isFence(line) {
			continue
		}
		line = unheaded(line)
		for strings.HasPrefix(line, ">") {
			line = strings.TrimSpace(line[1:])
		}
		for _, bullet := range []string{"- ", "* ", "• "} {
			if strings.HasPrefix(line, bullet) {
				line = trimCheckbox(strings.TrimPrefix(line, bullet))
				break
			}
		}
		words = append(words, line)
	}
	return strings.NewReplacer("**", "", "__", "").Replace(strings.Join(words, " "))
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
		cols, rows = defaultCols, defaultRows
	}
	// Each start is named afresh, before anything of this one can report, so
	// that what the last one still has to say is told from it. See handleHook.
	w.mu.Lock()
	p.launch = rand.Text()
	w.mu.Unlock()

	var argv, env []string
	// spec is the agent the pane runs, and stays empty for a shell. The session
	// is handed it as well as the command line: an agent that reports nothing
	// of its own lifecycle is watched through the lines its Spec says mean
	// waiting and idle, and a session never given it watches for none.
	var spec agent.Spec
	if !p.IsAgent() {
		argv = session.ShellArgs()
		env = w.environ(w.paneEnv(p, "", "")...)
	} else {
		// An empty agent is not a missing one: it means whichever agent this
		// pane would be opened with today -- its project's default, or the
		// installation's -- which is how a layout written before panes could
		// choose still restores, and how a project that has settled on another
		// agent gets it without every pane having to record the name.
		agentID := p.Agent
		if agentID == "" {
			agentID = w.defaultAgentFor(p.Root)
		}
		var ok bool
		spec, ok = w.agents().Find(agentID)
		if !ok {
			// A layout can name an agent this machine has no entry for — one
			// removed from the user's agents.json, or a layout carried over
			// from a machine that had it. That is this pane's problem and no
			// other's, so it is reported in place, with the way out of it:
			// told only that the agent was not configured, the user was left
			// to work out where agents are configured, and that the pane can
			// be restarted once it is.
			p.Err = fmt.Errorf("no agent named %q is configured on this machine, so this pane cannot start. Add it to agents.json and restart the pane, or close it", agentID)
			return
		}
		// A CLI runner is somebody else's program and may simply not be on the
		// machine; an API runner is this binary, which is by definition here.
		if spec.Runner != agent.RunnerAPI && spec.Exe != "" {
			if _, err := exec.LookPath(spec.Exe); err != nil {
				p.Err = missingCLI(spec)
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
			// The conversation to start or reattach is the one the agent was
			// last in, which is the pane's id until the agent moves to a new
			// one — Claude Code does on /clear. The pane token stays the
			// pane's id whatever happens, since that is what a lifecycle event
			// is reported against.
			Session: w.conversationOf(p),
			Model:   model,
			Cwd:     p.Cwd,
			Pane:    p.ID,
		}
		// Only an agent that reports its own lifecycle has anything to do with
		// a settings file; for the rest the pane's status comes from watching
		// what it prints, and writing one would leave a file behind per pane
		// that nothing ever reads. Nor does one whose arguments never name the
		// file: `flockdeck chat` reports its lifecycle as well, but is told
		// where in its environment, and every API pane wrote Claude Code's
		// hook settings -- and could run `claude --version` to choose them.
		if spec.Caps.Hooks && session.WantsSettings(spec) {
			// Which events and which form of hook to write depend on the Claude
			// Code that will read them, so the one asked is the program this
			// pane runs, not whichever claude happens to be first on PATH.
			//
			// The status line goes through Flockdeck as the preference says,
			// read afresh for each pane so that a change in Settings reaches
			// the next pane started, and a restart of this one.
			statusLine := session.StatusLine{
				Mode:     store.LoadPrefs().Spend.StatusLine,
				Endpoint: w.hookSrv.UsageEndpoint(),
				Cwd:      p.Cwd,
				Home:     transcript.ClaudeHomeFor(spec),
			}
			settings, err := session.WriteHookSettingsWith(spec.Exe,
				w.settingsDir, p.ID, w.selfExe, w.hookSrv.Endpoint(), statusLine)
			if err != nil {
				p.Err = err
				return
			}
			tokens.Settings = settings
		}
		// Resuming an agent that has no transcript for this session fails
		// immediately — `claude --resume` prints "No conversation found" and
		// exits — so a pane that was never prompted must start fresh instead.
		resuming := resume && spec.Caps.Resume && w.transcriptExists(spec, tokens.Session)
		if !resuming {
			// Handing the task over as an opening argument is far more
			// reliable than typing into the terminal, which would mean
			// guessing when the interface is ready to accept it.
			//
			// An agent with no hooks to answer is briefed here or nowhere, so
			// the briefing goes in front of the task. One with hooks is briefed
			// when it fires its first, and gets the task untouched.
			task := p.initial
			if task == "" && resume && spec.Caps.Resume {
				// A pane that would have resumed but has no conversation to
				// resume never got as far as its first turn — stopped at a
				// question, or before it began — so the task it was spawned
				// with has not been done. It is asked again rather than
				// brought back idle with its task gone.
				task = p.Task
			}
			tokens.Prompt = w.OpeningPrompt(p.ID, task, spec.Caps.Context)
		}
		argv = agent.BuildArgv(spec, resuming, tokens)
		extra := append([]string{}, spec.Env...)
		if spec.Runner == agent.RunnerAPI {
			// An API agent is Flockdeck's own chat client. BuildArgv leaves the
			// program off because only this side knows where the running
			// binary is — and on Windows the program is the console build
			// beside it, since the release itself gets no console to print to.
			argv = append([]string{session.ChatExe(w.selfExe), "chat"}, argv...)
			// A key Flockdeck is keeping for this agent goes into the environment
			// of this pane and no other. One the user has already exported is
			// inherited and needs nothing added, so nothing is: a second copy
			// of a secret is a second place it can be read from.
			extra = append(extra, creds.Env(spec)...)
		}
		env = w.environ(append(extra, w.paneEnv(p, spec.ID, model)...)...)
	}

	s, err := session.Start(session.Config{
		ID:   p.ID,
		Kind: p.Kind,
		Spec: spec,
		Name: p.Name,
		Cwd:  p.Cwd,
		Argv: argv,
		Env:  env,
		Cols: cols,
		Rows: rows,
		// Installed by Start, before the reader that calls it is running.
		OnChange: w.wake,
	})
	if err != nil {
		// This is shown in the pane in place of a terminal, so it has to name
		// the directory: a worktree removed under a pane is the usual reason a
		// session will not start, and the error itself never says which one.
		//
		// Where that is the reason it is said outright. The operating system's
		// own account of it — "The directory name is invalid", on Windows —
		// reads as a path mistyped somewhere, not as a directory that has gone,
		// and says nothing about what to do next.
		if _, statErr := os.Stat(p.Cwd); errors.Is(statErr, os.ErrNotExist) {
			p.Err = fmt.Errorf("the directory this pane works in, %s, no longer exists; it is most often a worktree removed while the pane was closed. Put the directory back and restart the pane, or close it", p.Cwd)
			return
		}
		p.Err = fmt.Errorf("%w (working directory %s)", err, p.Cwd)
		return
	}
	// Lifecycle hooks read this field from the hook server's goroutine, so it
	// is only ever swapped under the lock.
	w.mu.Lock()
	p.Sess = s
	p.Err = nil
	w.mu.Unlock()
	// The opening prompt is spent; a later restart resumes instead.
	p.initial = ""
}

// environ builds a pane's environment: Flockdeck's own, less the markers every
// agent in the catalog asks to have taken out, plus extra.
//
// The whole catalog's list rather than the built-in one, because the markers
// are those of whatever session Flockdeck was launched from, which has nothing
// to do with the agent in this pane — and an agent the user added with a
// "stripEnv" of its own would otherwise have it ignored in every pane.
func (w *Workspace) environ(extra ...string) []string {
	return session.EnvStripping(w.agents().StripEnv(), extra...)
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
	// Which start of the pane this is, for its hooks to send back: see
	// handleHook. It is Flockdeck's own business, so it has no older spelling.
	if p.launch != "" {
		env = append(env, hooks.LaunchEnv+"="+p.launch)
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
func (w *Workspace) defaultAgent() string { return w.defaultAgentFor(w.activeRoot) }

// defaultAgentFor is the agent a new pane in project root runs when it asks
// for none.
func (w *Workspace) defaultAgentFor(root string) string {
	w.catalogMu.RLock()
	run := w.runAgent
	w.catalogMu.RUnlock()
	if run != "" {
		return run
	}
	return w.agents().DefaultsFor(root).Agent
}

// resolveChoice settles the agent and model a new pane in project root runs,
// from what it asked for and that project's defaults, so that what is recorded
// on the pane is what it runs.
//
// Leaving both empty to mean "the default" cost three things. Only the agent
// was ever looked up, so the model a project was set to was never applied; it
// was looked up for the project on screen, which is not the pane's when it is
// split into another project; and a pane restored after the default had
// changed came back as a different agent on top of the old conversation.
//
// Only a pane that asked for no agent is given the defaults. One chosen in the
// picker is recorded as it was chosen: an empty model there is the agent's
// "Default" row, whatever the CLI is set to, and filling it from the project
// would run a model nobody picked. An agent the catalog has no entry for is
// left as asked too, so that starting it reports the problem in the pane
// rather than quietly running something else.
func (w *Workspace) resolveChoice(root, agentID, model string) (string, string) {
	if agentID != "" {
		return agentID, model
	}
	agentID = w.defaultAgentFor(root)
	c := w.agents()
	if _, ok := c.Find(agentID); !ok {
		return agentID, model
	}
	spec, model, _ := c.Resolve(root, agentID, model)
	return spec.ID, model
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
// default of project root rather than a missing answer, which is how a layout
// written before panes could choose still restores.
func (w *Workspace) specFor(root, id string) (agent.Spec, bool) {
	c := w.agents()
	if id == "" {
		id = w.defaultAgentFor(root)
	}
	return c.Find(id)
}

// transcriptExists reports whether the agent has a stored conversation for a
// session id, which is what decides whether resuming one is worth trying.
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
func (w *Workspace) projectFor(cwd string) string { return w.projectForOr(cwd, w.activeRoot) }

// projectForOr is projectFor with the project a directory under none of them
// belongs to named by the caller, for one that knows better than the screen
// does: a helper spawned into a worktree beside its repository belongs to the
// agent that spawned it, whichever project the user is looking at.
func (w *Workspace) projectForOr(cwd, fallback string) string {
	best := ""
	for _, r := range w.openRoots {
		if underDir(cwd, r) && len(r) > len(best) {
			best = r
		}
	}
	if best == "" {
		return fallback
	}
	return best
}

// helperProject is the project a helper an agent spawned belongs to. Its
// directory always comes from the agent that asked for it — that agent's own,
// or a worktree cut from its checkout — so the agent's project is the answer
// unless the directory is inside a project more particular than that one.
//
// The innermost open project containing the directory is not enough on its
// own. A project open on a directory above the others — the home folder, which
// is where a launch from the Start menu opens — contains every worktree beside
// its repository, and claimed each helper put in one: counted in its badge,
// stopped when it closed, briefed as its agent.
func (w *Workspace) helperProject(cwd, parent string) string {
	project := w.projectForOr(cwd, parent)
	if parent != "" && underDir(parent, project) {
		return parent
	}
	return project
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
//
// The directories that arrive without a project are the project on screen's
// own and, from the worktree panel, its worktrees — which sit beside the
// repository, not in it. So the project on screen is the answer unless the
// directory is inside a project more particular than that one, as for a
// helper an agent spawns: the innermost project containing a worktree is,
// with the home folder open, the home folder.
func (w *Workspace) newPane(c Choice, cwd, name, root string) *Pane {
	if cwd == "" {
		cwd = w.activeRoot
	}
	if name == "" {
		name = filepath.Base(cwd)
	}
	if root == "" {
		root = w.helperProject(cwd, w.activeRoot)
	}
	p := &Pane{
		ID: uuid.NewString(), Kind: c.Kind, Cwd: cwd, Name: name, Root: root,
		Branch: branchOf(cwd),
	}
	// A shell runs no agent, so it is never given one to remember: a kind and
	// an agent that disagreed would be written to the layout and read back as
	// a pane that is somehow both.
	if p.IsAgent() {
		p.Agent, p.Model = w.resolveChoice(root, c.Agent, c.Model)
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
	if selfExe == "" {
		return "flockdeck"
	}
	// `flockdeck` on PATH is this binary only if it resolves to it. A build
	// run from its own folder while an older copy is installed would otherwise
	// hand every agent the older one, whose spawn need not know the flags this
	// one's briefing describes.
	if onPath, err := exec.LookPath("flockdeck"); err == nil && sameFile(onPath, selfExe) {
		return "flockdeck"
	}
	return selfExe
}

// sameFile reports whether two paths name one file.
func sameFile(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	return err == nil && os.SameFile(fa, fb)
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
	for _, pid := range closing.Tree.Panes() {
		w.destroyPane(pid)
	}
	// What is left to do is what a tab emptied by a move needs, focus landing
	// on a neighbour in the same project included.
	w.unlinkTab(closing)
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
	w.ClosePaneByID(t.Focus)
}

// ClosePaneByID closes a pane wherever its tab is, without moving the window
// off whatever tab or project is on screen, and reports whether the pane was
// there to close. Closing the last pane in its tab closes the tab.
//
// It is what lets the control socket close a helper sitting in a tab nobody
// is looking at: FocusPane, which every command used to go through, only
// finds a pane in the tab already on screen, and refused one anywhere else as
// though it had already gone.
func (w *Workspace) ClosePaneByID(id string) bool {
	t := w.tabOf(id)
	if t == nil {
		return false
	}
	if t.Tree.Count() <= 1 {
		w.destroyPane(id)
		w.unlinkTab(t)
		return true
	}
	// Closing a pane is taking it out of its tab, which a move does too, focus
	// on the pane beside the gap and all; only ending its process is extra.
	w.detachPane(id)
	w.destroyPane(id)
	return true
}

// RestartPane relaunches the focused pane's process. An agent pane resumes the
// same conversation wherever its agent can.
func (w *Workspace) RestartPane() {
	p := w.FocusedPane()
	if p == nil {
		return
	}
	w.RestartPaneByID(p.ID)
}

// RestartPaneByID is RestartPane for a pane named by id, wherever its tab is,
// and reports whether the pane was there to restart.
func (w *Workspace) RestartPaneByID(id string) bool {
	p := w.Pane(id)
	if p == nil {
		return false
	}
	if p.Sess != nil {
		_ = p.Sess.Close()
		w.mu.Lock()
		p.Sess = nil
		w.mu.Unlock()
	}
	w.startPane(p, p.IsAgent())
	w.wake()
	return true
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

// RevealPane selects the tab a pane is in, switching project if that tab
// belongs to another, and focuses the pane there. FocusPane alone only works
// in the tab on screen, which is no help for a pane that has just been put in
// a tab of its own. A pane in no tab at all is left alone, and so is the
// window.
func (w *Workspace) RevealPane(id string) {
	tab := w.TabIDOf(id)
	if tab == "" {
		return
	}
	w.SelectTab(tab)
	w.FocusPane(id)
}

// ToggleZoom expands or restores the focused pane.
func (w *Workspace) ToggleZoom() {
	if t := w.CurrentTab(); t != nil {
		w.ToggleZoomByID(t.Focus)
	}
}

// ToggleZoomByID is ToggleZoom for a pane named by id, wherever its tab is: it
// gives the pane the focus of its own tab, expands or restores that tab, and
// reports whether the pane was there at all. Called on the pane already
// focused in the tab on screen, which is every use but the control socket's,
// it changes nothing beyond that tab's own Zoom.
func (w *Workspace) ToggleZoomByID(id string) bool {
	t := w.tabOf(id)
	if t == nil {
		return false
	}
	t.Focus = id
	t.Zoom = !t.Zoom
	return true
}

// SetPaneMuted mutes or unmutes a pane's phone push notifications -- see
// push.go's pushDue, which leaves a muted pane's wait out of the ones a push
// is built from. It reports whether the pane was found, which is false for
// one already closed, and is what a mutePane command for a gone id is
// refused by.
func (w *Workspace) SetPaneMuted(id string, muted bool) bool {
	p := w.Pane(id)
	if p == nil {
		return false
	}
	if p.Muted != muted {
		p.Muted = muted
		w.wake()
	}
	return true
}

// SetPaneAutoReview turns auto-review approvals on or off for one pane -- see
// Pane.AutoReview and reviewTool, which is where it is read. It reports
// whether the pane was found, which is false for one already closed, and is
// what an autoReview command for a gone id is refused by.
//
// It writes under w.mu, unlike SetPaneMuted: reviewTool reads AutoReview from
// the hook server's own goroutines, not the one that owns the workspace.
func (w *Workspace) SetPaneAutoReview(id string, on bool) bool {
	w.mu.Lock()
	p := w.panes[id]
	changed := p != nil && p.AutoReview != on
	if changed {
		p.AutoReview = on
	}
	w.mu.Unlock()
	if p == nil {
		return false
	}
	if changed {
		w.wake()
	}
	return true
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
// With no selection of the user's own, broadcast means every agent pane in
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
	return w.BroadcastTargetsFor(t.Focus)
}

// BroadcastTargetsFor is BroadcastTargets with the pane named standing where
// the focused one does. The prompt bar sends to the pane it was opened on,
// which focus may have left while it was being written in.
func (w *Workspace) BroadcastTargetsFor(focus string) []*Pane {
	t := w.CurrentTab()
	if t == nil {
		return nil
	}
	var out []*Pane
	seen := map[string]bool{}
	for _, id := range t.Tree.Panes() {
		if !w.InBroadcast(id) && id != focus {
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
//
// Each pane is written to on a goroutine of its own. This runs on the
// goroutine that owns the workspace, and a terminal whose program has stopped
// reading its input fills after a few kilobytes on Linux and macOS, after
// which a write into it blocks until the program reads again — which, done
// here, held up the whole window for as long as one pane went on not reading.
//
// A prompt of several lines is pasted rather than typed into a pane whose
// program takes bracketed paste: typed, every line break in it is an Enter,
// and the first line was submitted on its own with the rest arriving as
// further messages. Where the program has not asked for bracketed paste the
// markers would reach it as text, so it is typed as before.
func (w *Workspace) SendPrompt(text string, submit bool) {
	w.sendPrompt(w.BroadcastTargets(), text, submit)
}

// SendPromptTo is SendPrompt with the pane named standing where the focused
// one does; see BroadcastTargetsFor.
func (w *Workspace) SendPromptTo(focus, text string, submit bool) {
	w.sendPrompt(w.BroadcastTargetsFor(focus), text, submit)
}

func (w *Workspace) sendPrompt(targets []*Pane, text string, submit bool) {
	for _, p := range targets {
		go func(s *session.Session) {
			typed, pasted := promptInput(text, s.BracketedPaste())
			if err := s.WriteString(typed); err == nil && submit {
				// The Enter waits a moment after a paste. A program can take
				// a paste in on its own time -- Claude Code folds a long one
				// into a placeholder first -- and an Enter arriving on the
				// heels of the end marker could be read before the paste had
				// reached the input, submitting what was there without it.
				if pasted {
					time.Sleep(pasteSettle)
				}
				_ = s.WriteString("\r")
			}
		}(p.Sess)
	}
}

// pasteSettle is how long SendPrompt leaves between a pasted prompt and the
// Enter that submits it.
var pasteSettle = 100 * time.Millisecond

// promptInput returns what the prompt bar writes to a pane for text, and
// whether it is a paste. Only text with a line break in it is pasted: a line
// on its own is typed exactly as it always was, so a slash command still
// reaches an agent the way it would from the keyboard.
//
// The paste is what a terminal sends for one. Its line breaks are carriage
// returns, as xterm makes them, and any paste markers inside the text are
// removed, as terminals do: an end marker in it would close the paste early
// and leave the rest to be typed, line breaks and all.
func promptInput(text string, bracketed bool) (string, bool) {
	if !bracketed || !strings.ContainsAny(text, "\r\n") {
		return text, false
	}
	body := strings.NewReplacer(
		"\x1b[200~", "", "\x1b[201~", "",
		"\r\n", "\r", "\n", "\r",
	).Replace(text)
	return "\x1b[200~" + body + "\x1b[201~", true
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

// gitStatus reads one checkout's summary within a deadline. It is a variable
// for the same reason branchLookup is: how many are asked at once, and what
// happens to the rest while one of them hangs, are the properties worth
// checking.
var gitStatus = gitx.StatusWithin

// gitExited says when a git given up on has really gone; see gitx.Exited.
var gitExited = gitx.Exited

// gitDeadline is how long one checkout's refresh has before it is given up on
// and its panes are marked as not having answered. It is shorter than the
// fifteen seconds between refreshes, so a checkout that hangs has been given
// up on by the time the next refresh comes round to it rather than being
// passed over as still busy, and far beyond the fraction of a second git
// status takes anywhere it is not stuck. A variable so a test need not wait it
// out.
var gitDeadline = 10 * time.Second

// slowStatus is how long a checkout's status has to take before its index is
// refreshed, and indexRefreshEvery how long after that the same checkout's
// index may be refreshed again; indexRefresh does it (see RefreshGit). They
// are variables so a test need not make a checkout slow.
var (
	slowStatus        = 2 * time.Second
	indexRefreshEvery = 10 * time.Minute
	indexRefresh      = gitx.RefreshIndex
)

// RefreshGit updates every pane's git summary.
//
// The git calls are made off the caller's goroutine and only the results are
// applied, so a slow repository cannot stall the interface. Each checkout's
// result is handed to apply as soon as it is read, and apply must run it where
// mutating panes is safe. RefreshGit returns once every checkout it asked
// about has answered or been given up on.
//
// It applied nothing until every checkout had answered, so one that hung --
// on a network drive gone away, behind a hook that never returned -- held
// every pane's header at its old numbers for the twenty seconds git was
// given, and then its own panes went on showing numbers from before it hung
// as though nothing were wrong. Now a checkout holds up only its own panes,
// for gitDeadline at most, and they are marked when it runs out.
func (w *Workspace) RefreshGit(apply func(func())) {
	w.refreshGit(apply, nil)
}

// RefreshGitOf is RefreshGit for the checkouts of the panes named only.
//
// Every checkout of every open project was read every refresh, a whole-tree
// git status each, while only the panes on screen show what was read. With a
// few projects open that was dozens of processes every fifteen seconds for
// headers nobody could see.
func (w *Workspace) RefreshGitOf(apply func(func()), ids []string) {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	w.refreshGit(apply, func(p *Pane) bool { return want[p.ID] })
}

// refreshGit is RefreshGit for the panes keep accepts, or for every pane when
// keep is nil.
func (w *Workspace) refreshGit(apply func(func()), keep func(*Pane) bool) {
	if !gitx.Available() {
		return
	}
	// One status call per distinct directory, not per pane: several panes
	// commonly share a checkout. Distinct by pathKey rather than by string,
	// since one checkout reaches panes spelt more than one way — a trailing
	// separator, a drive letter in another case — and each spelling was a
	// whole-tree status of its own. rep is the spelling git is asked with.
	var cwds []string
	rep := map[string]string{}
	w.mu.RLock()
	for _, p := range w.panes {
		if p.Cwd == "" || (keep != nil && !keep(p)) {
			continue
		}
		if _, ok := rep[pathKey(p.Cwd)]; ok {
			continue
		}
		rep[pathKey(p.Cwd)] = p.Cwd
		cwds = append(cwds, p.Cwd)
	}
	w.mu.RUnlock()
	if len(cwds) == 0 {
		return
	}

	// A checkout still being read from an earlier refresh is left to that
	// one. Asking again would only queue a second git process behind the
	// first in the one checkout that is already slow.
	w.gitMu.Lock()
	if w.gitBusy == nil {
		w.gitBusy = map[string]bool{}
		// No more git processes at once than a restore asks for branches
		// with. A fan-out leaves a dozen worktrees open, and starting a git
		// process for every one of them in the same instant, every refresh,
		// contends with the agents working in them for no answer that arrives
		// any sooner. The bound is the workspace's rather than the refresh's
		// because refreshes overlap.
		w.gitSlots = make(chan struct{}, branchLookups)
	}
	slots := w.gitSlots
	var asking []string
	for _, cwd := range cwds {
		if key := pathKey(cwd); !w.gitBusy[key] {
			w.gitBusy[key] = true
			asking = append(asking, cwd)
		}
	}
	w.gitMu.Unlock()

	var wg sync.WaitGroup
	for _, cwd := range asking {
		wg.Add(1)
		go func(cwd string) {
			defer wg.Done()
			// The deadline starts once git does, not while waiting for a
			// slot: a checkout queued behind slow ones has not been slow.
			slots <- struct{}{}
			began := time.Now()
			st, err := gitStatus(cwd, gitDeadline)
			slow := err == nil && time.Since(began) >= slowStatus
			<-slots
			key := pathKey(cwd)
			late := errors.Is(err, context.DeadlineExceeded)
			apply(func() { w.applyGit(key, st, late) })
			// A status that answered, but slowly, is most often one looking
			// again at files touched since the index last recorded them,
			// which it cannot record itself (see gitx.RefreshIndex). Its
			// answer is shown first. The index is refreshed while the
			// checkout is still marked busy, so no status runs beside it, and
			// not again for a while: a checkout slow for some other reason
			// gains nothing, and the refresh holds the lock an agent's commit
			// needs for as long as it runs.
			if slow && w.indexDue(key) {
				_ = indexRefresh(cwd)
			}
			// A git given up on is answered for at once, but the checkout
			// stays busy until it has really gone. Ending one stuck on a
			// drive that has gone away takes as long as the drive does, and
			// asking again sooner started another git beside it every
			// refresh. That wait is not this refresh's: its slot is free.
			done := func() {
				w.gitMu.Lock()
				delete(w.gitBusy, key)
				w.gitMu.Unlock()
			}
			select {
			case <-gitExited(err):
				done()
			default:
				go func() {
					<-gitExited(err)
					done()
				}()
			}
		}(cwd)
	}
	wg.Wait()
}

// indexDue reports whether the index of the checkout key names may be
// refreshed now, and notes that it is being.
func (w *Workspace) indexDue(key string) bool {
	w.gitMu.Lock()
	defer w.gitMu.Unlock()
	if at, ok := w.gitIndexAt[key]; ok && time.Since(at) < indexRefreshEvery {
		return false
	}
	if w.gitIndexAt == nil {
		w.gitIndexAt = map[string]time.Time{}
	}
	w.gitIndexAt[key] = time.Now()
	return true
}

// applyGit gives every pane in the checkout key names what git said about it,
// or, when late, marks them as not having heard.
func (w *Workspace) applyGit(key string, st gitx.Status, late bool) {
	changed := false
	w.mu.Lock()
	for _, p := range w.panes {
		if p.Cwd == "" || pathKey(p.Cwd) != key {
			continue
		}
		if late {
			// What was read before is kept, since the checkout has not been
			// seen to change, but it is no longer passed off as current.
			if !p.GitTimedOut {
				p.GitTimedOut = true
				changed = true
			}
			continue
		}
		if p.Git == st && !p.GitTimedOut {
			continue
		}
		p.Git = st
		p.GitTimedOut = false
		// A checkout that has left its branch for a bare commit reports no
		// branch at all, and keeping the old name would go on telling the
		// user it is still on it. It is named the way the worktree panel
		// names one. Nothing reported at all -- git failing, or no longer
		// a repository -- is no evidence the branch moved, so it is kept.
		branch := st.Branch
		if branch == "" && st.Detached && st.Head != "" {
			branch = "detached@" + st.Head
		}
		if branch != "" && branch != p.Branch {
			p.Branch = branch
		}
		changed = true
	}
	w.mu.Unlock()
	if changed {
		w.wake()
	}
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

// OpenConversation opens a stored conversation in a new tab.
//
// The pane takes the conversation's own id, which is what makes `--resume`
// reattach to it and what lets the layout remember it afterwards. A
// conversation that is already open is focused instead of being started twice:
// two panes on one conversation would fight over the same transcript.
func (w *Workspace) OpenConversation(id, cwd, title string) error {
	return w.OpenConversationAs(id, cwd, title, "")
}

// OpenConversationAs is OpenConversation for a conversation the history panel
// knows the agent of. That agent is tried ahead of the rest: every API agent
// keeps its transcripts in the chat client's one folder, so without it an
// OpenAI conversation was reopened as whichever API agent came first.
func (w *Workspace) OpenConversationAs(id, cwd, title, agentID string) error {
	if id == "" {
		return fmt.Errorf("no conversation given")
	}
	// The id arrives from the window and becomes the pane's id, which is a
	// file name in more than one place: the chat client's transcript, the
	// pane's settings file, removed again when the pane goes. One naming a
	// directory, or the way out of one, would have those read, written and
	// removed somewhere else. No conversation an agent records is named so.
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return fmt.Errorf("%q is not a conversation", id)
	}
	// The pane in the conversation need not be the one whose id it is: that
	// one may have moved on, on /clear, to a conversation of its own.
	for _, t := range w.Tabs {
		for _, pid := range t.Tree.Panes() {
			if p := w.Pane(pid); p != nil && w.conversationOf(p) == id {
				w.SelectTab(t.ID)
				t.Focus = pid
				w.wake()
				return nil
			}
		}
	}
	paneID := id
	if w.Pane(id) != nil {
		if w.tabOf(id) != nil {
			// On screen, but in another conversation now, so this one gets a
			// pane of its own under an id of its own.
			paneID = uuid.NewString()
		} else {
			// Registered but on screen nowhere. Reusing the id below would
			// drop it from the registry with its process still running, so
			// end it first.
			w.destroyPane(id)
		}
	}
	if cwd == "" {
		cwd = w.activeRoot
	}
	// The pane runs whichever agent recorded the conversation, since only that
	// agent can reattach to it, whichever agent this project opens new panes
	// with. The one the listing named is asked first, then Claude Code, being
	// where every conversation from before there were other agents lives, then
	// the rest in catalog order. Every API agent is the same chat client
	// keeping one folder of transcripts, so a chat conversation nobody named
	// goes to the first of them.
	c := w.agents()
	specs := c.Specs
	if claude, ok := c.Find("claude"); ok {
		specs = append([]agent.Spec{claude}, specs...)
	}
	if named, ok := c.Find(agentID); ok && agentID != "" {
		specs = append([]agent.Spec{named}, specs...)
	}
	var spec agent.Spec
	for _, s := range specs {
		if transcript.Exists(s, id) {
			spec = s
			break
		}
	}
	if spec.ID == "" {
		return fmt.Errorf("that conversation is no longer stored")
	}

	p := &Pane{
		ID:     paneID,
		Kind:   session.KindClaude,
		Agent:  spec.ID,
		Cwd:    cwd,
		Name:   filepath.Base(cwd),
		Root:   w.projectFor(cwd),
		Branch: branchOf(cwd),
	}
	if paneID != id {
		p.Conversation = id
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

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/perch/internal/layout"
	"github.com/jmwri/perch/internal/session"
	"github.com/jmwri/perch/internal/store"
	"github.com/jmwri/perch/internal/workspace"
)

// The workspace is not safe for concurrent mutation, and it is now reached
// from several connection goroutines rather than from one UI loop. Every read
// and write of it is funnelled through a single owner goroutine; `do` queues
// work onto it.
func (s *Server) do(fn func()) {
	select {
	case s.cmds <- fn:
	case <-s.closed:
	}
}

// runLoop owns the workspace.
func (s *Server) runLoop() {
	for {
		select {
		case <-s.closed:
			return
		case fn := <-s.cmds:
			s.guard("applying a change to the workspace", fn)
		}
	}
}

// guard runs fn and survives a panic in it.
//
// A panic in any goroutine ends the process, and ending this process kills
// every agent running under it — work in progress in a dozen panes, thrown
// away because one command from the window reached a pane or a tab that had
// gone. The agents are the valuable thing here and they are not what failed,
// so the panic is reported and the interface carries on. The stack goes to
// the console, where a crash would have put it, and the window is told, since
// the person watching is otherwise left with a click that did nothing.
func (s *Server) guard(doing string, fn func()) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		fmt.Fprintf(os.Stderr, "perch: panic %s: %v\n%s\n", doing, r, debug.Stack())
		s.notifyAll(fmt.Sprintf("something went wrong %s: %v", doing, r), true)
	}()
	fn()
}

// notifyAll sends a one-off message to every connected window.
func (s *Server) notifyAll(text string, isErr bool) {
	data, err := json.Marshal(noticeMsg{Type: "notice", Text: text, Error: isErr})
	if err != nil {
		return
	}
	s.mu.Lock()
	clients := make([]*controlClient, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()
	for _, c := range clients {
		c.send(data)
	}
}

// ---------------------------------------------------------------------------
// Wire types
// ---------------------------------------------------------------------------

type stateMsg struct {
	Type            string              `json:"type"`
	Root            string              `json:"root"`
	ClaudeAvailable bool                `json:"claudeAvailable"`
	ActiveTab       string              `json:"activeTab"`
	Broadcast       bool                `json:"broadcast"`
	Waiting         int                 `json:"waiting"`
	Working         int                 `json:"working"`
	Projects        []projectView       `json:"projects"`
	Tabs            []tabView           `json:"tabs"`
	Panes           map[string]paneView `json:"panes"`
}

// projectView is one open project as the picker and switcher show it.
type projectView struct {
	Root    string `json:"root"`
	Name    string `json:"name"`
	Active  bool   `json:"active"`
	Tabs    int    `json:"tabs"`
	Waiting int    `json:"waiting"`
	Working int    `json:"working"`
}

type tabView struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Focus     string    `json:"focus"`
	Zoom      bool      `json:"zoom"`
	Attention bool      `json:"attention"`
	Root      *nodeView `json:"root"`
}

type nodeView struct {
	ID       string      `json:"id"`
	Pane     string      `json:"pane,omitempty"`
	Dir      string      `json:"dir,omitempty"`
	Weight   float64     `json:"weight"`
	Children []*nodeView `json:"children,omitempty"`
}

type paneView struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Cwd    string `json:"cwd"`
	Branch string `json:"branch"`
	// Project names the pane's own project, and is sent only when that is not
	// the project of the tab the pane is drawn on. The header shows it so that
	// a tab holding agents from two projects says which is which; leaving it
	// out for the ordinary pane keeps the header uncluttered.
	Project   string `json:"project,omitempty"`
	Status    string `json:"status"`
	Detail    string `json:"detail"`
	Err       string `json:"err,omitempty"`
	Broadcast bool   `json:"broadcast"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
	Dirty     int    `json:"dirty"`
	Untracked int    `json:"untracked"`
	Ahead     int    `json:"ahead"`
	Behind    int    `json:"behind"`
}

type noticeMsg struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Error bool   `json:"error"`
}

// command is a request from the front end.
type command struct {
	Cmd       string    `json:"cmd"`
	ID        string    `json:"id"`
	Node      string    `json:"node"`
	Root      string    `json:"root"`
	Dir       string    `json:"dir"`
	Kind      string    `json:"kind"`
	Text      string    `json:"text"`
	Path      string    `json:"path"`
	Cols      int       `json:"cols"`
	Rows      int       `json:"rows"`
	Weights   []float64 `json:"weights"`
	Submit    bool      `json:"submit"`
	Base      string    `json:"base"`
	Force     bool      `json:"force"`
	Push      bool      `json:"push"`
	Tasks     []string  `json:"tasks"`
	Worktrees bool      `json:"worktrees"`
	// Target names what a drag was dropped on: a pane for movePane and
	// swapPanes, a tab for movePaneToTab, moveTab and mergeTab.
	Target string `json:"target"`
	// Edge is which side of the target a dragged pane landed on.
	Edge  string `json:"edge"`
	Trust bool   `json:"trust"`
	Split bool   `json:"split"`
}

// ---------------------------------------------------------------------------
// Snapshot
// ---------------------------------------------------------------------------

// snapshot builds the full state. It must run on the workspace goroutine.
func (s *Server) snapshot() stateMsg {
	ws := s.ws
	waiting, working := ws.AttentionCount()

	msg := stateMsg{
		Type:            "state",
		Root:            ws.ActiveRoot(),
		ClaudeAvailable: ws.ClaudeAvailable(),
		ActiveTab:       ws.ActiveTabID(),
		Broadcast:       ws.Broadcast,
		Waiting:         waiting,
		Working:         working,
		Panes:           map[string]paneView{},
	}
	// These are sized rather than grown, and made rather than left nil: the
	// window walks them without checking them first, so an empty one has to
	// arrive as an empty array. Closing a project's last tab leaves no visible
	// tabs at all, and a nil slice would encode as null and take the interface
	// down instead of showing its empty state.
	projects := ws.Projects()
	msg.Projects = make([]projectView, 0, len(projects))
	for _, p := range projects {
		msg.Projects = append(msg.Projects, projectView{
			Root: p.Root, Name: p.Name, Active: p.Active,
			Tabs: p.Tabs, Waiting: p.Waiting, Working: p.Working,
		})
	}

	// Only the active project's tabs are rendered; the rest keep running.
	tabs := ws.VisibleTabs()
	msg.Tabs = make([]tabView, 0, len(tabs))
	// The pane ids of the tab being encoded, reused from one tab to the next.
	// They are collected on the way through the tree rather than asked for
	// separately, since walking it again for them costs a slice per node.
	var ids []string
	for _, t := range tabs {
		ids = ids[:0]
		root := encodeNode(t.Tree, &ids)
		msg.Tabs = append(msg.Tabs, tabView{
			ID:        t.ID,
			Title:     t.Title,
			Focus:     t.Focus,
			Zoom:      t.Zoom,
			Attention: ws.TabNeedsAttention(t),
			Root:      root,
		})
		// Cleaning the tab's directory once rather than once per pane: it is
		// the same answer every time round.
		tabRoot := filepath.Clean(t.Root)
		for _, id := range ids {
			p := ws.Pane(id)
			if p == nil {
				continue
			}
			st, detail := p.Status()
			pv := paneView{
				ID:        p.ID,
				Kind:      kindName(p.Kind),
				Name:      p.Name,
				Cwd:       p.Cwd,
				Branch:    p.Branch,
				Status:    st.String(),
				Detail:    detail,
				Broadcast: ws.InBroadcast(p.ID),
			}
			// The common case is a pane of the tab's own project, where the
			// two are the same string and there is nothing to clean or fold.
			if paneRoot := ws.RootOf(p.ID); paneRoot != "" && paneRoot != t.Root &&
				!strings.EqualFold(filepath.Clean(paneRoot), tabRoot) {
				pv.Project = filepath.Base(paneRoot)
			}
			if p.Err != nil {
				pv.Err = p.Err.Error()
			}
			if p.Sess != nil {
				pv.Cols, pv.Rows = p.Sess.Size()
			}
			pv.Dirty, pv.Untracked = p.Git.Dirty, p.Git.Untracked
			pv.Ahead, pv.Behind = p.Git.Ahead, p.Git.Behind
			msg.Panes[p.ID] = pv
		}
	}
	return msg
}

// encodeNode turns a layout tree into what the window lays out, appending the
// id of every pane it holds to panes on the way through, in tree order.
func encodeNode(n *layout.Node, panes *[]string) *nodeView {
	if n == nil {
		return nil
	}
	w := n.Weight
	if w <= 0 {
		w = 1
	}
	out := &nodeView{ID: n.ID, Weight: w}
	if n.IsLeaf() {
		out.Pane = n.Pane
		if n.Pane != "" {
			*panes = append(*panes, n.Pane)
		}
		return out
	}
	out.Dir = "v"
	if n.Dir == layout.Horizontal {
		out.Dir = "h"
	}
	out.Children = make([]*nodeView, 0, len(n.Children))
	for _, c := range n.Children {
		out.Children = append(out.Children, encodeNode(c, panes))
	}
	return out
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

func parseDir(s string) layout.Dir {
	if s == "v" {
		return layout.Vertical
	}
	return layout.Horizontal
}

// parseEdge reads which side of a pane a drag was dropped on. An unknown edge
// is treated as the right-hand one, which is where a plain split would go.
func parseEdge(s string) layout.Edge {
	switch s {
	case "left":
		return layout.EdgeLeft
	case "top":
		return layout.EdgeTop
	case "bottom":
		return layout.EdgeBottom
	default:
		return layout.EdgeRight
	}
}

// parseDirection reads a keyboard movement direction.
func parseDirection(s string) layout.Direction {
	switch s {
	case "left":
		return layout.Left
	case "up":
		return layout.Up
	case "down":
		return layout.Down
	default:
		return layout.Right
	}
}

// broadcastState pushes the current state to every connected window, unless
// that state is the one they were last sent.
//
// Wake fires on every chunk of output an agent produces, so with a few agents
// talking this runs many times a second. Almost none of those bursts change
// anything the window draws: the snapshot carries statuses, names and counts,
// not terminal output. Re-sending an identical one costs a frame per window
// and, on the far side, a parse and a full re-render of the tab bar, every
// pane header and the summary. Comparing the encoded bytes is far cheaper
// than either, and skipping the send cannot leave a window behind, since
// lastState is by definition what every window already holds and one that
// connected later was handed a snapshot of its own that is at least as new.
func (s *Server) broadcastState() {
	// Nobody to tell. A detached run sits like this for hours while the agents
	// carry on producing output, and every chunk of it wakes the server:
	// building a snapshot and encoding it for no window at all is the one part
	// of that work that buys nothing at any point. The git loop stands down
	// for the same reason.
	if s.ClientCount() == 0 {
		return
	}
	s.do(func() {
		data, err := json.Marshal(s.snapshot())
		if err != nil {
			return
		}
		if bytes.Equal(data, s.lastState) {
			return
		}
		s.lastState = data
		s.mu.Lock()
		clients := make([]*controlClient, 0, len(s.clients))
		for c := range s.clients {
			clients = append(clients, c)
		}
		s.mu.Unlock()
		for _, c := range clients {
			c.sendState(data)
		}
	})
}

// ---------------------------------------------------------------------------
// Control connection
// ---------------------------------------------------------------------------

type controlClient struct {
	conn *websocket.Conn
	out  chan []byte

	// pending is the newest snapshot not yet written, held apart from out
	// because snapshots supersede one another. See sendState.
	mu      sync.Mutex
	pending []byte
	ready   chan struct{}
}

// send queues a one-off message, dropping it if the window cannot keep up.
func (c *controlClient) send(data []byte) {
	select {
	case c.out <- data:
	default:
	}
}

// sendState queues a snapshot, replacing any earlier one still waiting.
//
// A window that has fallen behind — busy rendering, or on a socket that has
// stopped draining — used to have its newest snapshot dropped once the queue
// filled, and would then sit showing state that had since changed. It was
// covered up by the flood of identical snapshots that followed, one of which
// would eventually get through; now that an unchanged snapshot is not sent
// again, nothing would correct it until the next real change. Keeping the
// newest one aside instead means a slow window skips the snapshots it missed
// and lands on the current one, which is all it ever wanted, and leaves the
// queue for the notices, which do not supersede each other and must not be
// pushed out by a burst of state.
func (c *controlClient) sendState(data []byte) {
	c.mu.Lock()
	c.pending = data
	c.mu.Unlock()
	select {
	case c.ready <- struct{}{}:
	default:
	}
}

// takeState removes the waiting snapshot, if there still is one.
func (c *controlClient) takeState() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	data := c.pending
	c.pending = nil
	return data
}

func (s *Server) handleControl(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"127.0.0.1:*", "localhost:*"},
	})
	if err != nil {
		return
	}
	conn.SetReadLimit(1 << 20)

	c := &controlClient{
		conn:  conn,
		out:   make(chan []byte, 64),
		ready: make(chan struct{}, 1),
	}
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()
	// The git summaries are only kept current while somebody is looking, so
	// this window's arrival is what makes them current again.
	s.RefreshGitNow()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// http.Shutdown does not touch a hijacked connection, so a closing server
	// would otherwise leave this one open with its two goroutines parked on it
	// for ever. Tie the connection's lifetime to the server's.
	go func() {
		select {
		case <-s.closed:
			cancel()
		case <-ctx.Done():
		}
	}()
	go c.writeLoop(ctx)

	// The key table and the preferences come first: the palette and the
	// first-run hints are drawn from them, and both are wanted before the
	// first keystroke. The state follows so the window can render.
	s.do(func() {
		s.sendHello(c)
		if data, err := json.Marshal(s.snapshot()); err == nil {
			c.sendState(data)
		}
		// What was last broadcast no longer describes what every window holds.
		// Nothing was broadcast at all while there were no windows, so the
		// state may since have moved away and come back to it; comparing
		// against it would then skip a change this window has not been told
		// about. Forgetting it costs one extra broadcast per window opened.
		s.lastState = nil
	})

	defer func() {
		s.mu.Lock()
		delete(s.clients, c)
		remaining := len(s.clients)
		s.mu.Unlock()
		cancel()
		_ = conn.CloseNow()
		if remaining == 0 && s.OnLastClientGone != nil {
			s.OnLastClientGone()
		}
	}()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var cmd command
		if json.Unmarshal(data, &cmd) != nil {
			continue
		}
		s.guard("handling "+cmdName(cmd.Cmd), func() { s.handleCommand(c, cmd) })
	}
}

func (c *controlClient) writeLoop(ctx context.Context) {
	for {
		// The queue goes first. It carries the key table and the preferences a
		// window is given before anything else, which the palette and the
		// first-run hints are drawn from, and the notices, which are the only
		// place a refused worktree or a failed commit is ever reported. A
		// snapshot supersedes the one before it and can wait a moment for
		// them; neither of them can wait for it. Both are sent in bursts and
		// then stop, so this cannot starve the state.
		select {
		case <-ctx.Done():
			return
		case data := <-c.out:
			if !c.write(ctx, data) {
				return
			}
			continue
		default:
		}
		select {
		case <-ctx.Done():
			return
		case data := <-c.out:
			if !c.write(ctx, data) {
				return
			}
		case <-c.ready:
			if data := c.takeState(); data != nil && !c.write(ctx, data) {
				return
			}
		}
	}
}

// write sends one message and reports whether the connection is still usable.
func (c *controlClient) write(ctx context.Context, data []byte) bool {
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.conn.Write(writeCtx, websocket.MessageText, data) == nil
}

// sendJSON encodes a message to a single window.
func (c *controlClient) sendJSON(v any) {
	if data, err := json.Marshal(v); err == nil {
		c.send(data)
	}
}

// notify sends a one-off message to a single window.
func (c *controlClient) notify(text string, isErr bool) {
	if data, err := json.Marshal(noticeMsg{Type: "notice", Text: text, Error: isErr}); err == nil {
		c.send(data)
	}
}

// ---------------------------------------------------------------------------
// Commands
// ---------------------------------------------------------------------------

// handleCommand applies a request from a window. Everything that touches the
// workspace runs on its owner goroutine; anything slow (git) is done outside.
func (s *Server) handleCommand(c *controlClient, cmd command) {
	switch cmd.Cmd {
	case "worktrees":
		s.listWorktrees(c)
		return
	case "worktreeAdd":
		s.addWorktree(c, cmd.Text, cmd.Base, cmd.Path)
		return
	case "worktreeRemove":
		s.removeWorktree(c, cmd.Path, cmd.Force)
		return
	case "worktreePrune":
		s.pruneWorktrees(c)
		return
	case "browse":
		s.browse(c, cmd.Path)
		return
	case "fanoutPreview":
		s.previewFanout(c, cmd.ID)
		return
	case "fanout":
		s.runFanout(c, cmd.ID, cmd.Tasks, cmd.Worktrees, cmd.Split, cmd.Trust)
		return
	case "agents":
		s.listAgents(c)
		return
	case "revealPane":
		s.revealPane(cmd.Root, cmd.Node, cmd.ID)
		return
	case "changes":
		s.listChanges(c, cmd.Path)
		return
	case "diff":
		s.showDiff(c, cmd.Path, cmd.Text)
		return
	case "commit":
		s.commitChanges(c, cmd.Path, cmd.Text, cmd.Push)
		return
	case "gitPush":
		s.runRemote(c, "push", cmd.Path)
		return
	case "gitPull":
		s.runRemote(c, "pull", cmd.Path)
		return
	case "gitFetch":
		s.runRemote(c, "fetch", cmd.Path)
		return
	case "conversations":
		s.listConversations(c, cmd.Path)
		return
	case "resumeConversation":
		s.resumeConversation(c, cmd.ID, cmd.Path, titleFor(cmd.Text, cmd.Path))
		return
	case "recents":
		s.recents(c)
		return
	case "helpSeen":
		s.markHelpSeen()
		return
	case "dismissTip":
		s.dismissTip(cmd.ID)
		return
	case "forgetRecent":
		if err := store.ForgetRecent(cmd.Root); err != nil {
			// The list is about to be sent again with the project still on
			// it; without this the entry just refuses to go away.
			c.notify("could not forget "+filepath.Base(cmd.Root)+": "+err.Error(), true)
		}
		s.recents(c)
		return
	}

	s.do(func() {
		ws := s.ws
		switch cmd.Cmd {
		case "newTab":
			ws.NewTab(parseKind(cmd.Kind), cmd.Path, cmd.Text)
		case "closeTab":
			ws.CloseTab(cmd.ID)
		case "selectTab":
			ws.SelectTab(cmd.ID)
		case "nextTab":
			ws.NextTab()
		case "prevTab":
			ws.PrevTab()
		case "renameTab":
			t := ws.Tab(cmd.ID)
			if t == nil {
				return
			}
			title := tabTitle(cmd.Text)
			if title == "" {
				c.notify("a tab name is required", true)
				return
			}
			t.Title = title
			// A title chosen by hand is not replaced by a later prompt.
			t.AutoTitle = false
		case "openProject":
			// A path that is not absolute is resolved against the directory
			// perch was launched from, which the window knows nothing about
			// and did not mean. An empty one resolves to that directory
			// exactly: it opens as a project, becomes the active one, and gets
			// an agent started in it, while the projects the person was
			// working in drop off the tab bar until they think to close it
			// again. Everything the page sends here comes from a directory
			// listing or the recent list and is absolute already.
			if !filepath.IsAbs(cmd.Path) {
				c.notify("a project has to be named by its full path", true)
				return
			}
			if err := ws.OpenProject(cmd.Path); err != nil {
				c.notify(err.Error(), true)
				return
			}
			c.notify("opened "+filepath.Base(cmd.Path), false)
		case "selectProject":
			ws.SelectProject(cmd.Root)
		case "closeProject":
			ws.CloseProject(cmd.Root)
		case "splitPane":
			if !focusFor(ws, cmd.ID) {
				c.notify(paneGone, true)
				return
			}
			// A root names another open project to split into, which is how a
			// tab comes to hold agents from two projects at once. A path names
			// a directory, which is how the worktree panel puts an agent into
			// another checkout of the project it is already in.
			if cmd.Root != "" {
				ws.SplitPaneInProject(parseDir(cmd.Dir), parseKind(cmd.Kind), cmd.Root)
				break
			}
			ws.SplitPaneIn(parseDir(cmd.Dir), parseKind(cmd.Kind), cmd.Path)
		case "closePane":
			if !focusFor(ws, cmd.ID) {
				c.notify(paneGone, true)
				return
			}
			ws.ClosePane()
		case "focusPane":
			ws.FocusPane(cmd.ID)
		case "restartPane":
			if !focusFor(ws, cmd.ID) {
				c.notify(paneGone, true)
				return
			}
			ws.RestartPane()
		case "toggleZoom":
			if !focusFor(ws, cmd.ID) {
				c.notify(paneGone, true)
				return
			}
			ws.ToggleZoom()
		case "movePane":
			if err := ws.MovePane(cmd.ID, cmd.Target, parseEdge(cmd.Edge)); err != nil {
				c.notify(err.Error(), true)
				return
			}
		case "swapPanes":
			if err := ws.SwapPanes(cmd.ID, cmd.Target); err != nil {
				c.notify(err.Error(), true)
				return
			}
		case "movePaneDir":
			if err := ws.MovePaneDir(parseDirection(cmd.Dir)); err != nil {
				c.notify(err.Error(), true)
				return
			}
		case "movePaneToTab":
			if err := ws.MovePaneToTab(cmd.ID, cmd.Target); err != nil {
				c.notify(err.Error(), true)
				return
			}
		case "movePaneToNewTab":
			if err := ws.MovePaneToNewTab(cmd.ID); err != nil {
				c.notify(err.Error(), true)
				return
			}
		case "moveTab":
			if err := ws.MoveTab(cmd.ID, cmd.Target); err != nil {
				c.notify(err.Error(), true)
				return
			}
		case "mergeTab":
			if err := ws.MergeTab(cmd.ID, cmd.Target, parseDir(cmd.Dir)); err != nil {
				c.notify(err.Error(), true)
				return
			}
		case "mergeAllTabs":
			if err := ws.MergeTabsInProject(cmd.ID, parseDir(cmd.Dir)); err != nil {
				c.notify(err.Error(), true)
				return
			}
		case "setWeights":
			if t := ws.CurrentTab(); t != nil {
				if n := t.Tree.FindNode(cmd.Node); n != nil {
					n.SetChildWeights(cmd.Weights)
				}
			}
		case "resize":
			ws.ResizePaneTerminal(cmd.ID, cmd.Cols, cmd.Rows)
		case "toggleBroadcast":
			ws.ToggleBroadcast()
		case "toggleBroadcastMember":
			if !focusFor(ws, cmd.ID) {
				c.notify(paneGone, true)
				return
			}
			ws.ToggleBroadcastMember()
		case "sendPrompt":
			ws.SendPrompt(cmd.Text, true)
		case "save":
			_ = ws.SaveAll()
		case "detach":
			// Keep the agents running after the window goes; the window closes
			// itself once it has been told the detach took effect.
			s.Detach()
			_ = ws.SaveAll()
			c.sendJSON(map[string]any{"type": "detached"})
		case "quit":
			_ = ws.SaveAll()
			go s.requestQuit()
		default:
			return
		}
		s.wakeAsked()
	})
}

// cmdName names a command for a message. The name arrives from the page, so it
// is clamped rather than repeated back whole.
func cmdName(cmd string) string {
	if cmd == "" {
		return "a command"
	}
	const limit = 24
	if r := []rune(cmd); len(r) > limit {
		cmd = string(r[:limit]) + "…"
	}
	return "the " + cmd + " command"
}

// paneGone is what a window is told when it acts on a pane that has since
// been closed, most often because another window closed it first.
const paneGone = "that pane is no longer open"

// focusFor moves focus onto the pane a command names and reports whether the
// command should go ahead.
//
// The commands that use it all act on whichever pane has focus. FocusPane
// ignores a pane that is not in the current tab, so a stale click — on a pane
// closed a moment ago, or from a window still showing an older layout — would
// leave focus where it was and quietly close or restart the wrong agent
// instead. An empty id is the keyboard's way of saying "the focused pane" and
// is always allowed.
func focusFor(ws *workspace.Workspace, id string) bool {
	if id == "" {
		return true
	}
	ws.FocusPane(id)
	t := ws.CurrentTab()
	return t != nil && t.Focus == id
}

// tabTitle cleans up a hand-typed tab name. The front end only checks that
// something was typed, so a name of nothing but spaces would otherwise leave a
// tab with no label at all and no way to tell it from its neighbours, and a
// pasted paragraph would push every other tab off the bar.
func tabTitle(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	// Longer than the titles the workspace picks for itself, since this one
	// was chosen deliberately, but still bounded.
	const limit = 40
	if r := []rune(s); len(r) > limit {
		return strings.TrimSpace(string(r[:limit])) + "…"
	}
	return s
}

// paneByID looks a pane up on the workspace goroutine.
func (s *Server) paneByID(id string) *workspace.Pane {
	done := make(chan *workspace.Pane, 1)
	s.do(func() { done <- s.ws.Pane(id) })
	select {
	case p := <-done:
		return p
	case <-s.closed:
		return nil
	case <-time.After(5 * time.Second):
		return nil
	}
}

// activeRoot reads the active project's root on the workspace goroutine.
// ActiveRoot is a plain field read with no lock behind it, so the worktree and
// review commands — which all run on a connection goroutine — cannot call it
// directly without racing whichever command last switched project.
func (s *Server) activeRoot() string {
	done := make(chan string, 1)
	s.do(func() { done <- s.ws.ActiveRoot() })
	select {
	case root := <-done:
		return root
	case <-s.closed:
		return ""
	}
}

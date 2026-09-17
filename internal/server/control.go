package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/layout"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/spend"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/workspace"
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

// ask runs fn on the workspace goroutine and returns what it produced. ok is
// false when no answer is coming: the server closed first -- do drops work once
// it is shutting down -- or fn panicked. A caller left waiting for an answer
// that never comes strands the connection or request it is serving, and many
// ask from a window's own read loop, so that window would go on looking
// connected while ignoring everything it was sent.
func ask[T any](s *Server, fn func() T) (v T, ok bool) {
	done := make(chan T, 1)
	failed := make(chan struct{})
	s.do(func() {
		defer func() {
			if r := recover(); r != nil {
				close(failed)
				// guard reports it to every window, as it always has.
				panic(r)
			}
		}()
		done <- fn()
	})
	select {
	case v = <-done:
		return v, true
	case <-failed:
		return v, false
	case <-s.closed:
		return v, false
	}
}

// runLoop owns the workspace. It closes loopDone as it returns, which is what
// Stopped reports.
func (s *Server) runLoop() {
	defer close(s.loopDone)
	for {
		select {
		case <-s.closed:
			return
		case fn := <-s.cmds:
			// With changes queued and the server closed, select takes either
			// at random, so this went on applying queued changes after Close
			// had returned -- beside the shutdown saving and then closing the
			// very workspace they changed. One taken off the queue once the
			// server has closed is dropped instead; ask and everything else
			// waiting on an answer stop waiting on closed.
			select {
			case <-s.closed:
				return
			default:
			}
			s.guard("applying a change to the workspace", fn)
		}
	}
}

// Stopped is closed once the workspace goroutine has let go of the workspace
// for good: after Close, and after the change it was applying as the server
// closed has run its course. Until then the workspace is still that
// goroutine's, and saving or closing it from anywhere else races it.
func (s *Server) Stopped() <-chan struct{} { return s.loopDone }

// guard runs fn and survives a panic in it.
//
// A panic in any goroutine ends the process, and ending this process kills
// every agent running under it — work in progress in a dozen panes, thrown
// away because one command from the window reached a pane or a tab that had
// gone. The agents are the valuable thing here and they are not what failed,
// so the panic is reported and the interface carries on. The stack goes to
// the console, where a crash would have put it, and to error.log, and the
// windows are told, since the person watching is otherwise left with a click
// that did nothing.
func (s *Server) guard(doing string, fn func()) {
	defer s.survive(doing)
	fn()
}

// guardFor is guard for a command from one window, which alone is told.
func (s *Server) guardFor(c *controlClient, doing string, fn func()) {
	defer s.surviveFor(c, doing)
	fn()
}

// survive is guard for a goroutine's own body: deferred first thing in it, it
// keeps a panic in the goroutine's work from ending the process. A reply to a
// window is worked out on a goroutine of its own, reading transcripts, git's
// output and files other programs write -- and a panic on a goroutine nothing
// recovers takes every agent in every project down with it, over one reply to
// one window.
//
// It is for work no one window asked for, and every window is told. Work done
// for one window defers surviveFor instead.
func (s *Server) survive(doing string) {
	if r := recover(); r != nil {
		s.reportPanic(nil, doing, r)
	}
}

// surviveFor is survive for work done for one window, which alone is told
// what went wrong. Every window used to be: the others had asked for nothing,
// a phone reached through the relay was shown an error for a click made at
// the desk, and a window waiting on an answer of its own -- the Changes
// panel's commit, say -- took the notice for that answer.
func (s *Server) surviveFor(c *controlClient, doing string) {
	if r := recover(); r != nil {
		s.reportPanic(c, doing, r)
	}
}

// reportPanic says that a panic was recovered: its stack to the console and to
// error.log, and what was being done to the window that asked, or to every
// window when none did. The stack went only to the console, which a Flockdeck
// started from a shortcut does not have, and was lost.
func (s *Server) reportPanic(c *controlClient, doing string, r any) {
	stack := debug.Stack()
	fmt.Fprintf(os.Stderr, "flockdeck: panic %s: %v\n%s\n", doing, r, stack)
	text := fmt.Sprintf("something went wrong %s: %v", doing, r)
	if logPanic(doing, r, stack) {
		text += " — the details are in error.log in flockdeck's state directory"
	}
	if c != nil {
		c.notify(text, true)
		return
	}
	s.notifyAll(text, true)
}

// logPanic appends a recovered panic and its stack to error.log in the state
// directory, where a failed start is written too, and reports whether it did.
func logPanic(doing string, r any, stack []byte) bool {
	dir, err := store.Dir()
	if err != nil {
		return false
	}
	f, err := os.OpenFile(filepath.Join(dir, "error.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return false
	}
	_, err = fmt.Fprintf(f, "%s panic %s: %v\n%s\n", time.Now().Format(time.RFC3339), doing, r, stack)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err == nil
}

// notifyAll sends a one-off message to every connected window.
func (s *Server) notifyAll(text string, isErr bool) {
	data, err := json.Marshal(noticeMsg{Type: "notice", Text: text, Error: isErr})
	if err != nil {
		return
	}
	for _, c := range s.clientList() {
		c.send(data)
	}
}

// clientList is the windows connected at this moment, copied out so that
// sending to them does not hold the lock a window connecting or leaving needs.
func (s *Server) clientList() []*controlClient {
	return s.listClients(false)
}

// greetedClients is clientList without the windows still waiting for their
// hello, which is who a snapshot is broadcast to: no state may reach a window
// before its key table (see handleControl). A notice may, as it always could.
func (s *Server) greetedClients() []*controlClient {
	return s.listClients(true)
}

func (s *Server) listClients(greetedOnly bool) []*controlClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	clients := make([]*controlClient, 0, len(s.clients))
	for c, greeted := range s.clients {
		if greeted || !greetedOnly {
			clients = append(clients, c)
		}
	}
	return clients
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
	// Agents is the catalog: which agents there are, which of them this
	// machine could start, and what runs when nobody chooses. It rides on the
	// snapshot rather than being asked for, because the picker is opened from
	// a keystroke and should draw itself in the frame that follows it.
	Agents agentCatalog `json:"agents"`
	// Update is the release that has been downloaded and is waiting for a
	// restart, or nil when there is nothing to apply. It rides on the snapshot
	// so the badge appears without the page having to ask.
	Update *UpdateView `json:"update,omitempty"`
	// Recall says the version this instance is running has been pulled since
	// it was installed, a known problem rather than just an update being
	// available, so the window can show something distinct from the update
	// chip Update rides in on. Nil where nothing has recalled it, or nothing
	// has checked yet.
	Recall *RecallView `json:"recall,omitempty"`
	// Remote is the tunnel to the relay, or nil when this machine is not
	// enrolled for remote access — in which case the window shows nothing
	// about it at all.
	Remote *remoteView `json:"remote,omitempty"`
	// CanStartAgent says this instance understands the startAgent command, so
	// a phone can offer to start one. It is always true where this field is
	// sent at all; a window is a phone reading an older desktop's state the
	// moment it is absent, which is what lets it hide the offer instead of
	// sending a command that would be silently dropped.
	CanStartAgent bool `json:"canStartAgent,omitempty"`
	// CanSearchConversation says this instance understands the
	// conversationSearch command, so a phone can offer a search button in the
	// chat header. Always true where sent at all; an older desktop that
	// never sends it is exactly the case a phone should hide the button for,
	// rather than send a command that would be silently dropped.
	CanSearchConversation bool `json:"canSearchConversation,omitempty"`
	// CanMutePane says this instance understands the mutePane command, so a
	// phone can offer to mute one pane's push notifications rather than only
	// all or none. Always true where sent at all; an older desktop that never
	// sends it is exactly the case a phone should hide the offer for, rather
	// than send a command that would be silently dropped.
	CanMutePane bool `json:"canMutePane,omitempty"`
	// CanAutoReview says this instance understands the autoReview command, so
	// a phone can offer to turn auto-review approvals on or off for a pane
	// rather than the offer being silently dropped by an older desktop that
	// never sends this field at all.
	CanAutoReview bool `json:"canAutoReview,omitempty"`
}

// projectView is one open project as the picker and switcher show it.
type projectView struct {
	Root    string `json:"root"`
	Name    string `json:"name"`
	Active  bool   `json:"active"`
	Tabs    int    `json:"tabs"`
	Waiting int    `json:"waiting"`
	Working int    `json:"working"`
	// Panes counts every pane in the project, so the agents list, which the
	// window asks for again when this summary moves, follows a split or a
	// closed idle pane as well as a change of status.
	Panes int `json:"panes"`
	// Members lists every repo inside this project, root and display label.
	// A project nobody has grouped still carries one entry, itself, so the
	// switcher always has a member list to show rather than a special case
	// for a project of one.
	Members []repoView `json:"members"`
	// Archived says this project is archived (see workspace.Project); an
	// open project may still be one, since archiving it does not close it.
	Archived bool `json:"archived,omitempty"`
}

// repoView is one repo inside a project, as the switcher's expandable
// member list shows it.
type repoView struct {
	Root string `json:"root"`
	Name string `json:"name"`
}

type tabView struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Focus     string    `json:"focus"`
	Zoom      bool      `json:"zoom"`
	Attention bool      `json:"attention"`
	Root      *nodeView `json:"root"`
	// Named says the title was chosen by hand, which is when the rename
	// dialog offers to go back to the automatic one.
	Named bool `json:"named,omitempty"`
	// Delegated says a fan-out placed its own children into this tab (see
	// workspace.Tab.Delegated), which is what tells the window a settled
	// multi-pane tab is delegated work safe to roll up into a summary card,
	// rather than an ordinary split a person happens to have left idle.
	Delegated bool `json:"delegated,omitempty"`
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
	Project string `json:"project,omitempty"`
	// Parent is the pane whose agent started this one with its own `flockdeck
	// spawn` (workspace.Pane.Parent), sent only while that pane is still
	// open -- the same rule agentView.Parent already follows. It is what
	// lets a window group this pane's waiting or settled state under the
	// job it belongs to, rather than showing it as an unrelated pane.
	Parent string `json:"parent,omitempty"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	// StatusSince is when the current status began, RFC 3339 -- how the phone's
	// chat view times "Working for 3m" without polling: it ticks the gap to now
	// locally rather than asking again every second. Left out for a pane with
	// no session (Sess is nil), which has no status to time either.
	StatusSince string `json:"statusSince,omitempty"`
	// Ask is the question an AskUserQuestion call is putting to the user, and
	// Permission what a permission prompt for any other tool wants to do --
	// built from the same PreToolUse call's own input (see waitingViews), so
	// the phone's chat view can draw a real card without reading the pane's
	// screen. Both are left out for a pane not waiting on either.
	Ask        *askView        `json:"ask,omitempty"`
	Permission *permissionView `json:"permission,omitempty"`
	// Muted says a phone asked not to be pushed to about this pane's waits.
	// It still shows waiting everywhere this view is drawn -- the mute is
	// about the phone's own notifications, not the pane's status. See
	// Pane.Muted.
	Muted bool `json:"muted,omitempty"`
	// AutoReview says this pane's PreToolUse calls are put to auto-review
	// approvals before Claude Code would otherwise show its own permission
	// prompt, and AutoApproved counts how many it has let through unasked so
	// far. See Pane.AutoReview and Pane.AutoApproved.
	AutoReview   bool `json:"autoReview,omitempty"`
	AutoApproved int  `json:"autoApproved,omitempty"`
	// RemoteViewers is the device name of every window reached through the
	// relay that has this pane open right now, in its chat view or its
	// terminal -- so a window at the desk can show that a phone, its own
	// owner's or a colleague's, is also looking at or driving this pane,
	// rather than text appearing in it with nothing saying why. Left out
	// when nobody reached through the relay has it open. See
	// remoteviewers.go.
	RemoteViewers []string `json:"remoteViewers,omitempty"`
	// RemoteInsecure says one of those windows -- a terminal reached through
	// the relay, specifically -- is not end-to-end encrypted: a browser that
	// predates the feature, or one this host's own handshake could not
	// complete. Left out, the zero value, when every remote terminal on this
	// pane is encrypted, or none is open at all. See remoteviewers.go and
	// internal/e2e.
	RemoteInsecure bool `json:"remoteInsecure,omitempty"`
	// Last is this pane's agent's latest reply, trimmed for the phone's
	// inbox row -- left out for a shell, and for an agent pane whose
	// adapter has not seen a reply yet. See preview.go.
	Last *lastReplyView `json:"last,omitempty"`
	// Agent and Model are what the pane is running, drawn in the header beside
	// the branch. Both are left out for a shell, which is running neither.
	Agent string `json:"agent,omitempty"`
	Model string `json:"model,omitempty"`
	// Routed names the routing rule that chose the model, RoutedFrom the
	// model the pane would otherwise have run, and Route whether that moved
	// it "down" or "up". All are left out for a model chosen any other way.
	// RoutedFromAgent names the agent RoutedFrom belongs to, and is left out
	// unless routing moved the pane to another agent, not only another model.
	Routed          string `json:"routed,omitempty"`
	RoutedFrom      string `json:"routedFrom,omitempty"`
	RoutedFromAgent string `json:"routedFromAgent,omitempty"`
	Route           string `json:"route,omitempty"`
	Err             string `json:"err,omitempty"`
	Broadcast       bool   `json:"broadcast"`
	Cols            int    `json:"cols"`
	Rows            int    `json:"rows"`
	Dirty           int    `json:"dirty"`
	Untracked       int    `json:"untracked"`
	Ahead           int    `json:"ahead"`
	Behind          int    `json:"behind"`
	// GitTimedOut says the last read of the pane's checkout gave up before git
	// answered, so the four counts above are the ones read before that and may
	// no longer be true. The header says so instead of showing them.
	GitTimedOut bool `json:"gitTimedOut,omitempty"`

	// What the pane's process and everything it has spawned are costing the
	// machine. Left out when there is nothing to report -- a pane with no
	// process, or a platform that cannot say -- so that the header shows
	// nothing rather than a figure of zero.
	CPU   float64 `json:"cpu,omitempty"`
	RSS   uint64  `json:"rss,omitempty"`
	Procs int     `json:"procs,omitempty"`

	// Spend is what the pane's agent has spent in its conversation and how
	// near it is to its limits, as far as it has said. Left out for a pane
	// whose agent reports nothing.
	Spend *spend.PaneView `json:"spend,omitempty"`
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
	// Size is the terminal font size for fontSize, a number of lines for
	// scrollback, and a width in pixels for railWidth.
	Size int `json:"size"`
	// Agent and Model are what the picker chose, carried on newTab, splitPane
	// and spawn. Both empty means "whatever this project runs by default",
	// which is what every keystroke that does not go through the picker sends
	// — so splitting with the default agent stays one keystroke and gains
	// nothing it has to say.
	Agent string `json:"agent"`
	Model string `json:"model"`
	// TaskAgents and TaskModels are a fan-out's per-row overrides, positional
	// against Tasks. They are parallel arrays rather than a list of objects so
	// that the tasks stay exactly where they have always been on the wire: a
	// window that knows nothing about agents still sends a fan-out this side
	// reads.
	TaskAgents []string `json:"taskAgents"`
	TaskModels []string `json:"taskModels"`
	// TaskRouted and RouteOverrides say which of a fan-out's rows start on the
	// model routing chose, and which routed choices were changed before they
	// started; see fanoutRequest.
	TaskRouted     []string        `json:"taskRouted"`
	RouteOverrides []routeOverride `json:"routeOverrides"`
	// Relay, Name, Join and Invite are what the remote access dialog turns
	// remote access on with, each the flag of the same name to `flockdeck
	// remote enable`, and each may be empty. remoteMove takes the same, the
	// relay then being the one to move to. Name is also the new name
	// remoteRename gives this machine (Kind "host") or the device ID names.
	Relay  string `json:"relay"`
	Name   string `json:"name"`
	Join   string `json:"join"`
	Invite string `json:"invite"`
	// Files and Omitted are the review panel's list as a commit is asked for
	// from it: the files it showed, and how many more it left out. Stamps are
	// those files' stamps as they were listed. The commit is refused when the
	// tree no longer matches them.
	Files   []string          `json:"files"`
	Omitted int               `json:"omitted"`
	Stamps  map[string]string `json:"stamps"`
	// Follow marks a listing the review panel asked for by itself, because
	// the pane counts moved, rather than one somebody opened or refreshed.
	Follow bool `json:"follow"`
	// After, Before and Entry are the chat view's own: a cursor to resume
	// streaming from, an entry id to page backwards from, and an entry id to
	// fetch the full detail of. See conversation.go.
	After  string `json:"after"`
	Before string `json:"before"`
	Entry  string `json:"entry"`
	// Q is conversationSearch's own query: plain text, matched
	// case-insensitively against a pane's whole conversation. See
	// conversation_search.go.
	Q string `json:"q"`
	// MediaType and Data are attachImage's own: a picture's content type as
	// the phone claims it, and its bytes, base64. Name (above) is what the
	// phone calls the file. See imageattach.go.
	MediaType string `json:"mediaType"`
	Data      string `json:"data"`
	// Task and Worktree are startAgent's own: the opening prompt for the agent
	// it starts, and whether it should run in a fresh worktree cut from Root
	// rather than in the project itself. See startAgent.
	Task     string `json:"task"`
	Worktree bool   `json:"worktree"`
	// Muted is mutePane's own: whether the named pane should stop, or resume,
	// being pushed to the phone about. See Workspace.SetPaneMuted.
	Muted bool `json:"muted"`
	// AutoReview is autoReview's own: whether the named pane should start, or
	// stop, having its PreToolUse calls put to auto-review approvals. See
	// Workspace.SetPaneAutoReview.
	AutoReview bool `json:"autoReview"`
	// GHNumber, GHTitle, GHBody, GHBase and GHDraft are the GitHub panel's
	// own, for opening and commenting on pull requests and issues through
	// gh: a PR or issue number, a new item's title and body, the branch to
	// merge into, and whether to open it as a draft. GHState filters a
	// listing ("open", "closed", "merged" for pull requests, "all"); empty
	// means gh's own default, open. See internal/server/ghcli.go.
	GHNumber int    `json:"ghNumber"`
	GHTitle  string `json:"ghTitle"`
	GHBody   string `json:"ghBody"`
	GHBase   string `json:"ghBase"`
	GHDraft  bool   `json:"ghDraft"`
	GHState  string `json:"ghState"`
	// Archived is archiveProject's own: whether the named project should be
	// kept, or no longer kept, out of the picker's ordinary lists. See
	// store.SetProjectArchived.
	Archived bool `json:"archived"`
	// Roots is shared by two commands: groupProjects' own use is the open
	// projects, named by any of their own roots, to merge into one (see
	// Workspace.NewGroupFrom); reorderProjects' own use is every project in
	// a list the picker draws, in the order it should be shown in from now
	// on (see store.ReorderProjects). Which one a given command means is
	// decided by cmd.Type, the same as every other field this struct reuses
	// across commands.
	Roots []string `json:"roots"`
}

// ---------------------------------------------------------------------------
// Snapshot
// ---------------------------------------------------------------------------

// snapshot builds the full state. It must run on the workspace goroutine.
func (s *Server) snapshot() stateMsg {
	ws := s.ws
	waiting, working := ws.AttentionCount()
	// The git loop reads only the checkouts of the project on screen, so one
	// just brought on screen is read straight away rather than when the loop
	// next comes round, up to fifteen seconds later, with its headers showing
	// whatever was read before it was left.
	if root := ws.ActiveRoot(); root != s.gitShown {
		s.gitShown = root
		s.RefreshGitNow()
	}

	msg := stateMsg{
		Type:                  "state",
		Root:                  ws.ActiveRoot(),
		ClaudeAvailable:       ws.ClaudeAvailable(),
		ActiveTab:             ws.ActiveTabID(),
		Broadcast:             ws.Broadcast,
		Waiting:               waiting,
		Working:               working,
		Agents:                s.catalog(),
		Panes:                 map[string]paneView{},
		Update:                s.Update(),
		Recall:                s.Recall(),
		Remote:                s.remoteSnapshot(),
		CanStartAgent:         true,
		CanSearchConversation: true,
		CanMutePane:           true,
		CanAutoReview:         true,
	}
	// These are sized rather than grown, and made rather than left nil: the
	// window walks them without checking them first, so an empty one has to
	// arrive as an empty array. Closing a project's last tab leaves no visible
	// tabs at all, and a nil slice would encode as null and take the interface
	// down instead of showing its empty state.
	projects := ws.Projects()
	msg.Projects = make([]projectView, 0, len(projects))
	names := make(map[string]string, len(projects))
	for _, p := range projects {
		names[p.Root] = p.Name
		members := make([]repoView, 0, len(p.Members))
		for _, m := range p.Members {
			members = append(members, repoView{Root: m.Root, Name: m.Name})
		}
		msg.Projects = append(msg.Projects, projectView{
			Root: p.Root, Name: p.Name, Active: p.Active,
			Tabs: p.Tabs, Waiting: p.Waiting, Working: p.Working, Panes: p.Panes,
			Members:  members,
			Archived: p.Archived,
		})
	}

	// Reading a pane's process tree is the one part of building this that
	// costs anything, so it is left undone while no window is open to read the
	// answer, the same way the branch labels are. The session package takes
	// one reading of the process table every few seconds and shares it between
	// every pane, so this does not get dearer as panes are opened.
	sampleUsage := s.ClientCount() > 0
	spendNow := s.spendAt()
	// Read once for every pane's routing mark rather than once per pane.
	cat := ws.Catalog()

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
			Named:     t.Named,
			Delegated: t.Delegated,
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
				ID:           p.ID,
				Kind:         kindName(p.Kind),
				Name:         p.Name,
				Cwd:          p.Cwd,
				Branch:       p.Branch,
				Status:       st.String(),
				Detail:       detail,
				Broadcast:    ws.InBroadcast(p.ID),
				Muted:        p.Muted,
				AutoReview:   p.AutoReview,
				AutoApproved: p.AutoApproved,
			}
			pv.RemoteViewers = s.remoteViewersFor(p.ID)
			pv.RemoteInsecure = remoteTermViewers.insecure(p.ID)
			if st == session.StatusWaiting && p.Sess != nil {
				pv.Ask, pv.Permission = waitingViews(detail, p.Sess.ToolInput())
			}
			if pv.Kind != "shell" {
				if last, ok := s.preview.get(p.ID); ok {
					pv.Last = &last
				}
			}
			pv.Agent, pv.Model = paneAgent(p)
			pv.Routed, pv.RoutedFrom, pv.RoutedFromAgent, pv.Route = paneRoute(cat, p)
			// The common case is a pane of the tab's own project, where the
			// two are the same string and there is nothing to clean or fold.
			if paneRoot := ws.RootOf(p.ID); paneRoot != "" && paneRoot != t.Root &&
				!samePath(filepath.Clean(paneRoot), tabRoot) {
				pv.Project = projectLabel(names, paneRoot)
			}
			// Left out once the parent has closed, the same rule
			// agentView.Parent already follows, so a window never groups a
			// pane under a job that no longer exists.
			if p.Parent != "" && ws.Pane(p.Parent) != nil {
				pv.Parent = p.Parent
			}
			if p.Err != nil {
				pv.Err = p.Err.Error()
			}
			if p.Sess != nil {
				pv.Cols, pv.Rows = p.Sess.Size()
				pv.StatusSince = p.Sess.StatusSince().Format(time.RFC3339)
				if sampleUsage {
					pv.CPU, pv.RSS, pv.Procs = shownUsage(usageOf(p.Sess))
				}
			}
			pv.Dirty, pv.Untracked = p.Git.Dirty, p.Git.Untracked
			pv.Ahead, pv.Behind = p.Git.Ahead, p.Git.Behind
			pv.GitTimedOut = p.GitTimedOut
			pv.Spend = s.book.Pane(p.ID, spendNow)
			msg.Panes[p.ID] = pv
		}
	}
	return msg
}

// projectLabel names a project the way the project switcher does, given the
// names the open projects go by, falling back to its folder's name.
//
// The switcher's names tell two checkouts called the same thing apart, and a
// pane's own project is shown exactly where that matters: on a tab holding
// agents from two projects, and in the list of every agent there is. Naming
// both "app" there left nothing to tell them apart by.
func projectLabel(names map[string]string, root string) string {
	if n := names[root]; n != "" {
		return n
	}
	return filepath.Base(root)
}

// usageOf reads what a pane's process tree is costing the machine. It is a
// variable so a test can hold the figures still, which the real ones never are.
var usageOf = (*session.Session).Usage

// shownUsage rounds a pane's usage to the precision the window draws it at: a
// whole percent of a processor, and memory to the three significant figures
// the window's formatBytes writes it in.
//
// The process table is read afresh every few seconds, and the raw figures come
// out different every time — the processor share is a smoothed average and the
// memory a count of bytes — so sent as they stood they made every snapshot a
// new one. Each of those was a broadcast to every window, and a parse and a
// re-render there, of a header that then drew exactly what it drew before.
func shownUsage(u session.Usage) (cpu float64, rss uint64, procs int) {
	unit := uint64(1)
	for unit < 1<<40 && u.RSSBytes >= unit*1024 {
		unit *= 1024
	}
	v := float64(u.RSSBytes) / float64(unit)
	if unit > 1 && v < 100 {
		v = math.Round(v*10) / 10
	} else {
		v = math.Round(v)
	}
	return math.Round(u.CPUPercent), uint64(v * float64(unit)), u.Procs
}

// encodeNode turns a layout tree into what the window lays out, appending the
// id of every pane it holds to panes on the way through, in tree order.
func encodeNode(n *layout.Node, panes *[]string) *nodeView {
	if n == nil {
		return nil
	}
	// A weight that is not a usable number is replaced rather than passed on,
	// with the same test the layout applies to one arriving from a drag. Zero
	// is what a layout written before weights were recorded reads back as, and
	// a pane laid out with no width at all is worse than one given an even
	// share. A NaN or an infinity is worse again, and in a way that has nothing
	// to do with the layout: encoding/json will not write one, so the whole
	// state message fails to encode and every window stops being updated —
	// silently, for good, with no way back but restarting.
	w := n.Weight
	if w <= 0 || math.IsNaN(w) || math.IsInf(w, 0) {
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

// kindName is what a pane is on the wire. A pane is an agent or a shell:
// which agent it is travels beside this rather than in it, so that a second
// agent is a row in the catalog rather than a third kind of pane.
func kindName(k session.Kind) string {
	if k == session.KindShell {
		return "shell"
	}
	return "agent"
}

// parseKind reads one back. "claude" is still accepted because a window left
// open across an upgrade goes on sending the word it was built with.
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
// than either.
//
// Skipping the send cannot leave a window behind, because lastState either
// describes what every window holds or is empty. It is set only here, by the
// broadcast that put it in front of all of them at once, and it is emptied
// whenever a window connects — which is what covers the gap where no window
// was open and nothing was broadcast, and the window that then arrived was
// handed a snapshot of its own that this never saw.
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
		for _, c := range s.greetedClients() {
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

	// remote marks a window reached through the relay, and device is which
	// paired device it is on, as the relay reported it.
	remote bool
	device string

	// pending is the newest snapshot not yet written, held apart from out
	// because snapshots supersede one another. See sendState.
	mu      sync.Mutex
	pending []byte
	ready   chan struct{}

	// startAgentLimit and attachImageLimit bound how often this window can
	// start an agent or attach a picture -- see rateLimiter. Each spends real
	// resources (a process and a worktree; a decode and a disk write), and
	// unlike a fan-out's own MaxTasks there was nothing here to stop a window
	// asking for either as fast as it can send frames.
	startAgentLimit  rateLimiter
	attachImageLimit rateLimiter
}

// rateLimiter is a sliding-window limit on how often something may happen:
// at most limit calls within window are let through, per instance. The zero
// value is ready to use.
type rateLimiter struct {
	mu    sync.Mutex
	times []time.Time
	// now is the clock, time.Now when nil. A test sets it so a burst of
	// calls lands inside the window however slowly the machine runs them.
	now func() time.Time
}

// allow reports whether another call may go ahead now, and records it if so.
func (r *rateLimiter) allow(limit int, window time.Duration) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if r.now != nil {
		now = r.now()
	}
	cutoff := now.Add(-window)
	kept := r.times[:0]
	for _, t := range r.times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	r.times = kept
	if len(r.times) >= limit {
		return false
	}
	r.times = append(r.times, now)
	return true
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
	// No origin patterns, for the reason the terminal socket has none: the
	// page opening this must have come from this server's own address, or
	// through the relay from the relay's. This socket used to admit any page
	// on 127.0.0.1 or localhost, whatever its port -- and cookies take no
	// notice of the port, so the user's own dev server, or anything that could
	// be made to serve a page from one, had the token attached for it and
	// could send any command a window can: prompt every agent, open a shell.
	isRemote := fromRemote(r)
	// A window through the relay is often a phone on a metered link, and what
	// it is sent is mostly the same snapshot over and over with a word or two
	// changed: about 5.6 KB for six panes, which deflates to 58 bytes once the
	// last one is in the compressor's window. So it is compressed, if the
	// browser offers to be. A window on this machine is sent the same over
	// loopback, where compressing it buys nothing.
	var opts *websocket.AcceptOptions
	if isRemote {
		opts = &websocket.AcceptOptions{CompressionMode: websocket.CompressionContextTakeover}
	}
	conn, err := websocket.Accept(w, r, opts)
	if err != nil {
		return
	}
	conn.SetReadLimit(1 << 20)

	c := &controlClient{
		conn:   conn,
		out:    make(chan []byte, 64),
		ready:  make(chan struct{}, 1),
		remote: isRemote,
	}
	if isRemote {
		c.device = r.Header.Get("Flockdeck-Remote-Device")
	}
	// The window counts from the moment its socket opens: whether the last
	// window has gone is decided by counting them, and a page being reloaded
	// while the workspace is busy -- opening a project, say -- would otherwise
	// look like nobody there at all. It is sent no state until its hello.
	s.mu.Lock()
	s.clients[c] = false
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
	// Pinged as a terminal socket is (see keepalive). A window that went away
	// without closing -- a laptop shut, a phone gone out of signal while the
	// relay holds its end -- is otherwise found out only when a write to it
	// times out, and with the agents quiet nothing is written: it went on
	// being counted, among the windows through the relay the desk is shown,
	// for as long as the instance ran.
	//
	// Its gauge counts nothing: the control socket's own writes are small and
	// go out through writeLoop, so no frame of its is ever long enough on the
	// way for a ping to have to wait behind it, and it is pinged on every
	// interval, as a terminal socket was before gauges.
	go keepalive(ctx, cancel, conn, new(writeGauge), s.pingInterval, s.pingTimeout)

	// The key table and the preferences come first: the palette and the
	// first-run hints are drawn from them, and both are wanted before the
	// first keystroke. The state follows so the window can render.
	s.do(func() {
		s.sendHello(c)
		// The window is sent the state only from now, with its hello queued
		// ahead of any snapshot. Every broadcast is made on this goroutine,
		// so none can reach it first. Sent them from the moment
		// its socket opened, it was handed the snapshot of a broadcast
		// already queued here before its hello was even queued -- one
		// connection in a few dozen, whenever git or an agent had just
		// changed something.
		//
		// A window that has gone again while this waited its turn is no
		// longer on the list, and is not put back on it.
		s.mu.Lock()
		if _, ok := s.clients[c]; ok {
			s.clients[c] = true
		}
		s.mu.Unlock()
		data, err := json.Marshal(s.snapshot())
		if err == nil {
			c.sendState(data)
		}
		// lastState has to go on describing what every window holds. Nothing
		// was broadcast while there were no windows, so the state may since
		// have moved away and come back to it; comparing against it would then
		// skip a change this window has not been told about. A window that is
		// the only one holds exactly what it was just handed, though, and so
		// does every window when that is what they were last sent. Forgetting
		// it there as well sent the next wake's identical snapshot again, which
		// is the very send lastState is kept to spare.
		switch {
		case err == nil && s.ClientCount() == 1:
			s.lastState = data
		case err == nil && bytes.Equal(data, s.lastState):
		default:
			s.lastState = nil
		}
	})
	// After the hello is handed over, so this window is still sent its key
	// table first.
	s.viewersChanged(c)

	defer func() {
		s.mu.Lock()
		delete(s.clients, c)
		s.mu.Unlock()
		// A window that has gone is watching no pane's conversation either.
		s.convos.dropClient(c)
		// A window that has gone reports no more input from here.
		s.setDeskUsed(c, false)
		cancel()
		_ = conn.CloseNow()
		s.viewersChanged(c)
		// Only the windows on this machine count towards the last one going:
		// see LocalClientCount for why a remote window neither keeps the
		// application alive nor ends it.
		if !c.remote && s.LocalClientCount() == 0 && s.OnLastClientGone != nil {
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
		s.guardFor(c, "handling "+cmdName(cmd.Cmd), func() { s.handleCommand(c, cmd) })
	}
}

// viewersChanged has the windows told that one reached through the relay has
// come or gone. The desk shows how many there are -- somebody may be typing --
// and a phone arriving or leaving changes nothing else a snapshot carries, so
// with the agents quiet the count waited for the next unrelated change.
func (s *Server) viewersChanged(c *controlClient) {
	if c.remote {
		s.Wake()
	}
}

func (c *controlClient) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case data := <-c.out:
			if !c.write(ctx, data) {
				return
			}
		case <-c.ready:
			// Whatever was queued before this snapshot was handed over goes
			// out before it. That is what puts the key table and the
			// preferences in front of the first state, which the palette and
			// the first-run hints are drawn from, and it keeps a notice — the
			// only place a refused worktree or a failed commit is reported —
			// from waiting behind the write of a snapshot that a newer one may
			// supersede anyway. Neither arrives continuously, so the state
			// cannot be starved by them.
			if !c.drainQueue(ctx) {
				return
			}
			if data := c.takeState(); data != nil && !c.write(ctx, data) {
				return
			}
		}
	}
}

// drainQueue writes everything already waiting in the queue, and reports
// whether the connection is still usable.
func (c *controlClient) drainQueue(ctx context.Context) bool {
	for {
		select {
		case data := <-c.out:
			if !c.write(ctx, data) {
				return false
			}
		default:
			return true
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
		// Root names which repo the worktree is made in, for a project
		// spanning more than one; empty means the active project's own, as
		// it always has for a project of one.
		s.addWorktree(c, cmd.Root, cmd.Text, cmd.Base, cmd.Path)
		return
	case "worktreeRemove":
		s.removeWorktree(c, cmd.Root, cmd.Path, cmd.Force)
		return
	case "worktreePrune":
		s.pruneWorktrees(c, cmd.Root)
		return
	case "browse":
		s.browse(c, cmd.Path)
		return
	case "fanoutPreview":
		s.previewFanout(c, cmd.ID)
		return
	case "fanout":
		s.fanout(c, fanoutRequest{
			Parent:     cmd.ID,
			Tasks:      cmd.Tasks,
			Agent:      cmd.Agent,
			Model:      cmd.Model,
			TaskAgents: cmd.TaskAgents,
			TaskModels: cmd.TaskModels,
			TaskRouted: cmd.TaskRouted,
			Overrides:  cmd.RouteOverrides,
			Worktrees:  cmd.Worktrees,
			Split:      cmd.Split,
			Trust:      cmd.Trust,
		})
		return
	case "startAgent":
		s.startAgent(c, cmd)
		return
	case "routeTasks":
		s.routeTasks(c, cmd)
		return
	case "setRouting":
		s.setRouting(c, cmd)
		return
	case "clearRoutingLog":
		s.clearRoutingLog(c)
		return
	case "agents":
		s.listAgents(c)
		return
	case "keys":
		s.listKeys(c)
		return
	case "keySet":
		s.setKey(c, cmd.ID, cmd.Text)
		return
	case "keyClear":
		s.clearKey(c, cmd.ID)
		return
	case "refreshAgents":
		s.refreshAgents()
		return
	case "setAgentDefault":
		s.applyAgentDefault(c, cmd)
		return
	case "setAgentAddress":
		s.setAgentAddress(c, cmd.ID, cmd.Text)
		return
	case "revealPane":
		s.revealPane(c, cmd.Root, cmd.Node, cmd.ID)
		return
	case "changes":
		s.listChanges(c, cmd.Path, cmd.Follow)
		return
	case "diff":
		s.showDiff(c, cmd.Path, cmd.Text)
		return
	case "commit":
		s.commitChanges(c, cmd.Path, cmd.Text, cmd.Push, cmd.Files, cmd.Stamps, cmd.Omitted)
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
	case "ghStatus":
		s.ghStatus(c, cmd.Path)
		return
	case "ghInstall":
		s.ghInstall(c)
		return
	case "ghLogin":
		s.ghLogin(c)
		return
	case "ghLoginCancel":
		s.ghLoginCancel(c)
		return
	case "ghLogout":
		s.ghLogout(c, cmd.Path)
		return
	case "ghPRs":
		s.ghPRs(c, cmd.Path, cmd.GHState)
		return
	case "ghPR":
		s.ghPR(c, cmd.Path, cmd.GHNumber)
		return
	case "ghPRCreate":
		s.ghPRCreate(c, cmd.Path, cmd.GHTitle, cmd.GHBody, cmd.GHBase, cmd.GHDraft)
		return
	case "ghPRComment":
		s.ghPRComment(c, cmd.Path, cmd.GHNumber, cmd.GHBody)
		return
	case "ghIssues":
		s.ghIssues(c, cmd.Path, cmd.GHState)
		return
	case "ghIssue":
		s.ghIssue(c, cmd.Path, cmd.GHNumber)
		return
	case "ghIssueCreate":
		s.ghIssueCreate(c, cmd.Path, cmd.GHTitle, cmd.GHBody)
		return
	case "ghIssueComment":
		s.ghIssueComment(c, cmd.Path, cmd.GHNumber, cmd.GHBody)
		return
	case "ghChecks":
		s.ghChecks(c, cmd.Path)
		return
	case "conversations":
		s.listConversations(c, cmd.Path)
		return
	case "conversationOpen":
		s.conversationOpen(c, cmd.ID, cmd.After)
		return
	case "conversationOlder":
		s.conversationOlder(c, cmd.ID, cmd.Before)
		return
	case "conversationDetail":
		s.conversationDetailReq(c, cmd.ID, cmd.Entry)
		return
	case "conversationClose":
		s.conversationClose(c, cmd.ID)
		return

	case "conversationSearch":
		s.conversationSearch(c, cmd.ID, cmd.Q)
		return
	case "attachImage":
		s.attachImage(c, cmd.ID, cmd.Name, cmd.MediaType, cmd.Data)
		return
	case "resumeConversation":
		s.resumeConversation(c, cmd.ID, cmd.Path, titleFor(cmd.Text, cmd.Path), cmd.Agent)
		return
	case "recents":
		s.recents(c)
		return
	case "remoteDevices":
		s.remoteDevices(c)
		return
	case "remotePair":
		s.remotePair(c, cmd.Kind)
		return
	case "remoteRevoke":
		s.remoteRevoke(c, cmd.ID)
		return
	case "remoteRename":
		s.remoteRename(c, cmd.Kind, cmd.ID, cmd.Name)
		return
	case "remoteEnable":
		s.remoteEnable(c, cmd)
		return
	case "remoteDisable":
		s.remoteDisable(c, cmd.Force)
		return
	case "remoteMove":
		s.remoteMove(c, cmd)
		return
	case "remoteReconnect":
		s.remoteReconnect(c)
		return
	case "helpSeen":
		s.markHelpSeen(c)
		return
	case "dismissTip":
		s.dismissTip(c, cmd.ID)
		return
	case "fontSize":
		s.setFontSize(c, cmd.Size)
		return
	case "notifications":
		s.setNotifications(c, cmd.Kind == "off")
		return
	case "scrollback":
		s.setScrollback(c, cmd.Size)
		return
	case "updates":
		// Whether releases are fetched and staged is the desk's to say, as
		// restarting onto one is: a switch flipped on a phone changed what
		// the machine downloads and puts in place. The window has already
		// moved its switch, so it is sent the preferences as they stand,
		// which moves it back.
		if c.remote {
			c.notify("whether flockdeck checks for updates is set on the machine it runs on, not from a window reached through the relay", true)
			s.do(func() { c.sendJSON(prefsMsg{Type: "prefs", Prefs: s.prefs}) })
			return
		}
		s.setUpdates(c, cmd.Kind == "off")
		return
	case "checkForUpdate":
		// Reaching GitHub, and downloading a release that turns out to be
		// newer, both take a while -- long past what a handler is meant to
		// hold the connection's read loop up for -- so this runs on its own,
		// as restarting onto an update already does below.
		if c.remote {
			c.notify("checking for updates is set on the machine flockdeck runs on, not from a window reached through the relay", true)
			return
		}
		go s.checkForUpdates(c)
		return
	case "listVersions":
		s.listVersions(c)
		return
	case "installVersion":
		s.installVersion(c, cmd.Text)
		return
	case "presence":
		s.setDeskUsed(c, cmd.Kind == "used")
		return
	case "pushNotify":
		s.setPushOff(c, cmd.Kind == "off")
		return
	case "pushAnonymous":
		s.setPushAnonymous(c, cmd.Kind == "on")
		return
	case "pushDelay":
		s.setPushDelay(c, cmd.Size)
		return
	case "resetTips":
		s.resetTips(c)
		return
	case "cursorBlink":
		s.setCursorSteady(c, cmd.Kind == "off")
		return
	case "cursorStyle":
		s.setCursorStyle(c, cmd.Text)
		return
	case "screenReader":
		s.setScreenReader(c, cmd.Kind == "on")
		return
	case "statusLine":
		s.setStatusLine(c, cmd.Text)
		return
	case "fontFamily":
		s.setFontFamily(c, cmd.Text)
		return
	case "railExpanded":
		s.setRailExpanded(c, cmd.Kind == "on")
		return
	case "railWidth":
		s.setRailWidth(c, cmd.Size)
		return
	case "theme":
		s.setTheme(c, cmd.Text)
		return
	case "accentColor":
		s.setAccentColor(c, cmd.Text)
		return
	case "fanOutSameTab":
		s.setFanOutSameTab(c, cmd.Kind == "on")
		return
	case "autoReviewDefault":
		s.setAutoReviewDefault(c, cmd.Kind == "on")
		return
	case "setKeybinding":
		s.setKeybinding(c, cmd.ID, cmd.Text)
		return
	case "resetKeybinding":
		s.resetKeybinding(c, cmd.ID)
		return
	case "resetKeybindings":
		s.resetKeybindings(c)
		return
	case "forgetRecent":
		// On the workspace goroutine, where opening or switching to a project
		// rewrites the same list (TouchRecent). Each is a read and a rewrite
		// of the whole file, and side by side one could put back what the
		// other had just taken out -- as could two windows forgetting at once.
		if err, ok := ask(s, func() error { return store.ForgetRecent(cmd.Root) }); ok && err != nil {
			// The list is about to be sent again with the project still on
			// it; without this the entry just refuses to go away.
			c.notify("could not forget "+filepath.Base(cmd.Root)+": "+err.Error(), true)
		}
		s.recents(c)
		return
	case "renameProject":
		// On the workspace goroutine, guarded against opening or touching a
		// project the same way forgetRecent's own write is: both rewrite the
		// same file, and side by side one could put back what the other had
		// just changed.
		if err, ok := ask(s, func() error { return store.SetProjectName(cmd.Root, cmd.Text) }); ok {
			if err != nil {
				c.notify("could not rename "+filepath.Base(cmd.Root)+": "+err.Error(), true)
			} else {
				// The open project's own name, part of the main snapshot
				// rather than the recent list, has to be told to look again
				// too -- ReloadAgents is why the picker's other settings do
				// the same after a save.
				s.ws.ReloadProjectMeta()
				s.wakeAsked()
			}
		}
		s.recents(c)
		return
	case "archiveProject":
		if err, ok := ask(s, func() error { return store.SetProjectArchived(cmd.Root, cmd.Archived) }); ok {
			if err != nil {
				verb := "archive"
				if !cmd.Archived {
					verb = "unarchive"
				}
				c.notify("could not "+verb+" "+filepath.Base(cmd.Root)+": "+err.Error(), true)
			} else {
				s.ws.ReloadProjectMeta()
				s.wakeAsked()
			}
		}
		s.recents(c)
		return
	case "reorderProjects":
		if err, ok := ask(s, func() error { return store.ReorderProjects(cmd.Roots) }); ok {
			if err != nil {
				c.notify("could not reorder projects: "+err.Error(), true)
			} else {
				s.ws.ReloadProjectMeta()
				s.wakeAsked()
			}
		}
		s.recents(c)
		return
	case "removeProject":
		// Closing it, if it is open, and forgetting it happen as one step on
		// the workspace goroutine, the same way renameProject's write does --
		// a window drawing the picker mid-remove must not see it closed with
		// the recent list still holding it, or forgotten while it is still
		// open and showing in the switcher.
		//
		// The last open project can't be closed (CloseProject itself simply
		// declines), and must not be silently forgotten out from under
		// itself either: the picker would show nothing open at all.
		err, ok := ask(s, func() error {
			if _, open := s.ws.OpenRootFor(cmd.Root); open && len(s.ws.Projects()) <= 1 {
				return fmt.Errorf("%s can't be removed: close another project first, or it would leave nothing open", filepath.Base(cmd.Root))
			}
			if err := s.ws.CloseProject(cmd.Root); err != nil {
				return err
			}
			return store.ForgetRecent(cmd.Root)
		})
		if ok {
			if err != nil {
				c.notify("could not remove "+filepath.Base(cmd.Root)+": "+err.Error(), true)
			} else {
				s.wakeAsked()
			}
		}
		s.recents(c)
		return
	}

	s.do(func() {
		// Recovered here rather than by the workspace goroutine's own guard,
		// so that it is this window that is told.
		defer s.surviveFor(c, "handling "+cmdName(cmd.Cmd))
		ws := s.ws
		switch cmd.Cmd {
		case "newTab":
			s.newTabFor(cmd)
		case "closeTab":
			ws.CloseTab(cmd.ID)
		case "selectTab":
			// A tab bar drawn before another window closed the tab: the click
			// did nothing, and said nothing, as it now says it does.
			if ws.Tab(cmd.ID) == nil {
				c.notify(tabGone, true)
				return
			}
			ws.SelectTab(cmd.ID)
		case "nextTab":
			ws.NextTab()
		case "prevTab":
			ws.PrevTab()
		case "renameTab":
			// The same for a rename dialog, where the name just typed simply
			// vanished.
			if ws.Tab(cmd.ID) == nil {
				c.notify(tabGone, true)
				return
			}
			// An emptied name is how a tab goes back to naming itself: it
			// is what someone tries first, and the rename dialog's "Use the
			// automatic title" sends the same. Refusing it, as this did,
			// left a tab named once with no way back at all.
			if title := tabTitle(cmd.Text); title != "" {
				ws.NameTab(cmd.ID, title)
			} else if !ws.UseAutoTitle(cmd.ID) {
				c.notify("this tab already has its automatic title", false)
			}
		case "openProject":
			// A path that is not absolute is resolved against the directory
			// flockdeck was launched from, which the window knows nothing about
			// and did not mean. An empty one resolves to that directory
			// exactly: it opens as a project, becomes the active one, and gets
			// an agent started in it, while the projects the person was
			// working in drop off the tab bar until they think to close it
			// again. Everything the page sends here comes from a directory
			// listing or the recent list and is absolute already.
			// Quotes around it -- Explorer's "Copy as path" puts them there --
			// are not part of it.
			path := unquotePath(cmd.Path)
			if !filepath.IsAbs(path) {
				c.notify("a project has to be named by its full path", true)
				return
			}
			// The recent-projects list and the folder browser open a project
			// by its path whether it is open already or not, and one that is
			// is only switched to. "opened app" said for that reads as a
			// second copy of it having been started.
			before := len(ws.Projects())
			if err := ws.OpenProject(path); err != nil {
				c.notify(err.Error(), true)
				return
			}
			if len(ws.Projects()) == before {
				c.notify("switched to "+filepath.Base(path)+", which was already open", false)
			} else {
				c.notify("opened "+filepath.Base(path), false)
			}
		case "selectProject":
			ws.SelectProject(cmd.Root)
		case "selectRepo":
			// Picking one entry out of a project's own expanded member list,
			// or a worktree-panel row that names a repo directly: unlike
			// selectProject, this lands on the repo named rather than the
			// one the project was last left on.
			ws.SelectRepo(cmd.Root)
		case "closeProject":
			if err := ws.CloseProject(cmd.Root); err != nil {
				c.notify(err.Error(), true)
			}
		case "addRepoToGroup":
			// Root names the project -- any of its members does -- and Path
			// the folder to add, the same shape as openProject.
			path := unquotePath(cmd.Path)
			if !filepath.IsAbs(path) {
				c.notify("a repo has to be named by its full path", true)
				return
			}
			if err := ws.AddRepoToGroup(cmd.Root, path); err != nil {
				c.notify(err.Error(), true)
				return
			}
			c.notify("added "+filepath.Base(path)+" to the project", false)
		case "removeRepoFromGroup":
			if err := ws.RemoveRepoFromGroup(cmd.Root); err != nil {
				c.notify(err.Error(), true)
			}
		case "renameGroup":
			if err := ws.RenameGroup(cmd.Root, cmd.Text); err != nil {
				c.notify(err.Error(), true)
			}
		case "groupProjects":
			if _, err := ws.NewGroupFrom(cmd.Roots, cmd.Text); err != nil {
				c.notify(err.Error(), true)
			}
		case "splitPane":
			if !focusFor(ws, cmd.ID) {
				c.notify(paneGone, true)
				return
			}
			s.splitPaneFor(cmd)
		case "closePane":
			if !ws.ClosePaneByID(paneIDFor(ws, cmd.ID)) {
				c.notify(paneGone, true)
				return
			}
		case "closeFinishedPanes":
			panes, tabs := ws.CloseFinishedPanes()
			c.notify(closedFinishedNotice(panes, tabs), false)
		case "focusPane":
			ws.FocusPane(cmd.ID)
		case "restartPane":
			if !ws.RestartPaneByID(paneIDFor(ws, cmd.ID)) {
				c.notify(paneGone, true)
				return
			}
		case "toggleZoom":
			if !ws.ToggleZoomByID(paneIDFor(ws, cmd.ID)) {
				c.notify(paneGone, true)
				return
			}
		case "mutePane":
			if !ws.SetPaneMuted(cmd.ID, cmd.Muted) {
				c.notify(paneGone, true)
				return
			}
			s.wakeAsked()
		case "autoReview":
			if !ws.SetPaneAutoReview(cmd.ID, cmd.AutoReview) {
				c.notify(paneGone, true)
				return
			}
			s.wakeAsked()
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
		case "tilePanes":
			if err := ws.TilePanes(); err != nil {
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
			// The bar names the pane it was opened on, and the prompt goes
			// there. Focus can move while it is open -- the desk clicking
			// another pane, a fan-out revealing its first agent, a pane picked
			// from the agents list -- and the prompt went to whichever pane had
			// it by the time it was sent. A pane no longer in the tab on screen
			// is not guessed at. A window that names none still sends to the
			// focused pane.
			focus := ""
			if t := ws.CurrentTab(); t != nil {
				focus = t.Focus
				if cmd.ID != "" {
					if t.Tree.Find(cmd.ID) == nil {
						c.notify("the pane that prompt was written for is no longer in the tab on screen, so it was not sent — ↑ in the prompt bar brings it back", true)
						return
					}
					focus = cmd.ID
				}
			}
			// Only a pane with a process running takes the text. With the
			// focused one stopped and nothing else in the broadcast it went
			// nowhere, while the prompt bar closed as though it had been sent.
			if len(ws.BroadcastTargetsFor(focus)) == 0 {
				c.notify("nothing in this tab is running to send that to — restart the pane and send it again", true)
				return
			}
			ws.SendPromptTo(focus, cmd.Text, true)
		case "save":
			_ = ws.SaveAll()
		case "detach":
			// Keep the agents running after the window goes; the window closes
			// itself once it has been told the detach took effect.
			//
			// A window through the relay closing never stopped anything, so
			// for one there is nothing to detach -- marking the instance would
			// only stop the window on the desk quitting when its owner closes
			// it. Nor is it told it has been detached: a page the browser did
			// not open by script cannot close itself, and one left believing
			// it is on its way out stops reconnecting when the relay blinks,
			// looking live and answering nothing. It is told what is true
			// instead.
			if c.remote {
				c.notify("closing a window reached through the relay never stops the agents, so there is nothing to detach", false)
				return
			}
			s.Detach()
			// A layout that cannot be saved is no reason not to detach: the
			// agents run on, and the save is tried again every half minute.
			// But the window closes itself once told it has detached, and
			// the failure went with it unread, so it is kept open and told.
			if err := ws.SaveAll(); err != nil {
				c.notify("detached, but the layout could not be saved, so a crash now would lose what changed since it last was ("+err.Error()+"); the agents keep running, the save is tried again every half minute, and this window can be closed", true)
				return
			}
			c.sendJSON(map[string]any{"type": "detached"})
		case "quit":
			// Quitting stops every agent on this machine, and the instance
			// cannot be started again from a remote window -- which is why the
			// help promises that a remote window cannot quit it. /quit keeps
			// that promise by wanting the token; this is the other way in.
			if c.remote {
				c.notify("a window reached through the relay cannot quit flockdeck — quit it on the machine it runs on", true)
				return
			}
			if err := ws.SaveAll(); err != nil && !s.askedAgainPastFailedSave() {
				c.notify("the layout could not be saved, so flockdeck did not quit: quitting now would lose what changed since it last was ("+err.Error()+"). Quit again to quit anyway.", true)
				return
			}
			go s.requestQuit()
		case "restart":
			// A restart is a quit that comes back, and stops every agent on
			// the way just as quitting does -- and a relaunch that fails leaves
			// nothing a phone can start again. So it is the desk's, as Quit is.
			// The window hides the offer; this is for one that sends it anyway.
			if c.remote {
				c.notify("a window reached through the relay cannot restart flockdeck — restart it on the machine it runs on", true)
				return
			}
			// The layout is saved here as well as by the shutdown, because a
			// restart is the one quit the user expects to come back to exactly
			// what they were looking at -- and one that could not be saved
			// would come back to something else.
			if err := ws.SaveAll(); err != nil && !s.askedAgainPastFailedSave() {
				c.notify("the layout could not be saved, so flockdeck did not restart: it would come back without what changed since it last was ("+err.Error()+"). Restart again to restart anyway.", true)
				return
			}
			go s.requestRestart()
		default:
			return
		}
		s.wakeAsked()
	})
}

// askedAgainPastFailedSave reports whether a quit or restart may go ahead
// although the layout could not be saved.
//
// Both used to go ahead regardless, and whatever had changed since the last
// save was lost without a word. The first is now turned down, with the reason.
// Asking again soon after is the person having been told and choosing to lose
// it -- a disk that has filled up may stay full, and flockdeck has still to be
// quittable -- so that goes ahead. It runs on the workspace goroutine, which
// is what keeps unsavedAskedAt free of a lock of its own.
func (s *Server) askedAgainPastFailedSave() bool {
	if !s.unsavedAskedAt.IsZero() && time.Since(s.unsavedAskedAt) < askAgainWithin {
		return true
	}
	s.unsavedAskedAt = time.Now()
	return false
}

// askAgainWithin is how soon a quit or restart turned down for a failed save
// has to be asked again to go ahead anyway.
const askAgainWithin = 2 * time.Minute

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

// tabGone is paneGone for a tab, renamed or switched to from a tab bar or a
// dialog drawn before another window closed it.
const tabGone = "that tab is no longer open"

// closedFinishedNotice says what closeFinishedPanes did. It runs with no
// confirmation at all, so closing nothing has to say so as plainly as closing
// several does -- otherwise either looks like the click did nothing.
//
// plural (history.go) is safe to reach for here even though it reads "1
// unit" for a zero count: panes is never zero below the guard above it, and
// tabs is only ever passed while positive.
func closedFinishedNotice(panes, tabs int) string {
	if panes == 0 {
		return "no finished panes to close"
	}
	msg := fmt.Sprintf("closed %s", plural(panes, "finished pane"))
	if tabs > 0 {
		msg += fmt.Sprintf(" and %s", plural(tabs, "empty tab"))
	}
	return msg
}

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

// paneIDFor resolves what a command names: the pane it gives an id for, or,
// when it gives none, the one focused in the tab on screen -- the keyboard's
// way of asking for "whichever pane has focus" without knowing its id.
//
// closePane, restartPane and toggleZoom act on the pane by id, in whichever
// tab it is actually in, rather than through focusFor: that only ever finds a
// pane in the tab already on screen, which refused a helper sitting in any
// other tab as though it had already gone.
func paneIDFor(ws *workspace.Workspace, id string) string {
	if id != "" {
		return id
	}
	if t := ws.CurrentTab(); t != nil {
		return t.Focus
	}
	return ""
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

// activeRoot reads the active project's root on the workspace goroutine.
// ActiveRoot is a plain field read with no lock behind it, so the worktree and
// review commands — which all run on a connection goroutine — cannot call it
// directly without racing whichever command last switched project.
func (s *Server) activeRoot() string {
	root, _ := ask(s, s.ws.ActiveRoot)
	return root
}

package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/team"
)

// This file is the workspace side of the team: the roster of a project's agent
// panes, the messages between them, and telling a pane it has mail.
//
// The messages themselves live in internal/team. What is here is what needs the
// panes: who is in the caller's project, the unread count a pane's header shows,
// and the notice an agent is given in the reply to its next hook.
//
// A pane is never typed into to tell it of mail. Text in an agent's message box
// that the user did not write is a bug whether it is submitted or not, and on
// Windows a line and its Enter that arrive in one read are taken for a paste and
// left there. See HookNotice.

// messagesFile is where the inboxes are kept, next to the other workspace state.
const messagesFile = "messages.json"

// teamState is everything the team keeps on the workspace.
type teamState struct {
	inbox    *team.Store
	notifier *team.Notifier

	// saveMu makes taking a snapshot and writing it one step, so an older
	// snapshot cannot be written after a newer one.
	saveMu sync.Mutex
	// save writes the file; a test replaces it to make a write slow.
	save func(path string, data []byte) error
}

func (w *Workspace) initTeam() {
	t := &w.team
	*t = teamState{
		inbox:    team.NewStore(nil),
		notifier: team.NewNotifier(),
		save:     store.WriteAtomic,
	}
	// A message arriving or being read changes the unread count on a pane's
	// header, so the window is sent the state again.
	t.inbox.OnChange(w.wake)
}

// loadMessages reads the saved inboxes and arranges for every change to be
// saved. A damaged or missing file is not fatal: the inboxes start empty.
func (w *Workspace) loadMessages() {
	dir, err := store.Dir()
	if err != nil {
		return
	}
	path := filepath.Join(dir, messagesFile)
	if data, err := store.ReadState(path); err == nil {
		_ = w.team.inbox.Load(data)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return
	}
	w.team.inbox.OnChange(func() {
		w.wake()
		w.team.saveMu.Lock()
		defer w.team.saveMu.Unlock()
		data, err := w.team.inbox.Snapshot()
		if err != nil {
			return
		}
		// A message that cannot be written down still reaches its pane: the
		// inbox in memory is what `flockdeck inbox` reads.
		_ = w.team.save(path, data)
	})
}

// forgetTeam drops what delivery remembers of a pane that is gone, or whose
// session was replaced. A pane's inbox is kept when it is closed because
// Flockdeck is quitting or its project is being closed: the pane comes back with
// the same id, and what was unread should be there.
func (w *Workspace) forgetTeam(id string) {
	if w.team.inbox == nil {
		return
	}
	w.team.notifier.Forget(id)
}

// dropInbox deletes the inbox of a pane that was closed on purpose, by the user
// or by `flockdeck close`.
func (w *Workspace) dropInbox(id string) {
	if w.team.inbox != nil {
		w.team.inbox.Forget(id)
	}
}

// Roster lists the agent panes of the project the pane self belongs to, the pane
// itself included and marked, in the order the tabs show them. It must run on the
// workspace goroutine.
func (w *Workspace) Roster(self string) ([]team.Member, error) {
	if w.Pane(self) == nil {
		return nil, fmt.Errorf("no pane %q is open here", self)
	}
	return w.rosterOf(w.rootOf(self), self), nil
}

// rosterOf lists the agent panes of the project whose directory is myRoot, in the
// order the tabs show them. The pane self, which may be empty for none, is
// included whatever project it is in and marked. It must run on the workspace
// goroutine.
func (w *Workspace) rosterOf(myRoot, self string) []team.Member {
	myGroup := w.groupOf(myRoot)

	var ids []string
	seen := map[string]bool{}
	for _, t := range w.Tabs {
		for _, id := range t.Tree.Panes() {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	// Panes that belong to no tab are listed after them, in a fixed order.
	w.mu.RLock()
	var loose []string
	for id := range w.panes {
		if !seen[id] {
			loose = append(loose, id)
		}
	}
	w.mu.RUnlock()
	sort.Strings(loose)
	ids = append(ids, loose...)

	var out []team.Member
	for _, id := range ids {
		p := w.Pane(id)
		if p == nil || !p.IsAgent() {
			continue
		}
		if id != self {
			root := w.rootOf(id)
			g := w.groupOf(root)
			if myGroup != nil && g != nil {
				if g.ID != myGroup.ID {
					continue
				}
			} else if !sameDir(root, myRoot) {
				continue
			}
		}
		out = append(out, w.memberOf(p, id == self))
	}
	return out
}

// memberOf describes a pane for the roster.
func (w *Workspace) memberOf(p *Pane, self bool) team.Member {
	w.mu.RLock()
	sess, peer := p.Sess, p.PeerName
	w.mu.RUnlock()
	status := session.StatusExited
	if sess != nil && p.Err == nil {
		status, _ = sess.Status()
	}
	agentID, model := paneAgent(p)
	return team.Member{
		Pane: p.ID, Name: p.Name, Peer: peer, Role: p.Role,
		Agent: agentID, Model: model, Status: status.String(), Self: self,
	}
}

// senderOf says who a message from a pane is from.
func senderOf(m team.Member) team.Sender {
	return team.Sender{Pane: m.Pane, Role: m.Role, Name: m.Label()}
}

// SendMessage puts a message from the pane from into the inbox of every pane
// target names. It must run on the workspace goroutine.
//
// A pane sends as itself: from is checked to be an open pane and is the only
// sender there is. Anyone in the project may message anyone else in it; roles
// limit who may start whom, not who may talk.
func (w *Workspace) SendMessage(from, target, text string) (hooks.MsgResult, error) {
	members, err := w.Roster(from)
	if err != nil {
		return hooks.MsgResult{}, err
	}
	var me team.Member
	for _, m := range members {
		if m.Self {
			me = m
		}
	}
	// Only an agent pane is on the roster. A shell has no name or role to send
	// under, so a message from it would carry an empty sender.
	if me.Pane != from {
		return hooks.MsgResult{}, fmt.Errorf("pane %s is not an agent pane, so it is not part of the team and cannot send messages", from)
	}
	return w.deliver(members, senderOf(me), target, text)
}

// deliver puts a message from sender into the inbox of every member target
// names, and has each pane told it has mail when it can be. Both the panes and
// the window send through it, so the size limit, the full-inbox refusal and the
// delivery rules are the same for both.
func (w *Workspace) deliver(members []team.Member, sender team.Sender, target, text string) (hooks.MsgResult, error) {
	targets, err := team.Resolve(members, target)
	if err != nil {
		return hooks.MsgResult{}, err
	}
	var res hooks.MsgResult
	for _, to := range targets {
		m, err := w.team.inbox.Send(sender, to.Pane, text)
		if err != nil {
			if len(targets) == 1 {
				return hooks.MsgResult{}, err
			}
			res.Failed = append(res.Failed, fmt.Sprintf("%s: %v", to.Label(), err))
			continue
		}
		res.Sent = append(res.Sent, hooks.Sent{ID: m.ID, Pane: to.Pane, To: to.Label(), Notifies: w.notifiable(to.Pane)})
	}
	if len(res.Sent) == 0 {
		return hooks.MsgResult{}, errors.New(strings.Join(res.Failed, "; "))
	}
	return res, nil
}

// Inbox lists the unread messages of the pane id, or all of them.
func (w *Workspace) Inbox(id string, all bool) ([]team.Message, error) {
	if w.Pane(id) == nil {
		return nil, fmt.Errorf("no pane %q is open here", id)
	}
	return w.team.inbox.List(id, all), nil
}

// ReadMessage returns one message of the pane id's inbox and marks it read.
func (w *Workspace) ReadMessage(id string, msg int) (team.Message, error) {
	if w.Pane(id) == nil {
		return team.Message{}, fmt.Errorf("no pane %q is open here", id)
	}
	return w.team.inbox.Read(id, msg)
}

// notifyParent tells the pane that started p what happened to it, as a message
// from Flockdeck. A parent that is no longer open is told nothing.
func (w *Workspace) notifyParent(p *Pane, parent, what string) {
	if parent == "" || w.Pane(parent) == nil {
		return
	}
	label := p.Name
	w.mu.RLock()
	if p.PeerName != "" {
		label = p.PeerName
	}
	w.mu.RUnlock()
	who := "agent"
	if p.Role != "" {
		who = p.Role
	}
	w.team.inbox.SendSystem(parent, fmt.Sprintf("%s %q %s (pane %s)", who, label, what, p.ID))
}

// noteExit tells a pane's parent that the pane's process ended on its own, once
// for each time the pane was started. A process Flockdeck ended itself is a pane
// being closed or restarted, which says nothing.
func (w *Workspace) noteExit(id string) {
	p := w.Pane(id)
	if p == nil {
		return
	}
	w.mu.Lock()
	sess, parent := p.Sess, p.Parent
	if sess == nil || parent == "" || p.exitReported == sess {
		w.mu.Unlock()
		return
	}
	if st, _ := sess.Status(); st != session.StatusExited || sess.Closing() {
		w.mu.Unlock()
		return
	}
	p.exitReported = sess
	w.mu.Unlock()
	what := "exited"
	if why := sess.ExitFailure(); why != "" {
		what += " with " + why
	}
	w.notifyParent(p, parent, what)
}

// noteFinished tells a pane's parent that a close of finished panes closed it,
// unless its exit was already reported.
func (w *Workspace) noteFinished(p *Pane, sess *session.Session, parent string) {
	if w.team.inbox == nil {
		return
	}
	w.mu.Lock()
	done := parent == "" || (sess != nil && p.exitReported == sess)
	if !done {
		p.exitReported = sess
	}
	w.mu.Unlock()
	if !done {
		w.notifyParent(p, parent, "finished")
	}
}

// paneChanged is called whenever a pane's session changes: it is what notices a
// pane that has exited.
func (w *Workspace) paneChanged(id string) {
	if w.team.inbox == nil {
		return
	}
	w.noteExit(id)
}

// Unread is how many unread messages a pane has. The window draws it on the
// pane's header, so a message to an agent that has not been told of it, or has
// not read it, is never out of sight. It is safe from any goroutine.
func (w *Workspace) Unread(id string) int {
	if w.team.inbox == nil {
		return 0
	}
	return len(w.team.inbox.List(id, false))
}

// notifiable says the pane's agent reports its lifecycle through hooks, so a
// message to it is told in the reply to its next prompt or tool result (see
// HookNotice). An agent that does not, such as Codex, Gemini, OpenCode, Aider or
// Cursor Agent, is told nothing: it finds mail when it runs `flockdeck inbox`,
// as its briefing says, and the user sees the unread count on its header.
func (w *Workspace) notifiable(id string) bool {
	p := w.Pane(id)
	if p == nil || !p.IsAgent() {
		return false
	}
	w.mu.RLock()
	agentID := p.Agent
	w.mu.RUnlock()
	if agentID == "" {
		return false
	}
	spec, ok := w.agents().Find(agentID)
	return ok && spec.Caps.Hooks
}

// HookNotice is the notice of messages the pane's agent has not been told of, to
// be given in the reply to one of its hooks (UserPromptSubmit or PostToolUse), or
// "" when there is none. It counts them as told: the agent is told once, and the
// messages stay unread, and counted on the pane's header, until it reads them.
//
// This is the only way an agent is told of mail. It does not wait for the agent
// to be idle. An idle agent has no hook to answer until the user's next prompt,
// and one that is working reads the notice with its next tool result.
//
// It runs on the hook server's goroutine for every tool call, so it compares two
// numbers and does no more unless there is something to say.
func (w *Workspace) HookNotice(id string) string {
	t := &w.team
	if t.inbox == nil || !t.notifier.Pending(id, t.inbox.NewestUnread(id)) {
		return ""
	}
	if !w.notifiable(id) {
		return ""
	}
	return t.notifier.Take(id, t.inbox.List(id, false))
}

// cleanPeerName is a pane's own report of its name, cut down to what can be shown
// on one line: control characters and line breaks become spaces and the length
// is capped. The name goes into other agents' briefings and into the lines
// `flockdeck inbox` prints, so a newline in it would start a line of its own
// there.
func cleanPeerName(name string) string { return team.AgentLabel(name) }

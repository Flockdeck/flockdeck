package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

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

// eventsFile is the team event log, one JSON line for each event, next to the
// inboxes. It never holds message text.
const eventsFile = "team-events.jsonl"

// helperIdleDwell is how long a helper has to stay idle before its parent is
// told its turn is over: a turn that ends and another that starts straight
// after it is not a helper that has finished.
const helperIdleDwell = 5 * time.Second

// teamState is everything the team keeps on the workspace.
type teamState struct {
	inbox    *team.Store
	notifier *team.Notifier
	// events is the team event log, nil until loadMessages has found the
	// directory it is kept in. evMu orders the writes of its file.
	events *team.EventLog
	evMu   sync.Mutex

	// saveMu makes taking a snapshot and writing it one step, so an older
	// snapshot cannot be written after a newer one.
	saveMu sync.Mutex
	// save writes the file; a test replaces it to make a write slow.
	save func(path string, data []byte) error

	// idle reads the status of a helper pane and after runs fn once d has
	// passed; they are what a test replaces to report a helper's turns with a
	// clock and a status of its own.
	idle  func(p *Pane) idleReading
	after func(d time.Duration, fn func())
	// dwell is helperIdleDwell; a test shortens it.
	dwell time.Duration
}

func (w *Workspace) initTeam() {
	t := &w.team
	*t = teamState{
		inbox:    team.NewStore(nil),
		notifier: team.NewNotifier(),
		save:     store.WriteAtomic,
		after:    func(d time.Duration, fn func()) { time.AfterFunc(d, fn) },
		dwell:    helperIdleDwell,
	}
	t.idle = w.readIdle
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
	w.loadEvents(filepath.Join(dir, eventsFile))
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

// loadEvents reads the saved event log, skipping any line that is damaged, and
// arranges for each event the inboxes report to be added and saved.
func (w *Workspace) loadEvents(path string) {
	t := &w.team
	log := &team.EventLog{}
	if data, err := store.ReadState(path); err == nil {
		log.Load(data)
	}
	t.events = log
	t.inbox.OnEvent(func(e team.Event) {
		log.Append(e)
		t.evMu.Lock()
		defer t.evMu.Unlock()
		// A log that cannot be written down is no reason to lose the message.
		_ = t.save(path, log.Encode())
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
	var out []team.Member
	for _, p := range w.projectPanes(myRoot, self, true) {
		out = append(out, w.memberOf(p, p.ID == self))
	}
	return out
}

// projectPanes lists the panes of the project whose directory is myRoot, in the
// order the tabs show them, agents only or shells too. The pane self, which may
// be empty for none, is included whatever project it is in. It must run on the
// workspace goroutine.
func (w *Workspace) projectPanes(myRoot, self string, agentsOnly bool) []*Pane {
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

	var out []*Pane
	for _, id := range ids {
		p := w.Pane(id)
		if p == nil || agentsOnly && !p.IsAgent() {
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
		out = append(out, p)
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
	w.noteIdle(id)
}

// noteIdle tells a pane's parent that the pane has finished a turn and stayed
// idle, once for each turn. A helper normally finishes by going idle at its
// prompt, not by exiting, so without it a manager waiting on its helpers hears
// nothing until the user looks. It writes a message to the parent's inbox and
// types nothing anywhere. It applies to panes whose agent reports its own
// status, since an idle read off a quiet screen could be a dialog waiting for a
// key.
func (w *Workspace) noteIdle(id string) {
	p := w.Pane(id)
	if p == nil {
		return
	}
	w.mu.RLock()
	parent, sess := p.Parent, p.Sess
	w.mu.RUnlock()
	if parent == "" || sess == nil {
		return
	}
	r := w.team.idle(p)
	if !r.Eligible {
		return
	}
	switch r.Status {
	case "working":
		w.mu.Lock()
		p.turnSeen = true
		w.mu.Unlock()
	case "idle":
		w.mu.Lock()
		arm := p.turnSeen && !p.idleArmed
		if arm {
			p.idleArmed = true
		}
		w.mu.Unlock()
		if arm {
			w.armIdleReport(p, parent, sess)
		}
	}
}

// idleReading is what noteIdle needs to know of a pane: whether its status is
// reported by the agent itself, what it is, and for how long it has been it.
type idleReading struct {
	Eligible bool
	Status   string
	IdleFor  time.Duration
}

// readIdle is the reading of a real pane. Eligible is notifiable: the agent has
// hooks and has been heard from in this session.
func (w *Workspace) readIdle(p *Pane) idleReading {
	if !w.notifiable(p.ID) {
		return idleReading{}
	}
	w.mu.RLock()
	sess := p.Sess
	w.mu.RUnlock()
	if sess == nil {
		return idleReading{}
	}
	st, _ := sess.Status()
	return idleReading{Eligible: true, Status: st.String(), IdleFor: time.Since(sess.StatusSince())}
}

// armIdleReport tells the parent after helperIdleDwell if the helper is still idle
// then, in the same session; otherwise it drops the report, and the next time the
// helper goes idle arms it again. A session that is closing is not the same
// session for this: Flockdeck is ending it, in a close or a restart that has not
// yet replaced it, and a turn it was in the middle of is not one that finished.
func (w *Workspace) armIdleReport(p *Pane, parent string, sess *session.Session) {
	w.team.after(w.team.dwell, func() {
		w.mu.RLock()
		same := p.Sess == sess && !sess.Closing()
		w.mu.RUnlock()
		r := w.team.idle(p)
		if !same || w.Pane(p.ID) != p || !r.Eligible || r.Status != "idle" {
			w.mu.Lock()
			p.idleArmed = false
			w.mu.Unlock()
			return
		}
		// Idle, but not for as long as the dwell: it went busy and idle again
		// since this was armed. Look again after another full dwell.
		if r.IdleFor < w.team.dwell {
			w.armIdleReport(p, parent, sess)
			return
		}
		w.mu.Lock()
		p.idleArmed = false
		report := p.turnSeen
		p.turnSeen = false
		w.mu.Unlock()
		if report {
			w.notifyParent(p, parent, "finished its turn and is idle; its report is in `flockdeck inbox` if it sent one, and `flockdeck task list` shows its task")
		}
	})
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

// hooksCapable says the pane is an agent pane whose catalog entry has hooks, so
// it can be told of a message in the reply to its next prompt or tool result
// (see HookNotice). An agent that cannot, such as Codex, Gemini, OpenCode, Aider
// or Cursor Agent, is told nothing: it finds mail when it runs `flockdeck inbox`,
// as its briefing says, and the user sees the unread count on its header.
func (w *Workspace) hooksCapable(id string) bool {
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

// notifiable is hooksCapable and the agent's hooks have been heard from in this
// session. It is what `msg send` promises the sender: a Claude pane whose hooks
// never fire has the capability and not the notice, and is reported as not told.
// A pane that has only just started is reported so too, until its first hook.
func (w *Workspace) notifiable(id string) bool {
	if !w.hooksCapable(id) {
		return false
	}
	p := w.Pane(id)
	w.mu.RLock()
	sess := p.Sess
	w.mu.RUnlock()
	return sess != nil && sess.HooksReported()
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
	// A hook is arriving, which is proof enough that hooks work: notifiable is
	// the stricter test the sender is told, and is not asked here.
	if !w.hooksCapable(id) {
		return ""
	}
	unread := t.inbox.List(id, false)
	notice := t.notifier.Take(id, unread)
	if notice != "" {
		ids := make([]int, len(unread))
		for i, m := range unread {
			ids[i] = m.ID
		}
		t.inbox.MarkNotified(id, ids...)
	}
	return notice
}

// cleanPeerName is a pane's own report of its name, cut down to what can be shown
// on one line: control characters and line breaks become spaces and the length
// is capped. The name goes into other agents' briefings and into the lines
// `flockdeck inbox` prints, so a newline in it would start a line of its own
// there.
func cleanPeerName(name string) string { return team.AgentLabel(name) }

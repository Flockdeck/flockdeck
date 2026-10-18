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
// panes: who is in the caller's project, whether a pane may be typed into right
// now, and the one line that is typed.

// messagesFile is where the inboxes are kept, next to the other workspace state.
const messagesFile = "messages.json"

// helperIdleDwell is how long a helper has to stay idle before its parent is
// told its turn is over: a turn that ends and another that starts straight
// after it is not a helper that has finished.
const helperIdleDwell = 5 * time.Second

// noticeSlack is added to a retry so it lands just after the moment the
// notifier asked for, not just before it.
const noticeSlack = 50 * time.Millisecond

// teamState is everything the team keeps on the workspace. The function fields
// are what a test replaces, so delivery can be checked with a clock and a
// status of its own and no terminal.
type teamState struct {
	inbox    *team.Store
	notifier *team.Notifier

	// now is the clock the notifier is asked with.
	now func() time.Time
	// after runs fn once d has passed and returns what stops it.
	after func(d time.Duration, fn func()) func()
	// state says what the notifier is to know of a pane.
	state func(p *Pane) team.State
	// write types a notice into a pane's terminal and presses Enter.
	write func(p *Pane, notice string)

	// saveMu makes taking a snapshot and writing it one step, so an older
	// snapshot cannot be written after a newer one.
	saveMu sync.Mutex
	// save writes the file; a test replaces it to make a write slow.
	save func(path string, data []byte) error

	mu       sync.Mutex
	timers   map[string]func()
	shutdown bool
}

func (w *Workspace) initTeam() {
	t := &w.team
	*t = teamState{
		inbox:    team.NewStore(nil),
		notifier: team.NewNotifier(),
		now:      time.Now,
		after: func(d time.Duration, fn func()) func() {
			tm := time.AfterFunc(d, fn)
			return func() { tm.Stop() }
		},
		timers: map[string]func(){},
		save:   store.WriteAtomic,
	}
	t.state = w.noticeState
	t.write = w.typeNotice

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

// stopTeam stops the timers. A pane's inbox is kept when it is closed because
// Flockdeck is quitting or its project is being closed: the pane comes back with
// the same id, and what was unread should be there.
func (w *Workspace) stopTeam() {
	t := &w.team
	t.mu.Lock()
	t.shutdown = true
	for id, stop := range t.timers {
		stop()
		delete(t.timers, id)
	}
	t.mu.Unlock()
}

// forgetTeam drops what delivery remembers of a pane that is gone.
func (w *Workspace) forgetTeam(id string) {
	if w.team.inbox == nil {
		return
	}
	t := &w.team
	t.mu.Lock()
	if stop, ok := t.timers[id]; ok {
		stop()
		delete(t.timers, id)
	}
	t.mu.Unlock()
	t.notifier.Forget(id)
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
		res.Sent = append(res.Sent, hooks.Sent{ID: m.ID, Pane: to.Pane, To: to.Label()})
		w.pumpTeam(to.Pane)
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
	w.pumpTeam(parent)
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
// pane has gone idle with mail waiting, and one that has exited.
func (w *Workspace) paneChanged(id string) {
	if w.team.inbox == nil {
		return
	}
	w.pumpTeam(id)
	w.noteExit(id)
	w.noteIdle(id)
}

// noteIdle tells a pane's parent that the pane has finished a turn and stayed
// idle, once for each turn. Without it a manager that has started helpers and
// ended its own turn is never woken: a helper normally finishes by going idle at
// its prompt, not by exiting, so only the user would notice. It applies to panes
// whose agent reports its own status, since an idle read off a quiet screen could
// be a dialog waiting for a key.
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
	st := w.team.state(p)
	if !st.Eligible {
		return
	}
	switch st.Status {
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

// armIdleReport tells the parent after helperIdleDwell if the helper is still idle
// then, in the same session; otherwise it drops the report, and the next time the
// helper goes idle arms it again.
func (w *Workspace) armIdleReport(p *Pane, parent string, sess *session.Session) {
	t := &w.team
	t.mu.Lock()
	down := t.shutdown
	t.mu.Unlock()
	if down {
		return
	}
	t.after(helperIdleDwell, func() {
		w.mu.Lock()
		p.idleArmed = false
		same := p.Sess == sess
		w.mu.Unlock()
		if !same || w.Pane(p.ID) != p || w.team.state(p).Status != "idle" {
			return
		}
		w.mu.Lock()
		report := p.turnSeen
		p.turnSeen = false
		w.mu.Unlock()
		if report {
			w.notifyParent(p, parent, "finished its turn and is idle; read `flockdeck inbox` for its report and `flockdeck task list` for its task")
		}
	})
}

// pumpTeam types the notice for a pane's waiting messages, if the pane can be
// told now, and arranges to look again when it could be later. It is safe to call
// from any goroutine and cheap when there is nothing waiting.
func (w *Workspace) pumpTeam(id string) {
	t := &w.team
	if !t.notifier.Pending(id, t.inbox.NewestUnread(id)) {
		return
	}
	p := w.Pane(id)
	if p == nil {
		return
	}
	notice, retry := t.notifier.Next(t.now(), id, t.state(p), t.inbox.List(id, false))
	if notice != "" {
		t.write(p, notice)
	}
	if retry > 0 {
		w.armNotice(id, retry+noticeSlack)
	}
}

// armNotice looks at a pane again after d, replacing a look already planned.
func (w *Workspace) armNotice(id string, d time.Duration) {
	t := &w.team
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.shutdown {
		return
	}
	if stop, ok := t.timers[id]; ok {
		stop()
	}
	t.timers[id] = t.after(d, func() {
		t.mu.Lock()
		delete(t.timers, id)
		t.mu.Unlock()
		w.pumpTeam(id)
	})
}

// noticeState is what the notifier is told of a real pane.
//
// A pane can be told only if its agent reports its own lifecycle, and that has
// begun: then "idle" is what the agent said, between turns, and not a guess from
// a quiet screen that could be a dialog waiting for a key. A pane whose agent
// reports nothing is never typed into, and relies on its briefing to look at
// `flockdeck inbox`.
func (w *Workspace) noticeState(p *Pane) team.State {
	w.mu.RLock()
	sess, agentID := p.Sess, p.Agent
	w.mu.RUnlock()
	if sess == nil || !p.IsAgent() || p.Err != nil || agentID == "" {
		return team.State{}
	}
	spec, ok := w.agents().Find(agentID)
	if !ok || !spec.Caps.Hooks || !sess.HooksReported() {
		return team.State{}
	}
	st, _ := sess.Status()
	return team.State{
		Eligible: true,
		Status:   st.String(),
		IdleFor:  time.Since(sess.StatusSince()),
		// Half-typed text, or a slash command that may have opened a menu.
		Draft: sess.DraftPossible() || sess.MenuPossible(),
	}
}

// typeNotice types a notice into a pane the way the prompt bar types a prompt,
// and presses Enter, on a goroutine of its own so a pane that has stopped reading
// cannot hold the caller up. See typeInto.
func (w *Workspace) typeNotice(p *Pane, notice string) {
	w.mu.RLock()
	sess := p.Sess
	w.mu.RUnlock()
	if sess == nil {
		return
	}
	go typeInto(sess, notice, true)
}

// cleanPeerName is a pane's own report of its name, cut down to what can be shown
// on one line: control characters and line breaks become spaces and the length
// is capped. The name goes into other agents' briefings and into the lines
// `flockdeck inbox` prints, so a newline in it would start a line of its own
// there.
func cleanPeerName(name string) string { return team.AgentLabel(name) }

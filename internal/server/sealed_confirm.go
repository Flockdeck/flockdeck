package server

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
)

// Tier C of /ws/sealed (docs/remote-parity: stage 6): commit, push and pull from
// a paired device, each one only after a person at the desk says yes to that one
// request.
//
// A tier C command needs everything tier B does (the desk's "Control agents"
// switch for the device's key, checked again for the request), and then it is
// not run. It is held as a deskConfirm: the desk's windows are shown what was
// asked, by which device, for which repository, and the first of them to answer
// settles it. A yes runs that one request, after the device is checked once more
// and with the arguments the device sent, never others; a no, a timeout, the
// device leaving, the right being taken away or a key that changed ends it
// without running anything. Nothing is remembered: the next request is asked
// again. The answer is a command only a window on this machine that opened as a
// page may send (deskConfirmAnswer), bound to the window the question went to by
// a nonce, as the phone-approval dialog is (approvals_gate.go).
//
// This is not proof that a person is looking: a program that holds the window
// token can open a page of its own and answer. What it stops is the relay, the
// phone and a replayed or blind command, none of which can answer.
//
// Merges are not here and stay desk-only.

const (
	// deskConfirmLife is how long a question waits for the desk.
	deskConfirmLife = 2 * time.Minute
	// deskConfirmMax is how many questions may be open at once, all devices.
	deskConfirmMax = 8
	// confirmMaxMessage and confirmMaxFiles bound what a commit request may carry.
	confirmMaxMessage = 4000
	confirmMaxFiles   = 5000
)

// The states of a gitConfirm (what the device is told) and the outcome of a
// deskConfirm (what the desk is told). None carries text from a repository.
const (
	confirmWaiting = "waiting"
	confirmAllowed = "allowed"
	confirmDenied  = "denied"
	confirmExpired = "expired"
	confirmOff     = "off"    // the device may no longer act, or its socket went
	confirmBusy    = "busy"   // another question of this device is open, or too many are
	confirmNoDesk  = "nodesk" // no window at the desk to ask
	confirmBad     = "bad"    // the request was not well formed
)

// Words for the device. Fixed, so a refusal never quotes anything.
const (
	gitDeskOnly   = "Commit, push and pull are asked of the person at the desk and run only for the sealed connection"
	gitNeedsAPath = "A commit, push or pull from a device names the folder"
)

// gitConfirmMsg tells the device where its request stands.
type gitConfirmMsg struct {
	Type      string `json:"type"`
	Req       string `json:"req,omitempty"`
	ID        string `json:"id,omitempty"`
	Cmd       string `json:"cmd"`
	State     string `json:"state"`
	ExpiresMS int    `json:"expiresMs,omitempty"`
}

// deskConfirmMsg is the question put to a window at the desk, and later the news
// that it was settled. State is "ask" or "done"; a window removes the question
// on "done" whoever answered.
type deskConfirmMsg struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	State     string `json:"state"`
	Nonce     string `json:"nonce,omitempty"`
	Action    string `json:"action"`
	Device    string `json:"device,omitempty"`
	Summary   string `json:"summary,omitempty"`
	ExpiresMS int    `json:"expiresMs,omitempty"`
	Outcome   string `json:"outcome,omitempty"`
}

// A deskConfirm is one question waiting for the desk.
type deskConfirm struct {
	id      string
	device  string
	name    string
	key     []byte
	link    *sealedLink
	c       *controlClient
	cmd     command
	action  string
	dir     string
	nonces  map[*controlClient]string
	expires time.Time
	timer   *time.Timer
}

// deskConfirmState holds the questions open now.
type deskConfirmState struct {
	mu sync.Mutex
	by map[string]*deskConfirm
}

// take removes and returns a question, or nil.
func (d *deskConfirmState) take(id string) *deskConfirm {
	d.mu.Lock()
	defer d.mu.Unlock()
	q := d.by[id]
	delete(d.by, id)
	if q != nil && q.timer != nil {
		q.timer.Stop()
	}
	return q
}

// add registers q unless this device already has one open or too many are. A
// device has one question at a time: a second request waits for the first.
func (d *deskConfirmState) add(q *deskConfirm) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.by) >= deskConfirmMax {
		return false
	}
	for _, o := range d.by {
		if o.device == q.device {
			return false
		}
	}
	if d.by == nil {
		d.by = map[string]*deskConfirm{}
	}
	d.by[q.id] = q
	return true
}

// dropLink ends the questions of one socket, which has closed, and returns them.
func (d *deskConfirmState) dropLink(l *sealedLink) []*deskConfirm {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*deskConfirm
	for id, q := range d.by {
		if q.link == l {
			delete(d.by, id)
			if q.timer != nil {
				q.timer.Stop()
			}
			out = append(out, q)
		}
	}
	return out
}

// isGitAction is the commands tier C holds for the desk.
func isGitAction(cmd string) bool {
	return cmd == "commit" || cmd == "gitPush" || cmd == "gitPull"
}

// gitAskedOfDesk is the first thing handleCommand does with commit, push and
// pull. It reports whether it dealt with the command. A window on this machine
// is not held to anything here. A window through the relay that is not the
// sealed socket is refused: the relay can read and forge what it carries, so it
// cannot be what a person's yes at the desk stands behind. On the sealed socket
// the command is put to the desk and not run.
func (s *Server) gitAskedOfDesk(c *controlClient, cmd command) bool {
	if !c.remote {
		return false
	}
	if c.sealed == nil {
		c.notify(gitDeskOnly, true)
		return true
	}
	s.askDeskGit(c, cmd)
	return true
}

func (c *controlClient) confirmState(cmd command, id, state string, life time.Duration) {
	c.sendJSON(gitConfirmMsg{Type: "gitConfirm", Req: clampReq(cmd.Req), ID: id, Cmd: cmd.Cmd, State: state, ExpiresMS: int(life / time.Millisecond)})
}

// confirmAction is the words for what is asked.
func confirmAction(cmd command) string {
	switch {
	case cmd.Cmd == "commit" && cmd.Push:
		return "commit and push"
	case cmd.Cmd == "commit":
		return "commit"
	case cmd.Cmd == "gitPush":
		return "push"
	}
	return "pull"
}

// confirmSummary is what the person at the desk reads. It names the repository,
// the branch and, for a commit, the files and the first line of the message. It is
// built from what the request will do, with control characters taken out; the
// message and file names are the device's own words, shown as they are because
// the person is deciding on them.
func confirmSummary(cmd command, dir string) string {
	var b strings.Builder
	b.WriteString(strings.ToUpper(confirmAction(cmd)[:1]) + confirmAction(cmd)[1:])
	b.WriteString(" in ")
	b.WriteString(tidy(shortName(dir)))
	if br := gitx.CurrentBranch(dir); br != "" {
		b.WriteString(" on branch ")
		b.WriteString(tidy(br))
	}
	if cmd.Cmd == "commit" {
		switch {
		case cmd.Files == nil:
			b.WriteString(". Everything that changed")
		case len(cmd.Files) == 1:
			b.WriteString(". 1 file: " + safeLine(cmd.Files[0], 80))
		default:
			b.WriteString(". " + strconv.Itoa(len(cmd.Files)) + " files, among them " + safeLine(cmd.Files[0], 60))
		}
		b.WriteString(". Message: " + safeLine(cmd.Text, 200))
	}
	return b.String()
}

// askDeskGit holds a commit, push or pull from the device c until the desk
// answers. Everything it refuses is answered with a gitConfirm state; nothing is
// run here.
func (s *Server) askDeskGit(c *controlClient, cmd command) {
	l := c.sealed
	refuse := func(state string) {
		c.confirmState(cmd, "", state, 0)
		s.approvals.auditAsync(auditEvent{Event: "sealed-git", Device: c.device, DeviceName: l.name, Kind: cmd.Cmd, Reason: state})
	}
	if !isGitAction(cmd.Cmd) || cmd.Path == "" || strings.TrimSpace(unquotePath(cmd.Path)) == "" {
		refuse(confirmBad)
		return
	}
	if cmd.Cmd == "commit" {
		if strings.TrimSpace(cmd.Text) == "" || len(cmd.Text) > confirmMaxMessage || len(cmd.Files) > confirmMaxFiles {
			refuse(confirmBad)
			return
		}
	}
	// The folder is known (refuseRemotePaths ran first); it must also be a working
	// tree, so what the desk is shown is a repository.
	dir, err := treeRoot(unquotePath(cmd.Path))
	if err != nil {
		refuse(confirmBad)
		return
	}
	// The right is looked at again: the socket's own check ran before the request,
	// and this is the moment the question is made.
	if !l.mayAct() {
		refuse(confirmOff)
		return
	}
	if !s.sealed.device(c.device).asks.Allow(1) {
		refuse(confirmBusy)
		return
	}
	windows := s.deskPages()
	if len(windows) == 0 {
		refuse(confirmNoDesk)
		return
	}
	id, err := randomHex(8)
	if err != nil {
		refuse(confirmBad)
		return
	}
	cmd.Path = unquotePath(cmd.Path)
	q := &deskConfirm{
		id: id, device: c.device, name: l.name, key: l.key, link: l, c: c, cmd: cmd,
		action: confirmAction(cmd), dir: dir, nonces: map[*controlClient]string{}, expires: time.Now().Add(deskConfirmLife),
	}
	summary := confirmSummary(cmd, dir)
	for _, w := range windows {
		n, err := randomHex(16)
		if err != nil {
			refuse(confirmBad)
			return
		}
		q.nonces[w] = n
	}
	if !s.deskConfirms.add(q) {
		refuse(confirmBusy)
		return
	}
	q.timer = time.AfterFunc(deskConfirmLife, func() { s.settleConfirm(id, confirmExpired) })
	s.approvals.auditAsync(auditEvent{Event: "sealed-git", Device: c.device, DeviceName: l.name, Kind: cmd.Cmd, Reason: "asked"})
	for w, n := range q.nonces {
		w.sendJSON(deskConfirmMsg{Type: "deskConfirm", ID: id, State: "ask", Nonce: n, Action: q.action, Device: tidy(l.name), Summary: summary, ExpiresMS: int(deskConfirmLife / time.Millisecond)})
	}
	c.confirmState(cmd, id, confirmWaiting, deskConfirmLife)
}

// deskPages is the windows on this machine that opened as a page.
func (s *Server) deskPages() []*controlClient {
	var out []*controlClient
	for _, w := range s.clientList() {
		if !w.remote && w.page {
			out = append(out, w)
		}
	}
	return out
}

// settleConfirm ends a question without running it, tells the device and the
// desk, and writes the outcome down. It does nothing for a question already
// settled.
func (s *Server) settleConfirm(id, outcome string) {
	q := s.deskConfirms.take(id)
	if q == nil {
		return
	}
	s.finishConfirm(q, outcome)
}

func (s *Server) finishConfirm(q *deskConfirm, outcome string) {
	q.c.confirmState(q.cmd, q.id, outcome, 0)
	s.approvals.auditAsync(auditEvent{Event: "sealed-git", Device: q.device, DeviceName: q.name, Kind: q.cmd.Cmd, Reason: outcome})
	for w := range q.nonces {
		w.sendJSON(deskConfirmMsg{Type: "deskConfirm", ID: q.id, State: "done", Action: q.action, Outcome: outcome})
	}
}

// answerDeskConfirm is deskConfirmAnswer: a window at the desk says yes (On) or no
// to the question ID, with the nonce it was sent. Only a window on this machine
// that opened as a page may; a nonce that is not the one this window was given is
// ignored and leaves the question open.
func (s *Server) answerDeskConfirm(c *controlClient, cmd command) {
	if c.remote || !c.page {
		c.notify("A request from a paired device is answered in the Flockdeck window on this machine", true)
		return
	}
	s.deskConfirms.mu.Lock()
	q := s.deskConfirms.by[cmd.ID]
	ok := q != nil && q.nonces[c] != "" && q.nonces[c] == cmd.Nonce && time.Now().Before(q.expires)
	s.deskConfirms.mu.Unlock()
	if !ok {
		c.notify("That request has run out", true)
		return
	}
	if s.deskConfirms.take(cmd.ID) != q {
		c.notify("That request has run out", true)
		return
	}
	if !cmd.On {
		s.finishConfirm(q, confirmDenied)
		return
	}
	// Once more, now: the device may have lost the right, changed its key or gone
	// while the question was open. The socket is the one that asked, and it must
	// still be open and be the key the right was given for.
	if !q.link.mayAct() || !q.link.keyIs(q.key) {
		s.finishConfirm(q, confirmOff)
		return
	}
	s.approvals.auditAsync(auditEvent{Event: "sealed-git", Device: q.device, DeviceName: q.name, Kind: q.cmd.Cmd, Reason: confirmAllowed})
	q.c.confirmState(q.cmd, q.id, confirmAllowed, 0)
	for w := range q.nonces {
		w.sendJSON(deskConfirmMsg{Type: "deskConfirm", ID: q.id, State: "done", Action: q.action, Outcome: confirmAllowed})
	}
	s.runGitAction(q.c, q.cmd)
}

// runGitAction runs a confirmed request with the arguments the device sent. A
// git warning about the repository's settings or hooks is still answered at the
// desk only (gitProgramsAccepted refuses it to a remote client), so no answer is
// passed.
func (s *Server) runGitAction(c *controlClient, cmd command) {
	switch cmd.Cmd {
	case "commit":
		s.commitChanges(c, cmd.Path, cmd.Text, cmd.Push, cmd.Files, cmd.Stamps, cmd.Omitted, gitAnswer{})
	case "gitPush":
		s.runRemote(c, "push", cmd.Path, gitAnswer{})
	case "gitPull":
		s.runRemote(c, "pull", cmd.Path, gitAnswer{})
	}
}

// keyIs reports whether the key the link proved is key.
func (l *sealedLink) keyIs(key []byte) bool {
	return len(key) > 0 && string(l.key) == string(key)
}

// dropConfirms is called when a sealed socket closes: its questions go with it.
func (s *Server) dropConfirms(l *sealedLink) {
	for _, q := range s.deskConfirms.dropLink(l) {
		for w := range q.nonces {
			w.sendJSON(deskConfirmMsg{Type: "deskConfirm", ID: q.id, State: "done", Action: q.action, Outcome: confirmOff})
		}
		s.approvals.auditAsync(auditEvent{Event: "sealed-git", Device: q.device, DeviceName: q.name, Kind: q.cmd.Cmd, Reason: confirmOff})
	}
}

package server

import (
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/record"
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
// Stage 8 adds a comment on a pull request or an issue (ghPRComment,
// ghIssueComment) to the three. It is the same flow: the comment goes out under
// the person's GitHub account, so the desk reads what it says and which pull
// request or issue it is for before it runs.
//
// What the desk is shown is what runs. The host reads the repository itself when
// the request arrives (the files a commit would record, the branch, where a push
// would go), stores that, and ignores whatever the device said about files at the
// yes: the device's Files, Stamps and Omitted are never used. The folder, branch
// and remote are pinned at the ask, and a yes is refused ("moved") if the
// folder is no longer a known project, the branch is another, the remote
// changed, or the files a commit would record are no longer the ones shown. A
// message or comment is shown whole; one the desk's redaction would change, or
// that holds a character that cannot be shown as it is, is refused rather than
// posted as it was sent, with a fixed-word reason the device is told.
//
// A push (and a commit with a push) also pins the commit the branch is at and how
// many commits it would send, both shown, and is refused if git's settings
// (push.default, remote.<name>.push, pushRemote) would send more than the current
// branch. A yes that is refused tells the desk that nothing ran. A question left
// to lapse costs the device the base wait and is not counted as a no.
//
// Merges are not here and stay desk-only.

const (
	// deskConfirmLife is how long a question waits for the desk.
	deskConfirmLife = 2 * time.Minute
	// deskConfirmMax is how many questions may be open at once, all devices.
	deskConfirmMax = 8
	// confirmMaxMessage is the most characters of a commit message or a GitHub
	// comment a request may carry: all of it is shown to the person at the desk.
	confirmMaxMessage = 1000
	// confirmMaxFiles is the most changed files a commit request is asked for;
	// confirmShownFiles of them are named to the desk, with a count of the rest.
	confirmMaxFiles   = 5000
	confirmShownFiles = 25
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
	// confirmMoved: between the question and the yes the folder, the branch, the
	// remote or the files of a commit changed, so what was shown is not what would
	// run. Nothing ran; ask again.
	confirmMoved = "moved"
	// confirmWait: the desk said no to this device a moment ago, so no question is put
	// yet. The message carries waitSecs, how long until one is.
	confirmWait = "wait"
)

// Reasons the device is told with a "bad" refusal. Fixed words: none quotes the
// request or the repository.
const (
	reasonNotRequest  = "this is not a request the desk can be asked"
	reasonEmpty       = "the text is empty"
	reasonTooLong     = "the text is too long"
	reasonRedaction   = "the desk's redaction would change the text, so what is shown would not be what is sent"
	reasonControlChar = "the text holds a control or invisible character"
	reasonFolder      = "the folder is not a known project"
	reasonNoFiles     = "there are no changed files to commit"
	reasonTooManyFile = "too many changed files to show"
	reasonReadFiles   = "the changed files could not be read"
	reasonPushShape   = "a plain push here would send more than the current branch"
	reasonPushRead    = "what a push would send could not be read"
	reasonInternal    = "the request could not be set up"
)

// Words for the device. Fixed, so a refusal never quotes anything.
const (
	gitDeskOnly   = "Commit, push and pull are asked of the person at the desk and run only for the sealed connection"
	ghDeskOnly    = "A comment on GitHub is asked of the person at the desk and runs only for the sealed connection"
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
	// Reason is fixed words saying why a "bad" request was refused. WaitSecs is how
	// long until the desk may be asked again, on state "wait".
	Reason   string `json:"reason,omitempty"`
	WaitSecs int    `json:"waitSecs,omitempty"`
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

	// What was shown, read by the host at the ask and held to at the yes.
	branch   string
	remote   string
	reviewed *gitx.Reviewed
	// head is the commit the branch was at, and ahead the commits a push would send
	// (not counting the one a commit makes). A push or a commit with a push holds to
	// both: a commit made by the agent between the ask and the yes would be sent too.
	head   string
	ahead  int
	pushes bool
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
	return cmd == "commit" || cmd == "gitPush" || cmd == "gitPull" || isGHComment(cmd)
}

// isGHComment is a comment on a pull request or an issue.
func isGHComment(cmd string) bool { return cmd == "ghPRComment" || cmd == "ghIssueComment" }

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
		if isGHComment(cmd.Cmd) {
			c.notify(ghDeskOnly, true)
		} else {
			c.notify(gitDeskOnly, true)
		}
		return true
	}
	s.askDeskGit(c, cmd)
	return true
}

func (c *controlClient) confirmState(cmd command, id, state string, life time.Duration) {
	c.sendJSON(gitConfirmMsg{Type: "gitConfirm", Req: clampReq(cmd.Req), ID: id, Cmd: cmd.Cmd, State: state, ExpiresMS: int(life / time.Millisecond)})
}

// confirmRefusal answers a request that was not put to the desk, with the fixed
// words that say why (reason) or how long to wait (wait).
func (c *controlClient) confirmRefusal(cmd command, state, reason string, wait time.Duration) {
	c.sendJSON(gitConfirmMsg{Type: "gitConfirm", Req: clampReq(cmd.Req), Cmd: cmd.Cmd, State: state, Reason: reason, WaitSecs: int((wait + time.Second - 1) / time.Second)})
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
	case cmd.Cmd == "ghPRComment":
		return "comment on a pull request"
	case cmd.Cmd == "ghIssueComment":
		return "comment on an issue"
	}
	return "pull"
}

// shownText is text as the person at the desk is shown it, and whether it can be
// shown faithfully. It cannot when the desk's redaction would change it (the
// request is then refused: what posts must be what was read, not the raw text
// behind a redacted view), when it holds a control or format character that
// would be hidden or reorder what is read, or when it is longer than max.
// A line break is shown as a visible mark so the text is read whole on one line.
func shownText(text string, max int) (string, bool) {
	shown, reason := shownTextWhy(text, max)
	return shown, reason == ""
}

// shownTextWhy is shownText with the reason it cannot be shown, in fixed words
// (reason is "" when it can).
func shownTextWhy(text string, max int) (string, string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	switch {
	case strings.TrimSpace(text) == "":
		return "", reasonEmpty
	case len([]rune(text)) > max:
		return "", reasonTooLong
	case record.Redact(text) != text:
		return "", reasonRedaction
	}
	var b strings.Builder
	for _, r := range text {
		switch {
		case r == '\n':
			b.WriteString(" ↵ ")
		case r == '\t':
			b.WriteByte(' ')
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == ' ' || r == ' ':
			return "", reasonControlChar
		default:
			b.WriteRune(r)
		}
	}
	return b.String(), ""
}

// shownName is a file name as shown: the characters that cannot be shown become
// a space, and a long one is cut. The person is deciding on the list, and a name
// that cannot be shown whole is visibly cut ("...").
func shownName(name string) string {
	return cutAt(normaliseLine(name), 120)
}

// confirmSummary is what the person at the desk reads. It names the full folder,
// the branch, where a push or pull would go and, for a commit, the files the
// host found changed (all of them, up to confirmShownFiles by name and then a
// count) and the whole message; for a comment, the whole comment. Control
// characters are taken out. The text the device sent is shown as it will run;
// shownText has already refused what cannot be.
func confirmSummary(cmd command, dir, branch, remote string, r *gitx.Reviewed, pin gitPin) string {
	var b strings.Builder
	if isGHComment(cmd.Cmd) {
		kind := "pull request"
		if cmd.Cmd == "ghIssueComment" {
			kind = "issue"
		}
		b.WriteString("Comment on " + kind + " #" + strconv.Itoa(cmd.GHNumber) + " in " + tidyFull(dir))
		text, _ := shownText(cmd.GHBody, confirmMaxMessage)
		b.WriteString(". Comment: " + text)
		return b.String()
	}
	b.WriteString(strings.ToUpper(confirmAction(cmd)[:1]) + confirmAction(cmd)[1:])
	b.WriteString(" in ")
	b.WriteString(tidyFull(dir))
	if branch != "" {
		b.WriteString(" on branch ")
		b.WriteString(tidyFull(branch))
	}
	if cmd.Cmd != "commit" || cmd.Push {
		if remote != "" {
			b.WriteString(". Remote: " + tidyFull(remote))
		} else {
			b.WriteString(". No remote found")
		}
	}
	if pin.head != "" {
		b.WriteString(". Now at commit " + pin.head[:min(len(pin.head), 12)])
	}
	if pin.pushes {
		n, more := pin.ahead, ""
		if cmd.Cmd == "commit" {
			n, more = n+1, ", this commit included"
		}
		b.WriteString(". Sends " + strconv.Itoa(n) + " commit")
		if n != 1 {
			b.WriteString("s")
		}
		b.WriteString(more)
	}
	if cmd.Cmd == "commit" && r != nil {
		b.WriteString(". " + strconv.Itoa(len(r.Files)) + " changed file")
		if len(r.Files) != 1 {
			b.WriteString("s")
		}
		b.WriteString(": ")
		for i, f := range r.Files {
			if i == confirmShownFiles {
				b.WriteString(", and " + strconv.Itoa(len(r.Files)-i) + " more")
				break
			}
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(shownName(f))
		}
		// A file in the tail, or one whose name is cut, is not read by the person; one that looks like a
		// secret is counted so that it is not a surprise.
		if n := unseenSecrets(r.Files); n > 0 {
			b.WriteString(". Of the files not named in full above, " + strconv.Itoa(n) + " have names that look like secrets (.env, .pem, .key, id_rsa and similar)")
		}
		text, _ := shownText(cmd.Text, confirmMaxMessage)
		b.WriteString(". Message: " + text)
	}
	return b.String()
}

// gitPin is what the person is shown about the repository's state and what a yes
// is held to: the commit the branch is at, and for a push how many commits it sends.
type gitPin struct {
	head   string
	ahead  int
	pushes bool
}

// shownNameMax is the longest file name shown whole (shownName cuts at it).
const shownNameMax = 120

// unseenSecrets counts the files whose names look like secrets among those the
// desk is not shown whole: the ones past confirmShownFiles, and the ones in the
// first confirmShownFiles whose names are cut.
func unseenSecrets(files []string) int {
	n := 0
	for i, f := range files {
		if (i >= confirmShownFiles || len([]rune(normaliseLine(f))) > shownNameMax) && secretLookingName(f) {
			n++
		}
	}
	return n
}

// secretLookingName reports whether a path's file name is the kind that holds a
// credential: an environment file, a key or certificate, an SSH identity.
func secretLookingName(path string) bool {
	name := strings.ToLower(path)
	if i := strings.LastIndexAny(name, "/\\"); i >= 0 {
		name = name[i+1:]
	}
	switch {
	case name == ".env" || strings.HasPrefix(name, ".env."), strings.HasSuffix(name, ".env"):
		return true
	case strings.HasPrefix(name, "id_rsa"), strings.HasPrefix(name, "id_dsa"), strings.HasPrefix(name, "id_ecdsa"), strings.HasPrefix(name, "id_ed25519"):
		return true
	}
	for _, ext := range []string{".pem", ".key", ".p12", ".pfx", ".jks", ".keystore", ".ppk", ".kdbx"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return name == ".netrc" || name == ".npmrc" || name == ".pgpass" || name == "credentials" || name == "credentials.json"
}

// tidyFull is tidy without its length cut: a path is shown whole.
func tidyFull(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == ' ' || r == ' ' {
			r = ' '
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// askDeskGit holds a commit, push or pull from the device c until the desk
// answers. Everything it refuses is answered with a gitConfirm state; nothing is
// run here.
func (s *Server) askDeskGit(c *controlClient, cmd command) {
	l := c.sealed
	refuseWhy := func(state, reason string, wait time.Duration) {
		c.confirmRefusal(cmd, state, reason, wait)
		s.approvals.auditAsync(auditEvent{Event: "sealed-git", Device: c.device, DeviceName: l.name, Kind: cmd.Cmd, Reason: state})
	}
	refuse := func(state string) { refuseWhy(state, "", 0) }
	bad := func(reason string) { refuseWhy(confirmBad, reason, 0) }
	if !isGitAction(cmd.Cmd) || cmd.Path == "" || strings.TrimSpace(unquotePath(cmd.Path)) == "" {
		bad(reasonNotRequest)
		return
	}
	// The text that will run must be showable as it is (shownText).
	if cmd.Cmd == "commit" {
		if _, why := shownTextWhy(cmd.Text, confirmMaxMessage); why != "" {
			bad(why)
			return
		}
	}
	if isGHComment(cmd.Cmd) {
		if _, why := shownTextWhy(cmd.GHBody, confirmMaxMessage); why != "" {
			bad(why)
			return
		} else if cmd.GHNumber <= 0 {
			bad(reasonNotRequest)
			return
		}
	}
	// The folder is known (refuseRemotePaths ran first); it must also be a working
	// tree, so what the desk is shown is a repository.
	dir, err := treeRoot(unquotePath(cmd.Path))
	if err != nil || !s.remotePathKnown(dir) {
		bad(reasonFolder)
		return
	}
	// The right is looked at again: the socket's own check ran before the request,
	// and this is the moment the question is made.
	if !l.mayAct() {
		refuse(confirmOff)
		return
	}
	dev := s.sealed.device(c.device)
	// A desk that has said no to this device lately is not asked again at once.
	if wait := dev.waitAt(time.Now()); wait > 0 {
		refuseWhy(confirmWait, "", wait)
		return
	}
	if !dev.asks.Allow(1) {
		refuse(confirmBusy)
		return
	}
	windows := s.deskPages()
	if len(windows) == 0 {
		refuse(confirmNoDesk)
		return
	}
	// What would run, read now by the host: the branch, where a push or pull goes
	// and, for a commit, every file it would record. None of it comes from the device.
	branch := gitx.CurrentBranch(dir)
	var remote string
	if cmd.Cmd != "commit" || cmd.Push {
		remote = gitx.PushTarget(dir)
	}
	var reviewed *gitx.Reviewed
	if cmd.Cmd == "commit" {
		r, err := gitx.ReviewSnapshot(dir)
		switch {
		case err != nil:
			bad(reasonReadFiles)
			return
		case len(r.Files) == 0:
			bad(reasonNoFiles)
			return
		case len(r.Files) > confirmMaxFiles:
			bad(reasonTooManyFile)
			return
		}
		reviewed = &r
	}
	// The commit the branch is at, and for a push the commits it would send and
	// whether git's settings would send anything else: all read here, held to at
	// the yes. A commit made by the agent after the ask changes the head.
	// With no remote the push has nowhere to go and fails on its own ("No remote
	// found" is shown); there is nothing to count.
	pin := gitPin{head: gitx.HeadSHA(dir), pushes: remote != "" && (cmd.Cmd == "gitPush" || (cmd.Cmd == "commit" && cmd.Push))}
	if pin.pushes {
		if gitx.PushSettingsRisk(dir) != "" {
			bad(reasonPushShape)
			return
		}
		n, err := gitx.PushAhead(dir)
		if err != nil {
			bad(reasonPushRead)
			return
		}
		pin.ahead = n
	}
	id, err := randomHex(8)
	if err != nil {
		bad(reasonInternal)
		return
	}
	// The folder the request runs in is the one shown, not the device's spelling.
	cmd.Path = dir
	// The device's own account of the files is not used: the host's list is.
	cmd.Files, cmd.Stamps, cmd.Omitted = nil, nil, 0
	q := &deskConfirm{
		id: id, device: c.device, name: l.name, key: l.key, link: l, c: c, cmd: cmd,
		action: confirmAction(cmd), dir: dir, nonces: map[*controlClient]string{}, expires: time.Now().Add(deskConfirmLife),
		branch: branch, remote: remote, reviewed: reviewed, head: pin.head, ahead: pin.ahead, pushes: pin.pushes,
	}
	summary := confirmSummary(cmd, dir, branch, remote, reviewed, pin)
	for _, w := range windows {
		n, err := randomHex(16)
		if err != nil {
			bad(reasonInternal)
			return
		}
		q.nonces[w] = n
	}
	// The timer is set before the question is shared, so nothing reads it while
	// it is written.
	q.timer = time.AfterFunc(deskConfirmLife, func() { s.settleConfirm(id, confirmExpired) })
	if !s.deskConfirms.add(q) {
		q.timer.Stop()
		refuse(confirmBusy)
		return
	}
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
	// A no lengthens the wait before this device may put another, each one doubling
	// it; a question left to lapse costs the base wait and is not counted as a no.
	switch outcome {
	case confirmDenied:
		s.sealed.device(q.device).noteDenied(time.Now())
	case confirmExpired:
		s.sealed.device(q.device).noteLapsed(time.Now())
	}
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
		s.noticeDesk("Nothing ran: "+tidy(q.name)+" no longer has the right to "+q.action+", or its connection has changed since it asked. Your yes was not used", true)
		return
	}
	// What was shown is looked at once more: the folder, the branch, the remote
	// and the files of a commit must still be what the person read.
	if !s.pinHolds(q) {
		s.finishConfirm(q, confirmMoved)
		s.noticeDesk("Nothing ran: the repository changed after "+tidy(q.name)+" asked to "+q.action+" (the folder, branch, remote, last commit or files are not the ones you were shown). Your yes was not used. Ask again if you still want it", true)
		return
	}
	s.sealed.device(q.device).noteAllowed()
	s.approvals.auditAsync(auditEvent{Event: "sealed-git", Device: q.device, DeviceName: q.name, Kind: q.cmd.Cmd, Reason: confirmAllowed})
	q.c.confirmState(q.cmd, q.id, confirmAllowed, 0)
	for w := range q.nonces {
		w.sendJSON(deskConfirmMsg{Type: "deskConfirm", ID: q.id, State: "done", Action: q.action, Outcome: confirmAllowed})
	}
	s.runGitAction(q.c, q)
}

// pinHolds reports whether what the person was shown at the ask is still what
// would run: the folder is a known project, the branch is the same, a push or
// pull goes to the same remote, and the files a commit would record are the
// same, unwritten since.
func (s *Server) pinHolds(q *deskConfirm) bool {
	if !s.remotePathKnown(q.dir) {
		return false
	}
	if gitx.CurrentBranch(q.dir) != q.branch {
		return false
	}
	if (q.cmd.Cmd != "commit" || q.cmd.Push) && gitx.PushTarget(q.dir) != q.remote {
		return false
	}
	if gitx.HeadSHA(q.dir) != q.head {
		return false
	}
	if q.pushes {
		if gitx.PushSettingsRisk(q.dir) != "" {
			return false
		}
		if n, err := gitx.PushAhead(q.dir); err != nil || n != q.ahead {
			return false
		}
	}
	if q.reviewed != nil {
		now, err := gitx.ReviewSnapshot(q.dir)
		if err != nil || !gitx.SameReviewed(*q.reviewed, now) {
			return false
		}
	}
	return true
}

// runGitAction runs a confirmed request in the folder pinned at the ask. A commit
// is made of the files the host listed then (q.reviewed), never of what the device
// sent. A git warning about the repository's settings or hooks is still answered
// at the desk only (gitProgramsAccepted refuses it to a remote client), so no
// answer is passed.
func (s *Server) runGitAction(c *controlClient, q *deskConfirm) {
	cmd := q.cmd
	cmd.Path = q.dir
	switch cmd.Cmd {
	case "commit":
		r := q.reviewed
		s.commitChanges(c, cmd.Path, cmd.Text, cmd.Push, r.Files, r.Stamps, r.Unlisted, gitAnswer{})
	case "gitPush":
		s.runRemote(c, "push", cmd.Path, gitAnswer{})
	case "gitPull":
		s.runRemote(c, "pull", cmd.Path, gitAnswer{})
	case "ghPRComment":
		s.ghPRComment(c, cmd.Path, cmd.GHNumber, cmd.GHBody)
	case "ghIssueComment":
		s.ghIssueComment(c, cmd.Path, cmd.GHNumber, cmd.GHBody)
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

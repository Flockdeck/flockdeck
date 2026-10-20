package workspace

import (
	"errors"
	"strings"

	"github.com/jmwri/flockdeck/internal/session"
)

// MaxPaneSendBytes is the longest text SendToPane takes.
const MaxPaneSendBytes = 16 * 1024

// Why SendToPane refused. Each message is shown to the person who asked.
var (
	ErrSendPaneGone   = errors.New("That pane is no longer open")
	ErrSendExited     = errors.New("That pane's process has exited, so the text was not sent. Restart the pane and send it again")
	ErrSendEmpty      = errors.New("There was no text to send")
	ErrSendTooLong    = errors.New("That text is over 16 KB, so it was not sent")
	ErrSendShell      = errors.New("That pane is a shell, and a shell runs whatever it is sent, so the text was not sent")
	ErrSendPermission = errors.New("That pane is waiting on a permission prompt, which is answered with its choice buttons and not by text, so the text was not sent")
	ErrSendInFlight   = errors.New("The pane is still taking in the last text sent to it, so this was not sent. Try again in a moment")
)

// SendResult says how a SendToPane went.
type SendResult struct {
	// Queued is set when the pane was in the middle of a turn as the text was
	// written, so the agent reads it once that turn is over.
	Queued bool
	// Shell is set when the pane is a plain shell.
	Shell bool
	// Waiting is set when the pane was waiting on a question put to the user
	// (AskUserQuestion), so the text may be read as an answer to it.
	Waiting bool
	// Routed is set when the text was the pane's first prompt and routing
	// restarted the pane on another model to take it. See routeFirstTurn.
	Routed bool
}

// SendToPane types text into the pane with the given id and presses Enter,
// whichever tab the pane is in and whether or not that tab is on screen. It
// writes to that pane alone: unlike SendPromptTo it never goes through the
// broadcast.
//
// The pane is looked up with w.Pane, so it may belong to any project open in
// this workspace, not only the one on screen.
//
// Control characters are stripped before the empty and length checks, as
// typeInto strips them before writing: text that is nothing but an escape or a
// NUL would otherwise pass as text and be written as a bare Enter. The length
// limit is checked on the text as given.
//
// A pane that is waiting on anything but a question is refused, since the
// waiting can be a permission prompt however little it says about itself
// (some agents report waiting with no tool named at all): a permission is answered
// by the user, through its choice buttons, and never by text from another
// agent. A pane waiting on a question (the exact detail AskUserQuestion) is
// sent the text and the result has Waiting set. Idle and blocked panes are
// sent it. The status is read before the write happens, so a prompt that
// opens in between is not caught.
//
// The text is written as a prompt. In a pane whose program has not asked for
// bracketed paste, each line break types an Enter and so submits what came
// before it. In an agent pane a line starting with "!" or "/" is read by the
// agent as a shell or slash command. allowShell guards shell panes only and
// does nothing about either.
//
// A pane that is a shell is refused unless allowShell is set. The write
// happens on a goroutine of its own, as in SendPrompt, and only one is in
// flight for a pane at a time: a pane whose program has stopped reading would
// otherwise gain a blocked goroutine for every send.
//
// A submitted prompt is offered to routeFirstTurn as SendPromptTo does, so a
// pane opened by hand and sent its first prompt this way is treated the same
// as from the prompt bar. Like SendPrompt it runs on the goroutine that owns
// the workspace.
func (w *Workspace) SendToPane(id, text string, allowShell bool) (SendResult, error) {
	var res SendResult
	p := w.Pane(id)
	if p == nil {
		return res, ErrSendPaneGone
	}
	if !p.Alive() {
		return res, ErrSendExited
	}
	if len(text) > MaxPaneSendBytes {
		return res, ErrSendTooLong
	}
	text = stripControls(text)
	if strings.TrimSpace(text) == "" {
		return res, ErrSendEmpty
	}
	res.Shell = !p.IsAgent()
	if res.Shell && !allowShell {
		return res, ErrSendShell
	}
	if st, detail := p.Status(); st == session.StatusWaiting {
		if detail != "AskUserQuestion" {
			return res, ErrSendPermission
		}
		res.Waiting = true
	}
	if !w.claimSend(p) {
		return res, ErrSendInFlight
	}
	res.Queued = w.Runtime(p).Activity() == ActivityWorking
	if w.routeFirstTurn(p, text) {
		w.releaseSend(p)
		res.Routed, res.Queued = true, false
		return res, nil
	}
	if p.Structured() {
		// A prompt over the protocol, queued behind a running turn; no keys are
		// typed, because there is no terminal.
		err := w.Runtime(p).Wake(text)
		w.releaseSend(p)
		return res, err
	}
	sess := p.Sess
	go w.writeToPane(p, sess, text, typeIntoAsFlockdeck)
	return res, nil
}

// typeIntoAsFlockdeck is what SendToPane types with. The text is written on
// the graph's behalf, so it moves the pane's Flockdeck input time and not the
// user's: see session.LastUserInput.
func typeIntoAsFlockdeck(s *session.Session, text string, submit bool) {
	typeIntoFrom(s, session.InputFlockdeck, text, submit)
}

// writeToPane types text into sess with write and then lets the next send for
// p start, whether or not write returns normally. write is a parameter so a
// test can have it fail without sharing a variable with other sends. Nothing
// recovers a panic in write: it would take the process down, as one in
// typeInto does for SendPrompt, and that is by design.
func (w *Workspace) writeToPane(p *Pane, sess *session.Session, text string, write func(*session.Session, string, bool)) {
	defer w.releaseSend(p)
	write(sess, text, true)
}

// claimSend marks a write into p as in flight, and says false when one
// already is.
func (w *Workspace) claimSend(p *Pane) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if p.sending {
		return false
	}
	p.sending = true
	return true
}

func (w *Workspace) releaseSend(p *Pane) {
	w.mu.Lock()
	p.sending = false
	w.mu.Unlock()
}

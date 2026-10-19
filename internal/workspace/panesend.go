package workspace

import (
	"errors"
	"strings"
)

// MaxPaneSendBytes is the longest text SendToPane takes.
const MaxPaneSendBytes = 16 * 1024

// Why SendToPane refused. Each message is shown to the person who asked.
var (
	ErrSendPaneGone = errors.New("That pane is no longer open")
	ErrSendExited   = errors.New("That pane's process has exited, so the text was not sent. Restart the pane and send it again")
	ErrSendEmpty    = errors.New("There was no text to send")
	ErrSendTooLong  = errors.New("That text is over 16 KB, so it was not sent")
	ErrSendShell    = errors.New("That pane is a shell, and a shell runs whatever it is sent, so the text was not sent")
	ErrSendInFlight = errors.New("The pane is still taking in the last text sent to it, so this was not sent. Try again in a moment")
)

// SendResult says how a SendToPane went.
type SendResult struct {
	// Queued is set when the pane was in the middle of a turn as the text was
	// written, so the agent reads it once that turn is over.
	Queued bool
	// Shell is set when the pane is a plain shell.
	Shell bool
	// Routed is set when the text was the pane's first prompt and routing
	// restarted the pane on another model to take it. See routeFirstTurn.
	Routed bool
}

// SendToPane types text into the pane with the given id and presses Enter,
// whichever tab the pane is in and whether or not that tab is on screen. It
// writes to that pane alone: unlike SendPromptTo it never goes through the
// broadcast.
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
	if strings.TrimSpace(text) == "" {
		return res, ErrSendEmpty
	}
	if len(text) > MaxPaneSendBytes {
		return res, ErrSendTooLong
	}
	res.Shell = !p.IsAgent()
	if res.Shell && !allowShell {
		return res, ErrSendShell
	}
	if !w.claimSend(p) {
		return res, ErrSendInFlight
	}
	res.Queued = PaneActivity(p) == ActivityWorking
	if w.routeFirstTurn(p, text) {
		w.releaseSend(p)
		res.Routed, res.Queued = true, false
		return res, nil
	}
	sess := p.Sess
	go func() {
		defer w.releaseSend(p)
		typeInto(sess, text, true)
	}()
	return res, nil
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

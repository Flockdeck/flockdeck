package workspace

import (
	"github.com/jmwri/flockdeck/internal/team"
)

// Mail in the conversation of structured panes. A message a pane sends is
// shown in its conversation as a sent message, and the mail a pane is prompted
// with is shown by its own runtime when the turn begins (see
// structured.Runtime.SendMail). Both then follow the message's delivery state,
// which the inbox reports as events. The entries are made from the inbox's own
// record of the message and cleaned to one line of plain text, the way the
// prompt the agent is sent is; none of them is a prompt of the person's.

// showMailEvent updates the conversations for one event of the inbox.
func (w *Workspace) showMailEvent(e team.Event) {
	switch e.Kind {
	case team.EventMessage:
		w.showSent(e)
	case team.EventDelivery:
		w.showMailState(e)
	}
}

// showSent shows a message in the conversation of the structured pane that sent
// it. A message from the person, from Flockdeck, or from a terminal pane has
// no conversation to show it in.
func (w *Workspace) showSent(e team.Event) {
	if e.From == "" || w.team.inbox == nil {
		return
	}
	sr, ok := w.structuredRuntimeOf(e.From)
	if !ok {
		return
	}
	var msg team.Message
	found := false
	for _, m := range w.team.inbox.Messages([]string{e.To}) {
		if m.ID == e.ID {
			msg, found = m, true
			break
		}
	}
	if !found {
		return
	}
	sr.rt.NoteSent(msg.ID, w.mailRecipientLabel(e.To), promptBody(msg), msg.Delivery())
}

// mailRecipientLabel is what the sender's conversation calls the pane to.
func (w *Workspace) mailRecipientLabel(to string) string {
	p := w.Pane(to)
	if p == nil {
		return "another agent"
	}
	roles := w.labelRoles()
	label := w.memberOf(p, false, roles).Label(roles...)
	if label == "" {
		return "another agent"
	}
	return label
}

// showMailState moves the entries of the message in the conversations that hold
// it. The inbox's event names the message and not the panes, and ids are unique
// across the inbox, so every structured pane is asked and only the ones that
// show the message change.
func (w *Workspace) showMailState(e team.Event) {
	if !mailStates[e.State] {
		return
	}
	w.mu.RLock()
	panes := make([]*Pane, 0, len(w.panes))
	for _, p := range w.panes {
		panes = append(panes, p)
	}
	w.mu.RUnlock()
	for _, p := range panes {
		if sr, ok := p.setRuntime().(*structuredRuntime); ok {
			sr.rt.NoteMailState(e.ID, e.State)
		}
	}
}

// mailStates are the delivery states of mail on the prompt route.
var mailStates = map[string]bool{
	team.DeliveryQueued: true, team.DeliveryDelivered: true,
	team.DeliveryHandled: true, team.DeliveryUndeliverable: true,
}

package workspace

import (
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/team"
)

// tapSendMessage records a SendMessage call a Claude session made, from the hook
// event that reported it finished. Only a finished call is taken (PostToolUse):
// one that was refused or failed sent nothing.
//
// A call the parser reads is kept with its recipient resolved to a pane through
// the peer names of the sender's project. One it does not read is kept as a tool
// name and a sender only. Neither keeps any text of the message.
func (w *Workspace) tapSendMessage(from string, ev hooks.Event) {
	if ev.Event != "PostToolUse" || !hooks.IsSendMessage(ev.Tool) {
		return
	}
	tap := team.Tap{Time: time.Now(), From: from, Tool: team.CleanTool(ev.Tool)}
	if ev.Detail != nil {
		if name, ok := hooks.SendMessageRecipient(ev.Detail.Input); ok {
			tap.Understood = true
			tap.To = w.paneByPeerName(from, name)
		}
	}
	w.team.taps.Add(tap)
	// The event log gets the same record: who, to whom, when and the tool name.
	if add := w.team.addEvent; add != nil {
		state := team.TapUnread
		if tap.Understood {
			state = team.TapSent
		}
		add(team.Event{Kind: team.EventTap, Time: tap.Time, From: tap.From, To: tap.To, State: state, Tool: tap.Tool})
	}
	w.wake()
}

// paneByPeerName is the pane in the project of pane from whose peer name is
// name, or "" when no pane or more than one has it. A pane is never its own
// recipient.
func (w *Workspace) paneByPeerName(from, name string) string {
	name = cleanPeerName(name)
	if name == "" {
		return ""
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	src := w.panes[from]
	if src == nil {
		return ""
	}
	found := ""
	for id, p := range w.panes {
		if id == from || p.Root != src.Root || p.PeerName == "" || !strings.EqualFold(p.PeerName, name) {
			continue
		}
		if found != "" {
			return ""
		}
		found = id
	}
	return found
}

// Taps returns the SendMessage calls seen, oldest first.
func (w *Workspace) Taps() []team.Tap { return w.team.taps.Taps() }

// TapRev changes whenever a call is seen.
func (w *Workspace) TapRev() int { return w.team.taps.Rev() }

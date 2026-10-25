package server

// paneSendMsg is the answer to a paneSend that was accepted. Queued says the
// pane was in the middle of a turn, so the text waits behind it; Shell says
// the pane is a plain shell.
type paneSendMsg struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Queued bool   `json:"queued"`
	Shell  bool   `json:"shell"`
	// Waiting says the pane was waiting on a question put to the user.
	Waiting bool `json:"waiting,omitempty"`
	Routed  bool `json:"routed,omitempty"`
}

// paneSend types cmd.Text into the pane named by cmd.ID and submits it, for
// any pane open in the workspace. The pane need not be in the tab on screen,
// and the text goes to that pane alone whatever the broadcast is set to.
// cmd.AllowShell is the caller's say-so for a shell: without it a shell pane is refused.
// A refusal comes back as an error notice, an accepted send as a paneSend
// message.
//
// A window reached through the relay is refused for now. Decision: whether and
// how a remote device may send text into a pane is not decided (the phone
// scope), so the desk is the only place it is sent from; this refusal is the
// place to lift when it is.
// Plan v8 locks this: structured (ACP) panes stay desktop-only until a sealed
// team socket exists; a terminal pane can be approved from a phone only through
// the drawer's choice buttons on the sealed terminal path; merges are desk-only.
func (s *Server) paneSend(c *controlClient, cmd command) {
	if c.remote {
		c.notify(paneSendDeskOnly, true)
		return
	}
	s.do(func() {
		res, err := s.ws.SendToPaneThen(cmd.ID, cmd.Text, cmd.AllowShell, func(err error) { c.notify(err.Error(), true) })
		if err != nil {
			c.notify(err.Error(), true)
			return
		}
		c.sendJSON(paneSendMsg{Type: "paneSend", ID: cmd.ID, Queued: res.Queued, Shell: res.Shell, Waiting: res.Waiting, Routed: res.Routed})
	})
}

// paneSendDeskOnly is what a window reached through the relay is told.
const paneSendDeskOnly = "Sending text into a pane is done on the machine Flockdeck runs on, not from a window reached through the relay"

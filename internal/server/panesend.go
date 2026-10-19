package server

// paneSendMsg is the answer to a paneSend that was accepted. Queued says the
// pane was in the middle of a turn, so the text waits behind it; Shell says
// the pane is a plain shell.
type paneSendMsg struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Queued bool   `json:"queued"`
	Shell  bool   `json:"shell"`
	Routed bool   `json:"routed,omitempty"`
}

// paneSend types cmd.Text into the pane named by cmd.ID and submits it, for
// any pane open in the workspace. The pane need not be in the tab on screen,
// and the text goes to that pane alone whatever the broadcast is set to.
// cmd.AllowShell is the caller's say-so for a shell: without it a shell pane is refused.
// A refusal comes back as an error notice, an accepted send as a paneSend
// message.
func (s *Server) paneSend(c *controlClient, cmd command) {
	s.do(func() {
		res, err := s.ws.SendToPane(cmd.ID, cmd.Text, cmd.AllowShell)
		if err != nil {
			c.notify(err.Error(), true)
			return
		}
		c.sendJSON(paneSendMsg{Type: "paneSend", ID: cmd.ID, Queued: res.Queued, Shell: res.Shell, Routed: res.Routed})
	})
}

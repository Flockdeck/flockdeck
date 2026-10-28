package server

import (
	"errors"

	"github.com/jmwri/flockdeck/internal/acp/agentlogin"
	"github.com/jmwri/flockdeck/internal/structured"
)

// Signing in from a structured pane (API 1.24, cap structuredSignIn). A pane
// that waits for a login and whose adapter can run the agent's own login
// command carries a signIn block in its state; structuredSignIn starts the
// command and structuredSignInCancel ends it. When the command ends well the
// pane is restarted, which starts the agent again with the login it now has.
//
// Desk only, like every structured command: a window reached through the relay
// is refused in structuredCommand, a viewer by the table of commands. The
// block holds a state and one address, and the address passed the check in
// agentlogin twice. Nothing the agent or its login command wrote is in it, and
// the failure texts are ours.
const (
	structuredSignInNone = "That pane cannot be signed in from Flockdeck"
	structuredSignInBusy = "A sign-in is already running in that pane"
	structuredSignInEnd  = "That pane has no sign-in running"
)

// structuredSignInBlock is the signIn member of a pane's state. Available says
// the pane waits for a login that Flockdeck can run. State is empty before one
// is started, then starting, waiting (URL is the page to open), done or failed.
type structuredSignInBlock struct {
	Available bool   `json:"available"`
	State     string `json:"state,omitempty"`
	URL       string `json:"url,omitempty"`
}

func buildStructuredSignIn(m *structuredMsg, sn structured.Snapshot) {
	if !sn.CanSignIn && sn.SignIn == nil {
		return
	}
	b := &structuredSignInBlock{Available: sn.CanSignIn}
	if si := sn.SignIn; si != nil {
		b.State = si.State
		if u, ok := agentlogin.CleanShape(si.URL); ok {
			b.URL = u
		}
	}
	m.SignIn = b
}

// structuredSignIn starts the sign-in of the pane cmd.ID. It must run on the
// workspace goroutine.
func (s *Server) structuredSignIn(c *controlClient, cmd command) {
	id := cmd.ID
	rt := s.ws.StructuredRuntime(id)
	if rt == nil {
		refuse(c, cmd, errStructured(structuredNotPane))
		return
	}
	err := rt.SignIn(func() {
		// The same runtime still holds the pane: it may have been closed or
		// restarted by the person while the page was open.
		s.do(func() {
			if s.ws.StructuredRuntime(id) == rt {
				s.ws.RestartPaneByID(id)
			}
		})
	})
	switch {
	case err == nil:
		c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, OK: true})
	case errors.Is(err, structured.ErrSignInBusy):
		refuse(c, cmd, errStructured(structuredSignInBusy))
	default:
		refuse(c, cmd, errStructured(structuredSignInNone))
	}
}

// structuredSignInCancel ends the sign-in of the pane cmd.ID and waits until
// its command is gone. It must not run on the workspace goroutine.
func (s *Server) structuredSignInCancel(c *controlClient, cmd command) {
	rt := s.ws.StructuredRuntime(cmd.ID)
	if rt == nil {
		refuse(c, cmd, errStructured(structuredNotPane))
		return
	}
	go func() {
		if err := rt.SignInCancel(); err != nil {
			refuse(c, cmd, errStructured(structuredSignInEnd))
			return
		}
		c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, OK: true})
	}()
}

package server

import (
	"errors"
	"strings"
	"unicode"

	"github.com/jmwri/flockdeck/internal/shellrun"
	"github.com/jmwri/flockdeck/internal/structured"
	"github.com/jmwri/flockdeck/internal/team"
)

// The person's own commands in a structured pane, typed after "!" in the prompt
// box. They run in the pane's directory in a shell Flockdeck owns, with a
// scrubbed environment, and never go through the agent or its permission
// requests. The push lists them (`shell`) with their output as plain text, and
// four commands run, stop, remove and send one. Like the other structured
// commands they are refused to a window reached through the relay, and to a
// viewer, by the table in viewerrole.go.

const (
	// Limits on what a push says of one run.
	structuredShellCmdCut  = 500
	structuredShellTextCut = 8 * 1024

	// The refusals. None carries text from a program or an agent.
	structuredShellEmpty   = "Type a command after the !"
	structuredShellOneLine = "A command is one line, with no control characters"
	structuredShellLong    = "That command is too long"
	structuredShellBusy    = "Too many commands are running. Stop one first"
	structuredShellNoRun   = "That command is no longer listed"
	structuredShellStill   = "Stop that command before removing it"
	structuredShellStart   = "The command could not be started"
	structuredShellClosed  = "That pane is closed"
	structuredShellNoItem  = "The shell commands need the id of a command"
	structuredShellQueue   = "The agent is not running, so it cannot be sent the output"
)

// structuredShellItem is one of the person's commands in a structured push.
// State is running, exited, stopped, timeout or failed. Code is the exit code and
// is present for exited. Text is the output as plain text: escape sequences,
// control characters and format characters are removed, and it is cut to its newest
// 8 KiB, with Cut set when the start of it was let go.
type structuredShellItem struct {
	ID    int    `json:"id"`
	Cmd   string `json:"cmd"`
	State string `json:"state"`
	Code  *int   `json:"code,omitempty"`
	Text  string `json:"text"`
	Cut   bool   `json:"cut,omitempty"`
}

// buildStructuredShell adds the person's commands, the oldest first.
func buildStructuredShell(m *structuredMsg, sn structured.Snapshot) {
	for _, r := range sn.Shell {
		it := structuredShellItem{ID: r.ID, State: string(r.State)}
		it.Cmd = stripFormat(team.CleanLine(r.Cmd, structuredShellCmdCut))
		text, cut := shellrun.Clip(shellrun.Text(r.Output), structuredShellTextCut, true)
		it.Text, it.Cut = text, cut || r.Dropped > 0
		if r.State == shellrun.Exited {
			code := r.Code
			it.Code = &code
		}
		m.Shell = append(m.Shell, it)
	}
}

// shellLine says whether s is a command fit to run: one line, with no control
// character at all.
func shellLine(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) && r != '\t' {
			return false
		}
	}
	return !strings.ContainsAny(s, "  ")
}

// structuredShell runs structuredShellRun, structuredShellStop,
// structuredShellRemove and structuredShellSend on the pane cmd.ID. Run takes the
// command in cmd.Text; the others name a run by cmd.Item. The answer is a
// teamResult for the op. It must run on the workspace goroutine.
func (s *Server) structuredShell(c *controlClient, cmd command) {
	rt := s.ws.StructuredRuntime(cmd.ID)
	if rt == nil {
		refuse(c, cmd, errStructured(structuredNotPane))
		return
	}
	var err error
	switch cmd.Cmd {
	case "structuredShellRun":
		text := strings.TrimSpace(cmd.Text)
		switch {
		case text == "":
			err = errStructured(structuredShellEmpty)
		case !shellLine(text):
			err = errStructured(structuredShellOneLine)
		default:
			_, err = rt.RunShell(text)
		}
	case "structuredShellStop", "structuredShellRemove", "structuredShellSend":
		if cmd.Item <= 0 {
			err = errStructured(structuredShellNoItem)
			break
		}
		switch cmd.Cmd {
		case "structuredShellStop":
			err = rt.StopShell(cmd.Item)
		case "structuredShellRemove":
			err = rt.RemoveShell(cmd.Item)
		default:
			err = rt.SendShellOutput(cmd.Item)
		}
	}
	switch {
	case err == nil:
		c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, OK: true})
	case errors.As(err, new(errStructured)):
		refuse(c, cmd, err)
	case errors.Is(err, shellrun.ErrEmpty):
		refuse(c, cmd, errStructured(structuredShellEmpty))
	case errors.Is(err, shellrun.ErrTooLong):
		refuse(c, cmd, errStructured(structuredShellLong))
	case errors.Is(err, shellrun.ErrBusy):
		refuse(c, cmd, errStructured(structuredShellBusy))
	case errors.Is(err, shellrun.ErrNoRun):
		refuse(c, cmd, errStructured(structuredShellNoRun))
	case errors.Is(err, shellrun.ErrRunning):
		refuse(c, cmd, errStructured(structuredShellStill))
	case errors.Is(err, shellrun.ErrNotStart):
		refuse(c, cmd, errStructured(structuredShellStart))
	case errors.Is(err, structured.ErrShellClosed):
		refuse(c, cmd, errStructured(structuredShellClosed))
	default:
		// Only Send reaches here: the agent's queue refused the prompt.
		refuse(c, cmd, errStructured(structuredShellQueue))
	}
}

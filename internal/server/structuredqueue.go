package server

import (
	"errors"
	"strings"
	"unicode"

	"github.com/jmwri/flockdeck/internal/structured"
	"github.com/jmwri/flockdeck/internal/team"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// The queue of a structured pane: the prompts waiting behind the running turn,
// listed in the pane's push, and four commands that change it. A prompt is named
// by the id the push gave it and never by its place in the list, since the list
// shortens as turns end. A command about a prompt that has been sent since the
// window drew it is refused with fixed text and changes nothing.

const (
	// Limits on what a push says of one queued prompt.
	structuredQueueTextCut = structured.QueueTextCap
	structuredQueuePeerCut = 100

	// The refusals of the queue commands. None carries text from an agent.
	structuredQueueGone    = "That prompt has already been sent or removed"
	structuredQueueMail    = "A message from another agent cannot be edited or removed here"
	structuredQueueSystem  = "A prompt from Flockdeck cannot be edited or removed here"
	structuredQueueLong    = "That prompt is too long to edit here"
	structuredQueueRunning = "That pane's agent is not running"
	structuredQueueNoItem  = "The queue commands need the id of a waiting prompt"
)

// structuredQueueItem is one waiting prompt in a structured push. Kind is "user"
// for text the person typed, "mail" for a message from another agent and
// "flockdeck" for a prompt Flockdeck sent itself (the pane's opening prompt).
// Peer is the other agent's label and is set for mail only, and a message's text
// never carries it. The text and the label of mail have their format and
// direction characters removed, since another agent wrote them; the person's own
// text is sent as it is. State is "queued" for every item: a prompt that is sent leaves the list.
// Clipped says Text is the start of a longer text, and then the prompt can be
// removed but not edited.
type structuredQueueItem struct {
	ID      int    `json:"id"`
	Pos     int    `json:"pos"`
	Kind    string `json:"kind"`
	State   string `json:"state"`
	Peer    string `json:"peer,omitempty"`
	Text    string `json:"text"`
	Clipped bool   `json:"clipped,omitempty"`
}

// buildStructuredQueue adds the waiting prompts, in the order they will be sent.
func buildStructuredQueue(m *structuredMsg, sn structured.Snapshot) {
	for i, q := range sn.Queue {
		it := structuredQueueItem{ID: q.ID, Pos: i + 1, Kind: "user", State: "queued"}
		text := q.Text
		if q.Mail {
			text = stripFormat(text)
		}
		var cut bool
		it.Text, cut = clipStructured(text, structuredQueueTextCut)
		it.Clipped = q.Clipped || cut
		switch {
		case q.Mail:
			it.Kind = "mail"
			it.Peer = stripFormat(team.CleanLine(q.Peer, structuredQueuePeerCut))
		case q.System:
			it.Kind = "flockdeck"
		}
		m.Queue = append(m.Queue, it)
	}
}

// stripFormat removes the format characters (Unicode Cf: the bidirectional
// overrides and isolates, zero-width joiners, the byte order mark) from text
// another agent wrote, so it cannot reorder or hide what is around it.
func stripFormat(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
}

// structuredQueue runs structuredQueueEdit, structuredQueueRemove,
// structuredQueueClear and structuredQueueSendNow on the pane cmd.ID. Edit takes
// the new text in cmd.Text. Clear removes the person's waiting prompts and keeps
// the messages from other agents. SendNow ends the running turn and sends that
// prompt next, with the rest of the queue behind it. The answer is a teamResult
// for the op. It must run on the workspace goroutine.
func (s *Server) structuredQueue(c *controlClient, cmd command) {
	rt := s.ws.StructuredRuntime(cmd.ID)
	if rt == nil {
		refuse(c, cmd, errStructured(structuredNotPane))
		return
	}
	var err error
	switch cmd.Cmd {
	case "structuredQueueClear":
		rt.ClearQueued()
	case "structuredQueueEdit":
		if cmd.Item <= 0 {
			err = errStructured(structuredQueueNoItem)
			break
		}
		var text string
		if text, err = workspace.QueuedPromptText(cmd.Text); err == nil {
			err = rt.EditQueued(cmd.Item, text)
		}
	case "structuredQueueRemove":
		if cmd.Item <= 0 {
			err = errStructured(structuredQueueNoItem)
			break
		}
		err = rt.RemoveQueued(cmd.Item)
	case "structuredQueueSendNow":
		if cmd.Item <= 0 {
			err = errStructured(structuredQueueNoItem)
			break
		}
		err = rt.SendNow(cmd.Item)
	}
	switch {
	case err == nil:
		c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, OK: true})
	case errors.Is(err, structured.ErrNoItem):
		refuse(c, cmd, errStructured(structuredQueueGone))
	case errors.Is(err, structured.ErrMailItem):
		refuse(c, cmd, errStructured(structuredQueueMail))
	case errors.Is(err, structured.ErrSystemItem):
		refuse(c, cmd, errStructured(structuredQueueSystem))
	case errors.Is(err, structured.ErrClipped):
		refuse(c, cmd, errStructured(structuredQueueLong))
	case errors.Is(err, workspace.ErrSendEmpty), errors.Is(err, workspace.ErrSendTooLong), errors.As(err, new(errStructured)):
		refuse(c, cmd, err)
	default:
		refuse(c, cmd, errStructured(structuredQueueRunning))
	}
}

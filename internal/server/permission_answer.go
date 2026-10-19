package server

import (
	"errors"
	"fmt"
	"os"

	"github.com/jmwri/flockdeck/internal/acp/bridge"
)

// permissionAnswer is the command a person's choice on a structured agent's
// permission card comes in on: {cmd: "permissionAnswer", id: pane, requestId,
// optionId}.
//
// It answers with the id of an option on the card the pane is showing now. The
// id is taken from the request and checked against that card; there is no fixed
// answer and no answer by kind. "Allow always" is given only by a person
// pressing the button for that option, which sends that option's id.
//
// A window on this machine may send it. A window reached through the relay may
// not, whether the device is full or a viewer (the viewer is refused by
// commandAccess, the full device by the command itself): the structured panes
// stay on the desk until a sealed team socket exists. Like paneSend, this is the
// one place to lift when it does.

// permissionAnswerDeskOnly is what a window reached through the relay is told.
const permissionAnswerDeskOnly = "A structured agent's permission request is answered on the machine Flockdeck runs on, not from a window reached through the relay"

// The refusals. Each says what is wrong and is the whole of what a window is told.
const (
	permissionNotStructured = "That pane is not a structured agent pane"
	permissionNothingAsked  = "That pane is not waiting for an answer"
	permissionStale         = "That request has already been answered or has ended"
	permissionUnknownOption = "That is not one of the options offered"
	permissionBadRequest    = "The answer needs a pane, a request id and an option id"
)

// ErrPermissionStale is what a StructuredPane's AnswerPermission returns when
// the request is no longer the one waiting: it was answered, cancelled or
// replaced between the check and the answer.
var ErrPermissionStale = errors.New("the permission request is no longer waiting")

// StructuredPane is what permissionAnswer needs of a pane an agent drives over
// ACP. The workspace adapts its pane to it; tests use a fake.
type StructuredPane interface {
	// PendingPermission is the card the pane is waiting on, or nil.
	PendingPermission() *bridge.Card
	// AnswerPermission gives the person's choice to the agent. It returns
	// ErrPermissionStale (possibly wrapped) when requestID is not the card
	// pending now. The first answer to a request wins; a second is stale.
	AnswerPermission(requestID, optionID string) error
}

// SetStructuredPanes says how a pane id is turned into a structured pane. Until
// it is set no pane is one. It is called before the server runs.
func (s *Server) SetStructuredPanes(lookup func(paneID string) (StructuredPane, bool)) {
	s.structuredPane = lookup
}

// permissionAnswerMsg is the reply to a permissionAnswer command.
type permissionAnswerMsg struct {
	Type      string `json:"type"`
	Req       string `json:"req"`
	ID        string `json:"id"`
	RequestID string `json:"requestId"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
}

// permissionLog is where an answer is recorded. A test replaces it.
var permissionLog = func(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "flockdeck: "+format+"\n", args...)
}

func (s *Server) permissionAnswer(c *controlClient, cmd command) {
	refuse := func(msg string) {
		c.sendJSON(permissionAnswerMsg{Type: "permissionAnswer", Req: cmd.Req, ID: cmd.ID, RequestID: cmd.RequestID, Error: msg})
		c.notify(msg, true)
	}
	// permission-answer-relay: the refusal to lift when a sealed team socket exists.
	if c.remote {
		refuse(permissionAnswerDeskOnly)
		return
	}
	if cmd.ID == "" || cmd.RequestID == "" || cmd.OptionID == "" {
		refuse(permissionBadRequest)
		return
	}
	s.do(func() {
		var pane StructuredPane
		if s.structuredPane != nil {
			if p, ok := s.structuredPane(cmd.ID); ok && p != nil {
				pane = p
			}
		}
		if pane == nil {
			refuse(permissionNotStructured)
			return
		}
		card := pane.PendingPermission()
		switch {
		case card == nil:
			refuse(permissionNothingAsked)
			return
		case card.RequestID != cmd.RequestID:
			refuse(permissionStale)
			return
		}
		offered := false
		kind := ""
		for _, o := range card.Options {
			if o.OptionID == cmd.OptionID {
				offered, kind = true, o.Kind
				break
			}
		}
		if !offered {
			refuse(permissionUnknownOption)
			return
		}
		if err := pane.AnswerPermission(cmd.RequestID, cmd.OptionID); err != nil {
			if errors.Is(err, ErrPermissionStale) {
				refuse(permissionStale)
				return
			}
			refuse("The answer could not be given: " + clipForNotice(err.Error()))
			return
		}
		// What is recorded is which pane, which request and what kind of option;
		// never the tool, command or file the card was about.
		permissionLog("permission answered: pane %s request %s option %s (%s)", cmd.ID, cmd.RequestID, cmd.OptionID, kind)
		c.sendJSON(permissionAnswerMsg{Type: "permissionAnswer", Req: cmd.Req, ID: cmd.ID, RequestID: cmd.RequestID, OK: true})
	})
}

// clipForNotice keeps an error shown to a window short.
func clipForNotice(s string) string {
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

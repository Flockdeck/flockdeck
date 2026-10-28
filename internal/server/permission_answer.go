package server

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/acp/bridge"
)

// permissionAnswer is the command a person's choice on a structured agent's
// permission card comes in on: {cmd: "permissionAnswer", id: pane, cardSeq,
// requestId, optionId}.
//
// It answers with the id of an option on the card the pane is showing now. The
// card is named by cardSeq, a number Flockdeck gave it, and by requestId, the
// agent's own id: an agent can end a request and ask again with the same id for
// something else, so the id alone does not say which card a click was for. The
// option id is taken from the request and compared, as a string, with the ids on
// that card. Nothing here looks at the kind of an option, and no answer is fixed.
// An agent chooses its own option ids, so one called "allow_always" is an
// ordinary id and would be accepted if it is on the card; a person chooses
// "allow always" by pressing the button for that option, which sends its id.
//
// A window on this machine may send it. A window reached through the relay may
// not, whether the device is full or a viewer (the viewer is refused by
// commandAccess, the full device by the command itself): the structured panes
// stay on the desk until a sealed team socket exists. Like paneSend, this is the
// one place to lift when it does.

// maxAnswers is the most answers a command may carry: a card has at most four
// questions.
const maxAnswers = 4

// permissionAnswerDeskOnly is what a window reached through the relay is told.
const permissionAnswerDeskOnly = "A structured agent's permission request is answered on the machine Flockdeck runs on, not from a window reached through the relay"

// The refusals. Each says what is wrong and is the whole of what a window is told.
const (
	permissionNotStructured = "That pane is not a structured agent pane"
	permissionNothingAsked  = "That pane is not waiting for an answer"
	permissionStale         = "That request has already been answered or has ended"
	permissionUnknownOption = "That is not one of the options offered"
	permissionBadRequest    = "The answer needs a pane, a card number, a request id and an option id"
	permissionBadAnswers    = "The answers do not fit the questions on the card"
)

// ErrPermissionStale is what a StructuredPane's AnswerPermission returns when
// the request is no longer the one waiting: it was answered, cancelled or
// replaced between the check and the answer.
var ErrPermissionStale = errors.New("the permission request is no longer waiting")

// ErrBadAnswers is what a QuestionPane's AnswerQuestions returns for answers
// that do not fit the card.
var ErrBadAnswers = errors.New("the answers do not fit the questions")

// QuestionPane is a StructuredPane that can also be given the answers to a
// question card. A pane without it cannot answer one.
type QuestionPane interface {
	StructuredPane
	AnswerQuestions(seq uint64, requestID, optionID string, answers []bridge.Answer) error
}

// StructuredPane is what permissionAnswer needs of a pane an agent drives over
// ACP. The workspace adapts its pane to it; tests use a fake.
type StructuredPane interface {
	// PendingPermission is the card the pane is waiting on, or nil.
	PendingPermission() *bridge.Card
	// AnswerPermission gives the person's choice to the agent. It returns
	// ErrPermissionStale (possibly wrapped) unless the card with this seq and
	// requestID is the one pending now, and another error for an option that
	// is not on it. The first answer to a card wins; a second is stale.
	//
	// It must be atomic: it checks the card and takes the answer under one
	// lock, because the server checks the card first and answers second, and
	// the card can change between the two. And it must not block: the server
	// calls it on its own goroutine, which every window waits on. The package
	// structuredtest runs these checks against an implementation.
	AnswerPermission(seq uint64, requestID, optionID string) error
	// OwnAdapter says the pane runs an adapter Flockdeck wrote (Claude, Codex),
	// whose option ids and kinds mean what they say. A pane that cannot say is
	// not one: the phone may deny its cards and not allow them.
	OwnAdapter() bool
	// RuntimeToken names this run of the pane: it changes when the pane restarts
	// and when the conversation is rewound. It is never empty.
	RuntimeToken() string
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
	CardSeq   uint64 `json:"cardSeq"`
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
		c.sendJSON(permissionAnswerMsg{Type: "permissionAnswer", Req: cmd.Req, ID: cmd.ID, CardSeq: cmd.CardSeq, RequestID: cmd.RequestID, Error: msg})
		c.notify(msg, true)
	}
	// permission-answer-relay: the refusal to lift when a sealed team socket exists.
	if c.remote {
		refuse(permissionAnswerDeskOnly)
		return
	}
	if cmd.ID == "" || cmd.CardSeq == 0 || cmd.RequestID == "" || cmd.OptionID == "" {
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
		case card.Seq != cmd.CardSeq || card.RequestID != cmd.RequestID:
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
		var err error
		if len(card.Questions) > 0 || len(cmd.Answers) > 0 {
			// Answers go with a question card and its allowing option, and
			// with nothing else: checked again by the pane, which holds the card.
			qp, ok := pane.(QuestionPane)
			if !ok || len(cmd.Answers) > maxAnswers {
				refuse(permissionBadAnswers)
				return
			}
			err = qp.AnswerQuestions(cmd.CardSeq, cmd.RequestID, cmd.OptionID, cmd.Answers)
		} else {
			err = pane.AnswerPermission(cmd.CardSeq, cmd.RequestID, cmd.OptionID)
		}
		if err != nil {
			if errors.Is(err, ErrPermissionStale) {
				refuse(permissionStale)
				return
			}
			if errors.Is(err, ErrBadAnswers) {
				refuse(permissionBadAnswers)
				return
			}
			refuse("The answer could not be given: " + cleanForNotice(err.Error()))
			return
		}
		// What is recorded is which pane, which request and what kind of option;
		// never the tool, command or file the card was about.
		permissionLog("permission answered: pane %s card %d request %s option %s (%s)", cleanForNotice(cmd.ID), cmd.CardSeq, cleanForNotice(cmd.RequestID), cleanForNotice(cmd.OptionID), kind)
		c.sendJSON(permissionAnswerMsg{Type: "permissionAnswer", Req: cmd.Req, ID: cmd.ID, CardSeq: cmd.CardSeq, RequestID: cmd.RequestID, OK: true})
	})
}

// cleanForNotice is s as one short line for a notice or a log: control
// characters become spaces and it is cut at 200 bytes at a character.
func cleanForNotice(s string) string {
	s = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, s)), " ")
	if len(s) > 200 {
		s = s[:200]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		s += "..."
	}
	return s
}

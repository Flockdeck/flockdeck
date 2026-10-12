package hooks

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/team"
)

// This file is the callback API for the team: the roster of a pane's project
// (`flockdeck team`) and the messages between panes (`flockdeck msg`,
// `flockdeck inbox`). It follows spawn, peer-name and close: the pane sends its
// own id (FLOCKDECK_PANE) and the token it was started with, and the application
// refuses a request whose pane is not one it has open.
//
// The token is the same for every pane of a run, so it shows the caller is a
// process Flockdeck started and not which pane. The pane id is what a caller says
// it is; see the handlers' own note in internal/workspace/team.go.

// Auth is what every team request carries.
type Auth struct {
	// Pane is the caller's own id. It is the only pane the request acts as: a
	// message is sent from it and an inbox read is its own.
	Pane  string `json:"pane"`
	Token string `json:"token"`
}

func (a Auth) token() string { return a.Token }

type authed interface{ token() string }

// TeamRequest asks for the roster of the caller's project.
type TeamRequest struct {
	Auth
}

// TeamResult is the roster, with the caller's own entry marked Self.
type TeamResult struct {
	Members []team.Member `json:"members"`
}

// MsgRequest sends a message from the caller to a pane, a name or a role.
type MsgRequest struct {
	Auth
	Target string `json:"target"`
	Text   string `json:"text"`
}

// Sent is one delivery of a message: the inbox it went to and the id it has there.
type Sent struct {
	ID   int    `json:"id"`
	Pane string `json:"pane"`
	To   string `json:"to"`
}

// MsgResult says where a message went. A role has more than one entry.
type MsgResult struct {
	Sent []Sent `json:"sent"`
	// Failed names the targets of a role that did not get it, with the reason.
	Failed []string `json:"failed,omitempty"`
}

// InboxRequest reads the caller's own inbox. With Read set it returns that one
// message and marks it read; otherwise it lists the unread messages, or every
// message with All.
type InboxRequest struct {
	Auth
	All  bool `json:"all,omitempty"`
	Read int  `json:"read,omitempty"`
}

// InboxResult is the messages asked for, oldest first.
type InboxResult struct {
	Messages []team.Message `json:"messages"`
}

// teamHandlers are the functions the application installs.
type teamHandlers struct {
	team  func(TeamRequest) (TeamResult, error)
	msg   func(MsgRequest) (MsgResult, error)
	inbox func(InboxRequest) (InboxResult, error)
}

// SetTeamHandler installs the function that answers `flockdeck team`.
func (s *Server) SetTeamHandler(fn func(TeamRequest) (TeamResult, error)) {
	s.mu.Lock()
	s.onTeam.team = fn
	s.mu.Unlock()
}

// SetMsgHandler installs the function that sends a message.
func (s *Server) SetMsgHandler(fn func(MsgRequest) (MsgResult, error)) {
	s.mu.Lock()
	s.onTeam.msg = fn
	s.mu.Unlock()
}

// SetInboxHandler installs the function that reads an inbox.
func (s *Server) SetInboxHandler(fn func(InboxRequest) (InboxResult, error)) {
	s.mu.Lock()
	s.onTeam.inbox = fn
	s.mu.Unlock()
}

// serveJSON is the handler every team endpoint shares: post only, a body of at
// most 1 MiB, the run's token, then the installed function. A refusal from the
// function is a 400 with its own words, which the command prints.
func serveJSON[Req authed, Res any](s *Server, what string, installed func() func(Req) (Res, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req Req
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if subtle.ConstantTimeCompare([]byte(req.token()), []byte(s.token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		s.mu.RLock()
		fn := installed()
		s.mu.RUnlock()
		if fn == nil {
			http.Error(w, what+" is not available", http.StatusServiceUnavailable)
			return
		}
		res, err := fn(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}
}

func (s *Server) handleTeam() http.HandlerFunc {
	return serveJSON(s, "the team list", func() func(TeamRequest) (TeamResult, error) { return s.onTeam.team })
}

func (s *Server) handleMsg() http.HandlerFunc {
	return serveJSON(s, "messaging", func() func(MsgRequest) (MsgResult, error) { return s.onTeam.msg })
}

func (s *Server) handleInbox() http.HandlerFunc {
	return serveJSON(s, "the inbox", func() func(InboxRequest) (InboxResult, error) { return s.onTeam.inbox })
}

// teamTimeout is how long the team commands wait for the application. Nothing
// they ask for is slow, so an answer that has not come by now is not coming.
var teamTimeout = 10 * time.Second

func teamFailure(err error, api string) error {
	var op *net.OpError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("Flockdeck did not answer within %s; it may be busy, so try again shortly", teamTimeout)
	case errors.As(err, &op) && (op.Op == "dial" || op.Op == "read"):
		return fmt.Errorf("Flockdeck is not answering at %s; it may have been closed since this pane started", api)
	}
	return err
}

// post sends req to one of the team endpoints and decodes the answer.
func post[Res any](api, path string, req any) (Res, error) {
	var none Res
	body, err := json.Marshal(req)
	if err != nil {
		return none, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), teamTimeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, api+path, bytes.NewReader(body))
	if err != nil {
		return none, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return none, teamFailure(err, api)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if text := strings.TrimSpace(string(msg)); text != "" {
			return none, fmt.Errorf("%s", text)
		}
		return none, fmt.Errorf("the application refused: %s", resp.Status)
	}
	var out Res
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return none, fmt.Errorf("unreadable answer from the application: %w", err)
	}
	return out, nil
}

// Team is the client half of `flockdeck team`.
func Team(api, token, pane string) (TeamResult, error) {
	return post[TeamResult](api, "/team", TeamRequest{Auth{Pane: pane, Token: token}})
}

// Msg is the client half of `flockdeck msg send`.
func Msg(api, token, pane, target, text string) (MsgResult, error) {
	return post[MsgResult](api, "/msg", MsgRequest{Auth: Auth{Pane: pane, Token: token}, Target: target, Text: text})
}

// Inbox is the client half of `flockdeck inbox`: the unread messages, or all of
// them.
func Inbox(api, token, pane string, all bool) ([]team.Message, error) {
	res, err := post[InboxResult](api, "/inbox", InboxRequest{Auth: Auth{Pane: pane, Token: token}, All: all})
	return res.Messages, err
}

// ReadMessage is the client half of `flockdeck inbox read`.
func ReadMessage(api, token, pane string, id int) (team.Message, error) {
	res, err := post[InboxResult](api, "/inbox", InboxRequest{Auth: Auth{Pane: pane, Token: token}, Read: id})
	if err != nil {
		return team.Message{}, err
	}
	if len(res.Messages) != 1 {
		return team.Message{}, fmt.Errorf("the application sent %d messages for one id", len(res.Messages))
	}
	return res.Messages[0], nil
}

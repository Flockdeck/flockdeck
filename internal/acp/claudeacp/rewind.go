package claudeacp

import (
	"encoding/json"
	"regexp"

	"github.com/google/uuid"
)

// Going back to an earlier turn. The client names each prompt with a token in
// the prompt's _meta (the extension in internal/acp/rewind.go; this package does
// not import that one). The adapter gives the token to claude as the uuid of the
// user message, so it is the name claude keeps for that message, and remembers
// for every finished turn the uuid of claude's last assistant message in it.
//
// claude can start from a session cut after a message: --resume ID
// --resume-session-at UUID continues the conversation with everything after
// UUID left out, and with --fork-session --session-id NEW it does so as a new
// session, leaving ID as it was. So "before turn T" is the end of the turn
// before T, and the first turn, or a turn whose predecessor did not end by
// itself (it was cancelled, or failed), has no such point.
//
//   - _flockdeck/forkpoint answers that uuid. session/fork with it in _meta
//     starts claude with the fork flags. The fork exists as a file only once it
//     has a first message; claude writes nothing before that.
//   - _flockdeck/rewind starts a second claude with --resume-session-at on the
//     same session id, moves the session over to it, and ends the first. It is
//     refused while a turn, or a prompt waiting for one, is there.
//
// The turns are kept in memory. After the adapter restarts, a session it
// resumes knows none of the earlier turns: they cannot be named.

const (
	extKey     = "flockdeck.dev"
	maxTurns   = 2000
	msgNoPoint = "that turn has no earlier point to go back to"
)

var turnToken = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// turnRec is a finished or running turn: its token, and the uuid of claude's
// last assistant message in it when the turn ended by itself.
type turnRec struct {
	token string
	last  string
}

// extMeta reads what the client put under _meta["flockdeck.dev"]. Anything of
// the wrong type, or not shaped as a token, is left out.
func extMeta(params json.RawMessage) (turn, at string) {
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if json.Unmarshal(params, &p) != nil {
		return "", ""
	}
	var ext struct {
		Turn string `json:"turn"`
		At   string `json:"at"`
	}
	if json.Unmarshal(p.Meta[extKey], &ext) != nil {
		return "", ""
	}
	if turnToken.MatchString(ext.Turn) {
		turn = ext.Turn
	}
	if u, err := uuid.Parse(ext.At); err == nil {
		at = u.String()
	}
	return turn, at
}

// pointLocked is the uuid of the message to keep through so that the turn named
// by token is the first one dropped. Call with ss.mu held.
func (ss *session) pointLocked(token string) (string, *rpcErr) {
	if !turnToken.MatchString(token) {
		return "", &rpcErr{Code: codeInvalidParams, Message: "beforeTurn is not a turn"}
	}
	for i, t := range ss.turns {
		if t.token != token {
			continue
		}
		if i == 0 || ss.turns[i-1].last == "" {
			return "", &rpcErr{Code: codeInvalidParams, Message: msgNoPoint}
		}
		return ss.turns[i-1].last, nil
	}
	return "", &rpcErr{Code: codeInvalidParams, Message: "that turn is not known to this session"}
}

func (s *server) forkPoint(m rpcMsg) {
	var p struct {
		SessionID  string `json:"sessionId"`
		BeforeTurn string `json:"beforeTurn"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: err.Error()})
		return
	}
	ss := s.session(p.SessionID)
	if ss == nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "unknown session " + p.SessionID})
		return
	}
	ss.mu.Lock()
	at, e := ss.pointLocked(p.BeforeTurn)
	ss.mu.Unlock()
	if e != nil {
		s.replyErr(m.ID, e)
		return
	}
	s.reply(m.ID, map[string]any{"at": at})
}

// rewind goes back to before the turn: the session is started again from the
// end of the turn before it.
func (s *server) rewind(m rpcMsg) {
	var p struct {
		SessionID  string `json:"sessionId"`
		BeforeTurn string `json:"beforeTurn"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: err.Error()})
		return
	}
	old := s.session(p.SessionID)
	if old == nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "unknown session " + p.SessionID})
		return
	}
	old.mu.Lock()
	at, e := old.pointLocked(p.BeforeTurn)
	if e == nil && (old.cur != nil || old.busy || old.rewinding || len(old.queue) > 0 || closedCh(old.dead)) {
		e = &rpcErr{Code: codeInvalidParams, Message: "the session is not idle"}
	}
	var keep []turnRec
	mode, model, claudeID := old.mode, old.model, old.claudeID
	allowed := map[string]bool{}
	if e == nil {
		old.rewinding = true
		for i, t := range old.turns {
			if t.token == p.BeforeTurn {
				keep = append(keep, old.turns[:i]...)
				break
			}
		}
		for k, v := range old.allowed {
			allowed[k] = v
		}
	}
	old.mu.Unlock()
	if e != nil {
		s.replyErr(m.ID, e)
		return
	}
	fail := func(why string) {
		old.mu.Lock()
		old.rewinding = false
		old.mu.Unlock()
		s.logf("rewind: %s", why)
		s.replyErr(m.ID, &rpcErr{Code: codeInternal, Message: "the session could not be rewound"})
	}

	ns := s.newSession(old.id, old.cwd, old.mcp)
	ns.resume, ns.resumeAt, ns.turns, ns.allowed, ns.claudeID = true, at, keep, allowed, claudeID
	var err error
	if ns.mcpFile, err = writeMCPConfig(old.mcp); err != nil {
		fail(err.Error())
		return
	}
	if err := ns.start(); err != nil {
		ns.cleanup()
		fail("start claude: " + err.Error())
		return
	}
	ns.initialize()
	select {
	case <-ns.dead:
		ns.cleanup()
		fail("claude exited before the session started")
		return
	default:
	}
	// The new claude has the defaults; the person's choices are put back.
	if mode != ns.mode {
		if _, err := ns.control("set_permission_mode", map[string]any{"mode": mode}); err != nil {
			ns.close()
			fail("set_permission_mode: " + err.Error())
			return
		}
		ns.mode = mode
	}
	if model != ns.model {
		if _, err := ns.control("set_model", map[string]any{"model": model}); err != nil {
			ns.close()
			fail("set_model: " + err.Error())
			return
		}
		ns.model = model
	}
	s.mu.Lock()
	if s.closed || s.sessions[old.id] != old {
		s.mu.Unlock()
		ns.close()
		fail("the session closed during the rewind")
		return
	}
	for id, v := range s.sessions {
		if v == old {
			s.sessions[id] = ns
		}
	}
	s.mu.Unlock()
	go ns.worker()
	old.close()
	s.reply(m.ID, map[string]any{})
}

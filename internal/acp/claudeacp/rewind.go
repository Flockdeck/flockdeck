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
//   - _flockdeck/rewind starts a second claude with --resume-session-at as a
//     fork (--fork-session --session-id NEW), moves the session over to it, tells
//     the client the conversation now lives under NEW (flockdeck_session, which
//     the pane saves), and ends the first. The session keeps the id the client
//     prompts with. It is a fork and not a cut of the same session because claude
//     resumes a session by its latest message: a cut of the same id that had
//     received no message yet would come back whole the next time the session was
//     resumed (checked against claude 2.1.312). It is refused while a turn, or a
//     prompt waiting for one, is there.
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
func extMeta(params json.RawMessage) (turn, at string, badAt bool) {
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if json.Unmarshal(params, &p) != nil {
		return "", "", false
	}
	var ext struct {
		Turn string          `json:"turn"`
		At   json.RawMessage `json:"at"`
	}
	if json.Unmarshal(p.Meta[extKey], &ext) != nil {
		return "", "", false
	}
	if turnToken.MatchString(ext.Turn) {
		turn = ext.Turn
	}
	// A fork point that is present but is not a uuid is reported, so that the
	// caller refuses instead of forking the whole conversation.
	if len(ext.At) > 0 && string(ext.At) != "null" {
		var s string
		if json.Unmarshal(ext.At, &s) != nil {
			return turn, "", true
		}
		u, err := uuid.Parse(s)
		if err != nil {
			return turn, "", true
		}
		at = u.String()
	}
	return turn, at, false
}

// pointLocked is the uuid of the message to keep through so that the turn named
// by token is the first one dropped. Call with ss.mu held.
func (ss *session) pointLocked(token string) (string, *rpcErr) {
	if !turnToken.MatchString(token) {
		return "", &rpcErr{Code: codeInvalidParams, Message: "beforeTurn is not a turn"}
	}
	// After /clear claude keeps the conversation under an id of its own choosing
	// (cleared). The uuids held here name messages of the old session file, so no
	// point is given: neither a rewind nor a fork at a turn can start claude from
	// the wrong file. (A rewind moves the conversation to an id Flockdeck
	// chooses, forkID, and the fork keeps the uuids of the messages it keeps; that
	// was checked against claude 2.1.312 and is not "cleared".)
	if ss.cleared {
		return "", &rpcErr{Code: codeInvalidParams, Message: "the session was cleared; its earlier turns cannot be named"}
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
	mode, model := old.mode, old.model
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

	// claude's own id for the conversation now: the one it moved to after a
	// rewind, else the one the session was opened with. A session that has had
	// no message has no file yet (pendingSrc), and the rewind goes on from what
	// it was made from.
	src := old.id
	old.mu.Lock()
	if old.claudeID != "" {
		src = old.claudeID
	}
	if old.pendingSrc != "" {
		src = old.pendingSrc
	}
	old.mu.Unlock()
	newID := uuid.NewString()
	ns := s.newSession(old.id, old.cwd, old.mcp)
	ns.forkOf, ns.forkID, ns.resumeAt, ns.turns, ns.allowed = src, newID, at, keep, allowed
	ns.pendingSrc, ns.pendingAt = src, at
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
	_, herr := ns.initialize()
	select {
	case <-ns.dead:
		ns.cleanup()
		fail("claude exited before the session started")
		return
	default:
	}
	if herr != nil {
		// As in openSession: the control protocol did not answer as expected.
		ns.close()
		fail("the control protocol handshake failed: " + herr.Error())
		return
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
	// The conversation lives under newID from here: the client saves that id to
	// resume by (flockdeck_session), and the id it prompts with does not change.
	ns.adopt(newID)
	// The id the first claude had moved to is no longer this session's.
	old.mu.Lock()
	oc := old.claudeID
	old.mu.Unlock()
	s.mu.Lock()
	if oc != "" && oc != newID && s.sessions[oc] == ns {
		delete(s.sessions, oc)
	}
	s.mu.Unlock()
	old.close()
	s.reply(m.ID, map[string]any{})
}

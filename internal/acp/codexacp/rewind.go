package codexacp

import (
	"encoding/json"
	"regexp"
	"strconv"
)

// Going back to an earlier turn. The client names each prompt with a token in
// the prompt's _meta (internal/acp/rewind.go; this package does not import
// that one). The adapter keeps the tokens of the turns it has run, in order, and
// a turn's place from the end is what codex needs: thread/rollback drops the
// last N turns of a thread, and thread/fork copies a whole one.
//
//   - _flockdeck/rewind: thread/rollback with the number of turns from the one
//     named to the end, on the live thread. Refused while a turn, or a prompt
//     waiting for one, is there.
//   - _flockdeck/forkpoint: answers "rbN", N being that number. session/fork
//     with it in _meta is thread/fork of the source thread, then thread/rollback
//     of N turns on the new thread.
//
// Only plain turns are counted. A /compact or /review in the part that would be
// dropped, or a turn the adapter did not run (before a resume), makes the point
// refused: the number of turns codex would drop is not known then.
//
// Nothing here was run against a real codex; the wire is from the route
// document and the fake app-server (docs/acp-codex-adapter.md).

const (
	extKey   = "flockdeck.dev"
	maxTurns = 2000
)

var (
	turnToken = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	atRe      = regexp.MustCompile(`^rb([0-9]{1,4})$`)
)

// turnRec is a turn the thread has: the token the client gave it, and whether
// it is a plain turn.
type turnRec struct {
	token string
	plain bool
}

// extMeta reads what the client put under _meta["flockdeck.dev"]: a turn token,
// and the number of turns a fork is to drop (0 for none). Anything of the wrong
// type or shape is left out.
func extMeta(params json.RawMessage) (turn string, drop int) {
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if json.Unmarshal(params, &p) != nil {
		return "", 0
	}
	var ext struct {
		Turn string `json:"turn"`
		At   string `json:"at"`
	}
	if json.Unmarshal(p.Meta[extKey], &ext) != nil {
		return "", 0
	}
	if turnToken.MatchString(ext.Turn) {
		turn = ext.Turn
	}
	if m := atRe.FindStringSubmatch(ext.At); m != nil {
		drop, _ = strconv.Atoi(m[1])
	}
	return turn, drop
}

// dropCountLocked is how many turns there are from the one named to the end.
// Call with ss.mu held.
func (ss *session) dropCountLocked(token string) (int, *rpcErr) {
	if !turnToken.MatchString(token) {
		return 0, &rpcErr{Code: codeInvalidParams, Message: "beforeTurn is not a turn"}
	}
	for i, t := range ss.turns {
		if t.token != token {
			continue
		}
		if i == 0 {
			return 0, &rpcErr{Code: codeInvalidParams, Message: "that turn has no earlier point to go back to"}
		}
		for _, later := range ss.turns[i:] {
			if !later.plain {
				return 0, &rpcErr{Code: codeInvalidParams, Message: "a command ran after that turn"}
			}
		}
		return len(ss.turns) - i, nil
	}
	return 0, &rpcErr{Code: codeInvalidParams, Message: "that turn is not known to this session"}
}

func (s *server) forkPoint(m rpcMsg) {
	var p struct {
		SessionID  string `json:"sessionId"`
		BeforeTurn string `json:"beforeTurn"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "bad parameters"})
		return
	}
	ss := s.session(p.SessionID)
	if ss == nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "unknown session"})
		return
	}
	ss.mu.Lock()
	n, e := ss.dropCountLocked(p.BeforeTurn)
	ss.mu.Unlock()
	if e != nil {
		s.replyErr(m.ID, e)
		return
	}
	s.reply(m.ID, map[string]any{"at": "rb" + strconv.Itoa(n)})
}

func (s *server) rewind(m rpcMsg) {
	var p struct {
		SessionID  string `json:"sessionId"`
		BeforeTurn string `json:"beforeTurn"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "bad parameters"})
		return
	}
	ss := s.session(p.SessionID)
	if ss == nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "unknown session"})
		return
	}
	ss.mu.Lock()
	n, e := ss.dropCountLocked(p.BeforeTurn)
	if e == nil && (ss.cur != nil || ss.rewinding || len(ss.queue) > 0 || closedCh(ss.dead)) {
		e = &rpcErr{Code: codeInvalidParams, Message: "the session is not idle"}
	}
	if e == nil {
		ss.rewinding = true
	}
	ss.mu.Unlock()
	if e != nil {
		s.replyErr(m.ID, e)
		return
	}
	_, err := ss.call("thread/rollback", map[string]any{"threadId": ss.id, "numTurns": n})
	ss.mu.Lock()
	ss.rewinding = false
	if err == nil && len(ss.turns) >= n {
		ss.turns = ss.turns[:len(ss.turns)-n]
	}
	ss.mu.Unlock()
	if err != nil {
		ss.logf("rewind: %v", err)
		s.replyErr(m.ID, &rpcErr{Code: codeInternal, Message: "the session could not be rewound"})
		return
	}
	s.reply(m.ID, map[string]any{})
}

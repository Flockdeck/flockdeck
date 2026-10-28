package codexacp

import (
	"encoding/json"
	"errors"
	"fmt"
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
//   - _flockdeck/forkpoint: answers "kN", N being the number of turns the fork
//     keeps, counted from the start of the thread. session/fork with it in
//     _meta is thread/fork of the source thread, then thread/rollback of however
//     many turns the new thread has beyond N. The new thread's turns are counted
//     when it is made (thread/read), so a turn that ran on the source after the
//     point was chosen does not move the cut. If the count cannot be had, the
//     fork is refused.
//
// Only plain turns are counted. A /compact or /review in the part that would be
// dropped makes the point refused. So does a session that has turns before the
// ones the adapter ran (a resumed thread, or a fork): N would not be counted
// from the start of the thread. _flockdeck/rewind counts from the end, under the
// session's lock with nothing running, so it has no such limit.
//
// Nothing here was run against a real codex; the wire is from the route
// document and the fake app-server (docs/acp-codex-adapter.md).

const (
	extKey   = "flockdeck.dev"
	maxTurns = 2000
)

var (
	turnToken = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	atRe      = regexp.MustCompile(`^k([0-9]{1,4})$`)
)

// turnRec is a turn the thread has: the token the client gave it, and whether
// it is a plain turn.
type turnRec struct {
	token string
	plain bool
}

// extMeta reads what the client put under _meta["flockdeck.dev"]: a turn token,
// and the number of turns a fork is to keep (-1 when there is no fork point). A
// turn that is not a token is left out; a fork point that is present but not
// "kN" is reported as bad, so that the caller refuses instead of forking the
// whole thread.
func extMeta(params json.RawMessage) (turn string, keep int, bad bool) {
	keep = -1
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if json.Unmarshal(params, &p) != nil {
		return "", -1, false
	}
	var ext struct {
		Turn string          `json:"turn"`
		At   json.RawMessage `json:"at"`
	}
	if json.Unmarshal(p.Meta[extKey], &ext) != nil {
		return "", -1, false
	}
	if turnToken.MatchString(ext.Turn) {
		turn = ext.Turn
	}
	if len(ext.At) > 0 && string(ext.At) != "null" {
		var at string
		if json.Unmarshal(ext.At, &at) != nil {
			return turn, -1, true
		}
		m := atRe.FindStringSubmatch(at)
		if m == nil {
			return turn, -1, true
		}
		keep, _ = strconv.Atoi(m[1])
	}
	return turn, keep, false
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
	kept := len(ss.turns) - n
	if e == nil && ss.hasHistory {
		e = &rpcErr{Code: codeInvalidParams, Message: "this session has turns before the ones this adapter ran"}
	}
	ss.mu.Unlock()
	if e != nil {
		s.replyErr(m.ID, e)
		return
	}
	s.reply(m.ID, map[string]any{"at": "k" + strconv.Itoa(kept)})
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

// cutFork takes a new thread back to its first keep turns. It counts the
// thread's turns (thread/read with turns); a count it cannot get, or fewer turns
// than keep, is an error and the fork is not used.
func (ss *session) cutFork(thread string, keep int) error {
	raw, err := ss.call("thread/read", map[string]any{"threadId": thread, "includeTurns": true})
	if err != nil {
		return err
	}
	var r struct {
		Thread struct {
			Turns []json.RawMessage `json:"turns"`
		} `json:"thread"`
	}
	if json.Unmarshal(raw, &r) != nil || r.Thread.Turns == nil {
		return errors.New("thread/read gave no turns")
	}
	n := len(r.Thread.Turns)
	if n < keep {
		return fmt.Errorf("the fork has %d turns, fewer than the %d to keep", n, keep)
	}
	if n == keep {
		return nil
	}
	_, err = ss.call("thread/rollback", map[string]any{"threadId": thread, "numTurns": n - keep})
	return err
}

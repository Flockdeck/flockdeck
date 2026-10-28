package acptest

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// The rewind extension (internal/acp/rewind.go), with the session option
// "rewind" (WithSessionOptions). The agent then advertises it and keeps the turn
// tokens its prompts carried:
//
//	turns                 a prompt that reports them, as "turns=t1,t2"
//	_flockdeck/rewind     drops the named turn and every later one; an unknown turn is refused with -32602
//	_flockdeck/forkpoint  answers {at: "at-<index of the turn>"}, or -32602 for an unknown turn
//	session/fork          answers {sessionId: "fork-of-<source>-<at or all>"}
//
// Every call is also written to stderr as "acptest: rewind ...", for tests that
// start the agent and read what it logged.

func (a *agent) rewindCaps(caps map[string]any) {
	if !a.hasSessionOpt("rewind") {
		return
	}
	caps["_meta"] = map[string]any{"flockdeck.dev": map[string]any{"rewind": 1}}
	sc, _ := caps["sessionCapabilities"].(map[string]any)
	sc = cloneMap(sc)
	if sc == nil {
		sc = map[string]any{}
	}
	sc["fork"] = map[string]any{}
	caps["sessionCapabilities"] = sc
}

// noteTurn keeps the turn token a prompt carried, when the agent has the extension.
func (a *agent) noteTurn(params json.RawMessage) {
	if !a.hasSessionOpt("rewind") {
		return
	}
	var p struct {
		Meta map[string]map[string]any `json:"_meta"`
	}
	_ = json.Unmarshal(params, &p)
	if t, _ := p.Meta["flockdeck.dev"]["turn"].(string); t != "" {
		a.mu.Lock()
		a.turnTokens = append(a.turnTokens, t)
		a.mu.Unlock()
	}
}

func (a *agent) turnList() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.Join(a.turnTokens, ",")
}

func (a *agent) turnIndex(t string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, x := range a.turnTokens {
		if x == t {
			return i
		}
	}
	return -1
}

func (a *agent) rewindCall(m msg) {
	var p struct {
		SessionID  string                    `json:"sessionId"`
		BeforeTurn string                    `json:"beforeTurn"`
		Meta       map[string]map[string]any `json:"_meta"`
	}
	_ = json.Unmarshal(m.Params, &p)
	switch m.Method {
	case "session/fork":
		at, _ := p.Meta["flockdeck.dev"]["at"].(string)
		fmt.Fprintf(os.Stderr, "acptest: rewind fork source=%s at=%s\n", p.SessionID, at)
		if at == "" {
			at = "all"
		}
		res := map[string]any{"sessionId": "fork-of-" + p.SessionID + "-" + at}
		a.sessionShape(res)
		a.result(m.ID, res)
	case "_flockdeck/forkpoint":
		i := a.turnIndex(p.BeforeTurn)
		fmt.Fprintf(os.Stderr, "acptest: rewind forkpoint turn=%s index=%d\n", p.BeforeTurn, i)
		if i < 0 {
			a.rpcError(m.ID, -32602, "unknown turn SECRET-AGENT-TEXT")
			return
		}
		a.result(m.ID, map[string]any{"at": fmt.Sprintf("at-%d", i)})
	default:
		i := a.turnIndex(p.BeforeTurn)
		fmt.Fprintf(os.Stderr, "acptest: rewind rewind turn=%s index=%d\n", p.BeforeTurn, i)
		if i < 0 {
			a.rpcError(m.ID, -32602, "unknown turn SECRET-AGENT-TEXT")
			return
		}
		if a.hasSessionOpt("rewindhang") {
			return
		}
		a.mu.Lock()
		a.turnTokens = a.turnTokens[:i]
		a.mu.Unlock()
		a.result(m.ID, map[string]any{})
	}
}

package codexacp

import (
	"encoding/json"
	"strings"
)

// dispatch handles one line from codex. It runs on the goroutine that reads
// codex, so it must not wait: a request from codex is answered on a goroutine
// of its own.
func (ss *session) dispatch(b []byte) {
	var m rpcMsg
	if json.Unmarshal(b, &m) != nil {
		ss.logf("ignored a line from codex that is not JSON")
		return
	}
	switch {
	case m.Method == "":
		ss.handleResponse(m)
	case m.ID != nil:
		select {
		case ss.reqSem <- struct{}{}:
			go func() {
				defer func() { <-ss.reqSem }()
				ss.handleRequest(m)
			}()
		default:
			ss.refuse(m.ID, "too many requests at once")
		}
	default:
		ss.handleNotification(m)
	}
}

// handleResponse hands a response to the call waiting for its id. A response
// is matched by id only, never by position.
func (ss *session) handleResponse(m rpcMsg) {
	if m.ID == nil {
		return
	}
	var id int64
	if json.Unmarshal(*m.ID, &id) != nil {
		return
	}
	ss.mu.Lock()
	ch := ss.pending[id]
	ss.mu.Unlock()
	if ch != nil {
		select {
		case ch <- m:
		default:
		}
	}
}

// refuse answers a request from codex with an error, so that it does not wait.
func (ss *session) refuse(id *json.RawMessage, msg string) {
	if id == nil {
		return
	}
	_ = ss.write(map[string]any{"id": id, "error": map[string]any{"code": codeMethodNotFound, "message": msg}})
}

func textContent(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

// emit sends a session/update for this session. Before thread/start has
// answered there is no session to send it for.
func (ss *session) emit(u map[string]any) {
	if id := ss.tid(); id != "" {
		ss.srv.update(id, u)
	}
}

// mine reports whether a notification is about this session's thread. One
// with no thread id is taken to be.
func (ss *session) mine(threadID string) bool {
	return threadID == "" || threadID == ss.tid()
}

type planStep struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

type tokens struct {
	Total int64 `json:"totalTokens"`
}

func (ss *session) handleNotification(m rpcMsg) {
	var p struct {
		ThreadID string `json:"threadId"`
		ItemID   string `json:"itemId"`
		Delta    string `json:"delta"`
		Index    int    `json:"summaryIndex"`
		Item     *item  `json:"item"`
		Turn     struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  *struct {
				Message string          `json:"message"`
				Info    json.RawMessage `json:"codexErrorInfo"`
			} `json:"error"`
		} `json:"turn"`
		Changes    json.RawMessage `json:"changes"`
		Plan       []planStep      `json:"plan"`
		RequestID  json.RawMessage `json:"requestId"`
		TokenUsage *struct {
			Total  tokens `json:"total"`
			Last   tokens `json:"last"`
			Window int64  `json:"modelContextWindow"`
		} `json:"tokenUsage"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(m.Params, &p) != nil || !ss.mine(p.ThreadID) {
		return
	}
	if (m.Method == "turn/started" || m.Method == "turn/completed") && p.ThreadID == "" {
		return // the start and end of a turn must say whose it is
	}
	switch m.Method {
	case "turn/started":
		ss.mu.Lock()
		t := ss.cur
		ss.mu.Unlock()
		if t != nil && p.Turn.ID != "" {
			ss.setTurnID(t, p.Turn.ID)
		}
	case "turn/completed":
		var msg, name string
		if p.Turn.Error != nil {
			msg, name = p.Turn.Error.Message, errName(p.Turn.Error.Info)
		}
		ss.finishTurn(p.Turn.ID, ss.classifyTurn(p.Turn.Status, msg, name))
	case "item/started":
		if p.Item != nil {
			ss.itemStarted(p.Item)
		}
	case "item/completed":
		if p.Item != nil {
			ss.itemCompleted(p.Item)
		}
	case "item/agentMessage/delta":
		if p.Delta != "" {
			ss.mark(p.ItemID, func(st *itemState) { st.streamed = true })
			ss.emit(map[string]any{"sessionUpdate": "agent_message_chunk", "content": textContent(p.Delta)})
		}
	case "item/reasoning/summaryTextDelta":
		if p.Delta != "" {
			ss.mark(p.ItemID, func(st *itemState) { st.streamed, st.summary = true, true })
			ss.emit(map[string]any{"sessionUpdate": "agent_thought_chunk", "content": textContent(p.Delta)})
		}
	case "item/reasoning/summaryPartAdded":
		if p.Index > 0 {
			ss.emit(map[string]any{"sessionUpdate": "agent_thought_chunk", "content": textContent("\n\n")})
		}
	case "item/reasoning/textDelta":
		// The raw reasoning is shown only when the item has no summary.
		if p.Delta != "" && ss.rawReasoning(p.ItemID) {
			ss.emit(map[string]any{"sessionUpdate": "agent_thought_chunk", "content": textContent(p.Delta)})
		}
	case "item/commandExecution/outputDelta":
		ss.commandOutput(p.ItemID, p.Delta)
	case "item/fileChange/patchUpdated":
		ss.patchUpdated(p.ItemID, p.Changes)
	case "turn/plan/updated":
		entries := []map[string]any{}
		for _, s := range p.Plan {
			if len(entries) >= 100 {
				break
			}
			entries = append(entries, map[string]any{"content": s.Step, "priority": "medium", "status": planStatus(s.Status)})
		}
		ss.emit(map[string]any{"sessionUpdate": "plan", "entries": entries})
	case "thread/tokenUsage/updated":
		if u := p.TokenUsage; u != nil {
			ss.usage(u.Last.Total, u.Total.Total, u.Window)
		}
	case "serverRequest/resolved":
		// Codex settled a request without us (the turn was interrupted):
		// withdraw the card and send no answer.
		ss.mu.Lock()
		op := ss.perms[string(p.RequestID)]
		ss.mu.Unlock()
		if op != nil {
			op.abandon()
		}
	case "error":
		ss.logf("codex reported an error: %s", clip(p.Error.Message, 500))
	}
}

func planStatus(s string) string {
	switch strings.ToLower(s) {
	case "inprogress", "in_progress", "in-progress":
		return "in_progress"
	case "completed":
		return "completed"
	}
	return "pending"
}

// usage sends a usage_update: the size of the last request against the
// window. There is no cost, because codex does not report one.
func (ss *session) usage(last, total, window int64) {
	used := last
	if used == 0 {
		used = total
	}
	ss.mu.Lock()
	if window > 0 {
		ss.window = window
	}
	window = ss.window
	ss.lastUsed = used
	ss.mu.Unlock()
	if window > 0 {
		ss.emit(map[string]any{"sessionUpdate": "usage_update", "used": used, "size": window})
	}
}

// errName is the kind of a codexErrorInfo, which is a string for most errors
// and an object with one member for those that carry detail.
func errName(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o map[string]json.RawMessage
	if json.Unmarshal(raw, &o) == nil {
		for k := range o {
			return k
		}
	}
	return ""
}

// classifyTurn turns the end of a turn into an ACP stop reason or an error.
// The error text is never codex's.
func (ss *session) classifyTurn(status, msg, name string) turnResult {
	switch status {
	case "completed":
		return turnResult{stop: "end_turn"}
	case "interrupted":
		return turnResult{stop: "cancelled"}
	case "failed":
		ss.logf("the turn failed (%s): %s", name, clip(msg, 500))
		switch name {
		case "unauthorized":
			return turnResult{err: &rpcErr{Code: codeAuthRequired, Message: "Authentication required"}}
		case "contextWindowExceeded":
			return turnResult{stop: "max_tokens"}
		}
	}
	// failed, or a status this adapter does not know.
	return turnResult{err: &rpcErr{Code: codeInternal, Message: "the turn failed"}}
}

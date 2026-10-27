package claudeacp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// maxLine is the longest line read from claude. A tool result can be large; a
// line past this ends the session, and is never held in memory whole.
var maxLine = 16 << 20

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type usage struct {
	Input         int64 `json:"input_tokens"`
	Output        int64 `json:"output_tokens"`
	CacheRead     int64 `json:"cache_read_input_tokens"`
	CacheCreation int64 `json:"cache_creation_input_tokens"`
}

func (u usage) context() int64 { return u.Input + u.Output + u.CacheRead + u.CacheCreation }

type line struct {
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype"`
	ParentToolUseID json.RawMessage `json:"parent_tool_use_id"`
	Event           json.RawMessage `json:"event"`
	Message         struct {
		ID      string          `json:"id"`
		Content json.RawMessage `json:"content"`
		Usage   *usage          `json:"usage"`
	} `json:"message"`
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
	Response  json.RawMessage `json:"response"`

	// system/init
	SessionID      string `json:"session_id"`
	Model          string `json:"model"`
	PermissionMode string `json:"permissionMode"`

	// assistant: the code of an API error message ("rate_limit", "server_error")
	Error json.RawMessage `json:"error"`

	// rate_limit_event
	RateLimit *rateLimitInfo `json:"rate_limit_info"`

	// system/compact_boundary
	CompactMeta *struct {
		Trigger    string `json:"trigger"`
		PreTokens  int64  `json:"pre_tokens"`
		PostTokens int64  `json:"post_tokens"`
	} `json:"compact_metadata"`

	// conversation_reset
	NewConversationID string `json:"new_conversation_id"`

	// result
	APIErrorStatus int     `json:"api_error_status"`
	IsError        bool    `json:"is_error"`
	Result       string  `json:"result"`
	StopReason   string  `json:"stop_reason"`
	TerminalReas string  `json:"terminal_reason"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	Usage        *usage  `json:"usage"`
	ModelUsage   map[string]struct {
		ContextWindow int64 `json:"contextWindow"`
	} `json:"modelUsage"`
}

func (l line) nested() bool {
	p := strings.TrimSpace(string(l.ParentToolUseID))
	return p != "" && p != "null"
}

// readLoop reads claude's lines, never holding more than maxLine of one. A line
// that long, or a read that fails, ends claude: it would otherwise sit blocked
// on a pipe nobody reads and the session would hang.
func (ss *session) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 64<<10)
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(buf)+len(chunk) > maxLine {
			ss.srv.logf("a line from claude was over %d bytes; ending the session", maxLine)
			ss.kill()
			return
		}
		buf = append(buf, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if len(strings.TrimSpace(string(buf))) > 0 {
			ss.mu.Lock()
			ss.lastLine = time.Now()
			ss.mu.Unlock()
			var l line
			if json.Unmarshal(buf, &l) == nil {
				ss.handleLine(l)
			}
		}
		if cap(buf) > 1<<20 {
			buf = nil
		} else {
			buf = buf[:0]
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				ss.srv.logf("reading from claude: %v; ending the session", err)
				ss.kill()
			}
			return
		}
	}
}

func (ss *session) handleLine(l line) {
	switch l.Type {
	case "system":
		switch l.Subtype {
		case "init":
			ss.handleInit(l)
		case "compact_boundary":
			ss.handleCompact(l)
		}
	case "rate_limit_event":
		if l.RateLimit != nil && l.RateLimit.Status == "rejected" {
			ss.mu.Lock()
			ss.facts.rejected, ss.facts.resetsAt = true, l.RateLimit.ResetsAt
			ss.mu.Unlock()
		}
	case "conversation_reset":
		// /clear: claude goes on under a new session id, which arrives in the
		// next init line (adopt). The context starts empty.
		ss.mu.Lock()
		ss.lastUsed = 0
		ss.mu.Unlock()
		ss.srv.update(ss.id, map[string]any{"sessionUpdate": "flockdeck_notice", "code": "new_conversation"})
	case "stream_event":
		ss.handleStream(l)
	case "assistant":
		ss.handleAssistant(l)
	case "user":
		ss.handleUser(l)
	case "result":
		ss.handleResult(l)
	case "control_response":
		var r struct {
			RequestID string `json:"request_id"`
		}
		_ = json.Unmarshal(l.Response, &r)
		ss.mu.Lock()
		ch := ss.waiters[r.RequestID]
		ss.mu.Unlock()
		if ch != nil {
			select {
			case ch <- l.Response:
			default:
			}
		}
	case "control_request":
		go ss.handleControlRequest(l)
	case "control_cancel_request":
		ss.mu.Lock()
		p := ss.perms[l.RequestID]
		ss.mu.Unlock()
		if p != nil {
			p.abandon()
		}
	}
}

// handleInit notes the model and mode claude reports at the start of a turn.
// A mode that changed behind our back (leaving plan mode after ExitPlanMode)
// is passed on.
func (ss *session) handleInit(l line) {
	ss.mu.Lock()
	if ss.cur == nil {
		// claude begins a turn that no prompt of ours started: a finished
		// background task. Prompts wait for its result.
		ss.busy = true
	}
	changed := l.PermissionMode != "" && l.PermissionMode != ss.mode && knownMode(l.PermissionMode)
	if changed {
		ss.mode = l.PermissionMode
	}
	ss.sawInit = true
	ss.mu.Unlock()
	ss.adopt(l.SessionID)
	if changed {
		ss.srv.update(ss.id, map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": l.PermissionMode})
	}
}

func textContent(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

func (ss *session) handleStream(l line) {
	if l.nested() {
		return
	}
	var ev struct {
		Type    string `json:"type"`
		Message struct {
			ID string `json:"id"`
		} `json:"message"`
		Delta struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
		} `json:"delta"`
	}
	if json.Unmarshal(l.Event, &ev) != nil {
		return
	}
	switch ev.Type {
	case "message_start":
		if ev.Message.ID != "" {
			ss.mu.Lock()
			ss.streamed[ev.Message.ID] = true
			ss.mu.Unlock()
		}
	case "content_block_delta":
		switch ev.Delta.Type {
		case "text_delta":
			if ev.Delta.Text != "" {
				ss.srv.update(ss.id, map[string]any{"sessionUpdate": "agent_message_chunk", "content": textContent(ev.Delta.Text)})
			}
		case "thinking_delta":
			if ev.Delta.Thinking != "" {
				ss.srv.update(ss.id, map[string]any{"sessionUpdate": "agent_thought_chunk", "content": textContent(ev.Delta.Thinking)})
			}
		}
	}
}

func (ss *session) handleAssistant(l line) {
	var blocks []block
	_ = json.Unmarshal(l.Message.Content, &blocks)
	ss.mu.Lock()
	streamed := ss.streamed[l.Message.ID]
	if !l.nested() && l.Message.Usage != nil {
		ss.lastUsed = l.Message.Usage.context()
	}
	if code := errString(l.Error); code != "" && !l.nested() {
		ss.facts.apiError = code
	}
	ss.mu.Unlock()
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if !streamed && !l.nested() && b.Text != "" {
				ss.srv.update(ss.id, map[string]any{"sessionUpdate": "agent_message_chunk", "content": textContent(b.Text)})
			}
		case "thinking":
			if !streamed && !l.nested() && b.Thinking != "" {
				ss.srv.update(ss.id, map[string]any{"sessionUpdate": "agent_thought_chunk", "content": textContent(b.Thinking)})
			}
		case "tool_use":
			ss.announce(b.ID, b.Name, b.Input, "")
		}
	}
}

// announce sends the tool_call for a tool use, once. title is claude's own
// label, used when the tool has none of ours.
func (ss *session) announce(id, name string, input json.RawMessage, display string) toolInfo {
	ss.mu.Lock()
	if info, ok := ss.announced[id]; ok {
		ss.mu.Unlock()
		return info
	}
	ss.mu.Unlock()
	d := describeTool(name, input, ss.cwd, display)
	info := toolInfo{name: name, kind: d.kind, edit: d.kind == "edit", hidden: d.plan != nil}
	ss.mu.Lock()
	ss.announced[id] = info
	ss.mu.Unlock()
	if d.plan != nil {
		ss.srv.update(ss.id, map[string]any{"sessionUpdate": "plan", "entries": d.plan})
		return info
	}
	u := map[string]any{"sessionUpdate": "tool_call", "toolCallId": id, "title": d.title, "kind": d.kind, "status": "pending"}
	if len(input) > 0 {
		u["rawInput"] = input
	}
	if len(d.locations) > 0 {
		u["locations"] = d.locations
	}
	if len(d.content) > 0 {
		u["content"] = d.content
	}
	ss.srv.update(ss.id, u)
	return info
}

func (ss *session) handleUser(l line) {
	var blocks []block
	if json.Unmarshal(l.Message.Content, &blocks) != nil {
		return // a plain string: a replayed prompt
	}
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		ss.mu.Lock()
		info, ok := ss.announced[b.ToolUseID]
		ss.mu.Unlock()
		if !ok || info.hidden {
			continue
		}
		status := "completed"
		if b.IsError {
			status = "failed"
		}
		u := map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": b.ToolUseID, "status": status}
		// An edit keeps the diff it was announced with; its result text only
		// says the file was updated.
		if text := resultText(b.Content); text != "" && !(info.edit && !b.IsError) {
			u["content"] = []any{map[string]any{"type": "content", "content": textContent(text)}}
		}
		ss.srv.update(ss.id, u)
	}
}

func (ss *session) handleResult(l line) {
	// usage first, so the update arrives before the prompt's answer
	ss.mu.Lock()
	used := ss.lastUsed
	if used == 0 && l.Usage != nil {
		used = l.Usage.context()
	}
	window := ss.window
	for _, mu := range l.ModelUsage {
		if mu.ContextWindow > 0 {
			window = mu.ContextWindow
		}
	}
	ss.window = window
	t := ss.cur
	cancelled := t != nil && t.cancelled
	facts := ss.facts
	ss.facts = turnFacts{}
	ss.busy = false
	ss.idle.Broadcast()
	ss.mu.Unlock()
	if window > 0 {
		u := map[string]any{"sessionUpdate": "usage_update", "used": used, "size": window}
		if l.TotalCostUSD > 0 {
			u["cost"] = map[string]any{"amount": l.TotalCostUSD, "currency": "USD"}
		}
		ss.srv.update(ss.id, u)
	}
	if t == nil {
		return // the end of a turn claude started on its own
	}
	stop, err := classify(l, facts, cancelled)
	if err != nil && err.Code != codeAuthRequired {
		ss.srv.logf("the turn failed (%v): %s", err.Data, clip(l.Result, 500))
	}
	select {
	case t.done <- turnResult{stop: stop, err: err}:
	default:
		ss.srv.logf("a second result for one turn was dropped")
	}
}

// classify turns a result line into an ACP stop reason or an error.
func classify(l line, f turnFacts, cancelled bool) (string, *rpcErr) {
	switch {
	case cancelled:
		return "cancelled", nil
	case l.IsError:
		return "", failure(l, f)
	case l.TerminalReas == "aborted_tools" || l.TerminalReas == "aborted_streaming":
		return "cancelled", nil
	case l.Subtype == "error_max_turns" || l.TerminalReas == "max_turns":
		return "max_turn_requests", nil
	case l.Subtype == "error_during_execution":
		return "", failure(l, f)
	case l.StopReason == "max_tokens":
		return "max_tokens", nil
	case l.StopReason == "refusal":
		return "refusal", nil
	}
	return "end_turn", nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// handleCompact tells the client claude compacted its context, for /compact and
// for an automatic compaction alike. The words are ours; only the two token
// counts are claude's, as numbers.
func (ss *session) handleCompact(l line) {
	if m := l.CompactMeta; m != nil && m.PostTokens >= 0 && m.PostTokens < 1<<40 {
		ss.mu.Lock()
		ss.lastUsed = m.PostTokens
		ss.mu.Unlock()
	}
	msg := "Context compacted."
	if m := l.CompactMeta; m != nil && m.PreTokens > 0 && m.PostTokens >= 0 && m.PreTokens < 1<<40 && m.PostTokens < 1<<40 {
		msg = fmt.Sprintf("Context compacted: %s to %s tokens.", groupDigits(m.PreTokens), groupDigits(m.PostTokens))
	}
	ss.srv.update(ss.id, map[string]any{"sessionUpdate": "compaction", "status": "completed", "message": msg})
}

func groupDigits(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

package claudeacp

import (
	"encoding/json"
)

const (
	optAllow       = "allow"
	optAllowAlways = "allow_always"
	optReject      = "reject"
)

// canAlways reports whether a tool may be allowed for the rest of the session.
// Only a shell command may: the rule is the command with all its input, so it
// covers that call and no other. A rule for an edit tool, WebFetch, Agent or an
// MCP tool would cover calls the person never saw (a path outside the project,
// .claude/settings.json, any URL, any prompt), so those cards offer Allow and
// Reject only. acceptEdits mode is the way to stop being asked about edits.
func canAlways(tool string) bool { return tool == "Bash" || tool == "PowerShell" }

// ruleKey is what an "allow always" answer is remembered by: the tool and its
// whole input except the description, in canonical form. A later call that
// differs in any other field (dangerouslyDisableSandbox, run_in_background,
// timeout) is a different call and is asked about.
func ruleKey(tool string, input json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return tool + "\x00" + string(input)
	}
	delete(m, "description")
	b, err := json.Marshal(m) // map keys are sorted
	if err != nil {
		return tool + "\x00" + string(input)
	}
	return tool + "\x00" + string(b)
}

func (ss *session) respond(requestID string, payload map[string]any) {
	_ = ss.write(map[string]any{"type": "control_response", "response": map[string]any{
		"subtype": "success", "request_id": requestID, "response": payload,
	}})
}

func (ss *session) respondError(requestID, msg string) {
	_ = ss.write(map[string]any{"type": "control_response", "response": map[string]any{
		"subtype": "error", "request_id": requestID, "error": msg,
	}})
}

func (ss *session) handleControlRequest(l line) {
	var req struct {
		Subtype     string          `json:"subtype"`
		ToolName    string          `json:"tool_name"`
		DisplayName string          `json:"display_name"`
		Input       json.RawMessage `json:"input"`
		ToolUseID   string          `json:"tool_use_id"`
	}
	_ = json.Unmarshal(l.Request, &req)
	if req.Subtype != "can_use_tool" {
		ss.respondError(l.RequestID, "not supported by this client: "+req.Subtype)
		return
	}
	if req.ToolName == "AskUserQuestion" {
		ss.respond(l.RequestID, map[string]any{"behavior": "deny",
			"message": "Questions are not supported in this client. Ask the user in plain text instead."})
		return
	}
	key := ruleKey(req.ToolName, req.Input)
	ss.mu.Lock()
	always := canAlways(req.ToolName) && ss.allowed[key]
	ss.mu.Unlock()
	toolID := req.ToolUseID
	if toolID == "" {
		toolID = l.RequestID
	}
	if always {
		ss.allow(l.RequestID, toolID, req.Input)
		return
	}

	// The assistant line normally announced the tool already; if the request
	// came first, announce from the request.
	ss.announce(toolID, req.ToolName, req.Input, req.DisplayName)
	d := describeTool(req.ToolName, req.Input, ss.cwd, req.DisplayName)
	call := map[string]any{"toolCallId": toolID, "title": d.title, "kind": d.kind, "status": "pending"}
	if len(req.Input) > 0 {
		call["rawInput"] = req.Input
	}
	if len(d.locations) > 0 {
		call["locations"] = d.locations
	}
	if len(d.content) > 0 {
		call["content"] = d.content
	}
	op := &openPerm{abandoned: make(chan struct{})}
	ss.mu.Lock()
	ss.perms[l.RequestID] = op
	ss.mu.Unlock()
	defer func() {
		ss.mu.Lock()
		delete(ss.perms, l.RequestID)
		ss.mu.Unlock()
	}()

	options := []any{map[string]any{"optionId": optAllow, "name": "Allow", "kind": "allow_once"}}
	if canAlways(req.ToolName) {
		options = append(options, map[string]any{"optionId": optAllowAlways, "name": "Always allow this command", "kind": "allow_always"})
	}
	options = append(options, map[string]any{"optionId": optReject, "name": "Reject", "kind": "reject_once"})
	id, ch := ss.srv.request("session/request_permission", map[string]any{
		"sessionId": ss.id,
		"toolCall":  call,
		"options":   options,
	})
	select {
	case m := <-ch:
		// A response that raced a cancel or a retraction is dropped, not sent:
		// both channels can be ready at once.
		select {
		case <-op.abandoned:
			return
		default:
		}
		var out struct {
			Outcome struct {
				Outcome  string `json:"outcome"`
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		}
		_ = json.Unmarshal(m.Result, &out)
		switch {
		case m.Error != nil || out.Outcome.Outcome != "selected":
			ss.respond(l.RequestID, map[string]any{"behavior": "deny", "message": "The permission request was cancelled."})
		case out.Outcome.OptionID == optAllow:
			ss.allow(l.RequestID, toolID, req.Input)
		case out.Outcome.OptionID == optAllowAlways && canAlways(req.ToolName):
			ss.mu.Lock()
			ss.allowed[key] = true
			ss.mu.Unlock()
			ss.allow(l.RequestID, toolID, req.Input)
		default: // reject, or an option that was not offered
			ss.respond(l.RequestID, map[string]any{"behavior": "deny", "message": "The user rejected this tool call."})
		}
	case <-op.abandoned:
		// claude retracted the request, or the turn was cancelled: it needs no
		// answer, and the client has answered cancelled itself.
		ss.srv.forget(id)
	case <-ss.dead:
		ss.srv.forget(id)
	}
}

func (ss *session) allow(requestID, toolID string, input json.RawMessage) {
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	ss.respond(requestID, map[string]any{"behavior": "allow", "updatedInput": input})
	ss.mu.Lock()
	info, ok := ss.announced[toolID]
	ss.mu.Unlock()
	if ok && !info.hidden {
		ss.srv.update(ss.id, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": toolID, "status": "in_progress"})
	}
}

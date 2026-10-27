package claudeacp

import (
	"encoding/json"
	"strings"
)

const (
	optAllow       = "allow"
	optAllowAlways = "allow_always"
	optReject      = "reject"
)

// ruleKey is what an "allow always" answer is remembered by. A shell command
// is remembered exactly, so that one approval does not cover other commands.
// Every other tool is remembered by name: allowing Edit always means every edit
// of the session, as acceptEdits would.
func ruleKey(tool string, input json.RawMessage) string {
	if tool == "Bash" || tool == "PowerShell" {
		var in struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(input, &in)
		return tool + "\x00" + strings.TrimSpace(in.Command)
	}
	return tool
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
	always := ss.allowed[key]
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

	always1 := "Always allow " + req.ToolName
	if req.ToolName == "Bash" || req.ToolName == "PowerShell" {
		always1 = "Always allow this command"
	}
	id, ch := ss.srv.request("session/request_permission", map[string]any{
		"sessionId": ss.id,
		"toolCall":  call,
		"options": []any{
			map[string]any{"optionId": optAllow, "name": "Allow", "kind": "allow_once"},
			map[string]any{"optionId": optAllowAlways, "name": always1, "kind": "allow_always"},
			map[string]any{"optionId": optReject, "name": "Reject", "kind": "reject_once"},
		},
	})
	select {
	case m := <-ch:
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
		case out.Outcome.OptionID == optAllowAlways:
			ss.mu.Lock()
			ss.allowed[key] = true
			ss.mu.Unlock()
			ss.allow(l.RequestID, toolID, req.Input)
		default:
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

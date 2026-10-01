// Package review is the reviewer behind auto-review approvals: given the same
// detail a permission prompt would show a person (see server/waiting.go's
// permissionView), it decides whether a PreToolUse call is safe enough to let
// through without waking anyone, the way Codex's auto_review spares a person
// the interruption for a command a reviewer is confident about.
//
// It is deliberately one-sided. Deciding wrongly that something dangerous is
// safe is a real harm; deciding wrongly that something safe needs a person is
// only the interruption auto-review exists to spare them, which they still
// get. So Decide only ever answers Allow or asks — it never answers Deny, and
// never touches Claude Code's own permission settings: it only ever resolves
// the one request in front of it, and only by letting through exactly what
// was already going to be asked about. A pane with auto-review off, or a
// request Decide is not sure of, is answered exactly as it always has been.
package review

import "encoding/json"

// Decision is what Decide answers about one PreToolUse call.
type Decision struct {
	// Allow means the request is safe enough to let through without asking.
	Allow bool
	// Reason is shown to the agent as the permission decision's reason when
	// Allow is true, and is empty otherwise -- there is nothing to explain
	// about falling through to the ordinary prompt.
	Reason string
}

// ask is what every request Decide is not sure of gets: the fallback that
// leaves it answered exactly as it always was.
var ask = Decision{}

// maxCommandBytes is the longest command Decide will judge at all. The hook
// that carries a tool_input here clips each field (hooks.buildToolInput), and
// Claude Code runs the command it holds, not the clipped one: a command whose
// first 64 KiB read as a harmless cat was let through with whatever followed
// the cut never seen. No read-only command anyone types comes near this, so
// anything longer is simply asked about -- well short of any clip, so that a
// command cut down anywhere on its way here can never be judged by its prefix.
const maxCommandBytes = 4 << 10

// Decide reviews one PreToolUse call, given its tool name, the same compact
// tool_input JSON hooks.buildToolInput already produces (Event.ToolInput,
// carried on Session.ToolInput), and the directory the agent is working in
// (Event.Cwd), which is where a Bash command runs. Only Bash is judged at all:
// Edit, Write and MultiEdit change files on disk, which is exactly the kind
// of request a person is meant to see before it happens, and every other
// tool -- an MCP call included -- is unknown territory Decide cannot reason
// about.
func Decide(tool, toolInputJSON, cwd string) Decision {
	if tool != "Bash" || toolInputJSON == "" {
		return ask
	}
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal([]byte(toolInputJSON), &in) != nil || in.Command == "" {
		return ask
	}
	if len(in.Command) > maxCommandBytes {
		return ask
	}
	ok, gitSub := readOnlyCommand(in.Command)
	if !ok {
		return ask
	}
	if gitSub != "" && !gitRunsNothing(cwd, touchesWorkTree[gitSub]) {
		return ask
	}
	return Decision{Allow: true, Reason: "auto-review: a read-only command"}
}

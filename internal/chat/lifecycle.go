package chat

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/jmwri/perch/internal/hooks"
)

// The lifecycle events perch chat reports. They are Claude Code's own event
// names, sent to the same endpoint, because the workspace already knows what
// every one of them means for a pane's status: a chat pane goes amber when it
// asks a question and grey when it finishes for exactly the same reasons a
// Claude pane does, and nothing had to learn a second protocol to make that
// true.
const (
	eventSessionStart = "SessionStart"
	eventUserPrompt   = "UserPromptSubmit"
	eventPreTool      = "PreToolUse"
	eventPostTool     = "PostToolUse"
	eventNotification = "Notification"
	eventStop         = "Stop"
	eventSessionEnd   = "SessionEnd"
)

// reporter tells Perch what this pane is doing.
type reporter struct {
	endpoint string
	token    string
	session  string
	cwd      string
	// emit is the client half, kept as a field so a test can watch what would
	// have been sent without a server to send it to.
	emit func(stdin io.Reader, endpoint, token, session, event string) (string, error)
}

func newReporter(api, token, session, cwd string) *reporter {
	endpoint := strings.TrimRight(api, "/")
	if endpoint != "" && !strings.HasSuffix(endpoint, "/hook") {
		endpoint += "/hook"
	}
	return &reporter{endpoint: endpoint, token: token, session: session, cwd: cwd, emit: hooks.Emit}
}

// hookInput is the JSON a lifecycle hook is fed on its standard input.
//
// Going through the same door as a command hook rather than posting the event
// ourselves is deliberate: the client half already knows the payload, the
// token, the timeout and what to do about a refusal, and the one place the
// protocol is written down stays the one place. The fields are the ones Claude
// Code puts there, which is what the reading end parses.
type hookInput struct {
	SessionID string `json:"session_id"`
	ToolName  string `json:"tool_name,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	Prompt    string `json:"prompt,omitempty"`
	Source    string `json:"source,omitempty"`
}

// send reports one event and returns whatever came back, which for a
// SessionStart is the pane's briefing.
//
// A failure is swallowed. Perch may not be listening at all -- somebody can run
// `perch chat` in a plain terminal -- and a chat that stopped working because
// its status could not be reported would be a poor trade for a coloured dot.
func (r *reporter) send(event string, in hookInput) string {
	if r == nil || r.endpoint == "" || r.session == "" {
		return ""
	}
	in.SessionID = r.session
	if in.Cwd == "" {
		in.Cwd = r.cwd
	}
	data, err := json.Marshal(in)
	if err != nil {
		return ""
	}
	out, err := r.emit(bytes.NewReader(data), r.endpoint, r.token, r.session, event)
	if err != nil {
		return ""
	}
	return out
}

// sessionStart announces the pane and returns the briefing Perch answers with:
// which pane this is, who else is working, what the pane can do. It is asked
// again after a `/clear`, so the description survives a conversation being
// started over -- the same reason Claude Code's own hook fires again after a
// compaction.
func (r *reporter) sessionStart(source string) string {
	return r.send(eventSessionStart, hookInput{Source: source})
}

func (r *reporter) userPrompt(prompt string) { r.send(eventUserPrompt, hookInput{Prompt: prompt}) }
func (r *reporter) preTool(tool string)      { r.send(eventPreTool, hookInput{ToolName: tool}) }
func (r *reporter) postTool(tool string)     { r.send(eventPostTool, hookInput{ToolName: tool}) }
func (r *reporter) stop()                    { r.send(eventStop, hookInput{}) }
func (r *reporter) sessionEnd()              { r.send(eventSessionEnd, hookInput{}) }

// notification is what turns the pane amber and tells the user which pane wants
// them. It is sent when the chat is waiting on an answer it cannot go on
// without -- a tool asking permission -- which is the whole point of Perch:
// twelve panes and one person, who needs to be told which one to look at.
func (r *reporter) notification(tool string) { r.send(eventNotification, hookInput{ToolName: tool}) }

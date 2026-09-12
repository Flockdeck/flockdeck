package chat

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/pricing"
	spending "github.com/jmwri/flockdeck/internal/spend"
)

// The lifecycle events flockdeck chat reports. They are Claude Code's own event
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

// reporter tells Flockdeck what this pane is doing.
type reporter struct {
	endpoint string
	token    string
	session  string
	cwd      string
	// emit is the client half, kept as a field so a test can watch what would
	// have been sent without a server to send it to.
	emit func(stdin io.Reader, endpoint, token, session, event string) (string, error)
	// usageEndpoint is where each call's spending goes, beside the lifecycle
	// events, and report the client half that sends it.
	usageEndpoint string
	report        func(endpoint, token string, r spending.Report, timeout time.Duration) error
}

func newReporter(api, token, session, cwd string) *reporter {
	endpoint := strings.TrimRight(api, "/")
	if endpoint != "" && !strings.HasSuffix(endpoint, "/hook") {
		endpoint += "/hook"
	}
	var usage string
	if base := strings.TrimSuffix(endpoint, "/hook"); base != "" {
		usage = base + "/usage"
	}
	return &reporter{endpoint: endpoint, token: token, session: session, cwd: cwd, emit: hooks.Emit,
		usageEndpoint: usage, report: hooks.Report}
}

// usageTimeout bounds one usage report. It is sent between a call's answer and
// whatever comes next, over loopback, so a second is far more than it takes
// and little enough that a Flockdeck which has stopped answering costs a turn
// no more than that.
const usageTimeout = time.Second

// usage reports what one call read and wrote, and what that cost where the
// model has a price here. It is sent a call at a time, not a turn at a time,
// because a turn that uses tools makes many calls and the pane header should
// move with each of them.
//
// Like every other report it is swallowed when it fails: a figure lost is a
// figure lost, and never the turn.
func (r *reporter) usage(model string, u Usage) {
	if r == nil || r.usageEndpoint == "" || r.session == "" || r.report == nil || u == (Usage{}) {
		return
	}
	rep := spending.Report{
		Pane:  r.session,
		Model: model,
		Tokens: spending.Tokens{
			In: int64(u.In), Out: int64(u.Out),
			CacheRead: int64(u.CacheRead), CacheWrite5m: int64(u.CacheWrite),
		},
	}
	// The rate and the day it was read come from the one price table
	// together, so the pane header says how old this very price is rather
	// than a date kept beside it that could drift from it.
	if rate, ok := pricing.Lookup(model, time.Now()); ok {
		usd := rate.Cost(pricing.Usage{In: u.In, Out: u.Out, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite})
		rep.Cost = spending.Cost{USD: usd, Known: true, Source: "table", Checked: rate.Checked}
	}
	_ = r.report(r.usageEndpoint, r.token, rep, usageTimeout)
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
// A failure is swallowed. Flockdeck may not be listening at all -- somebody can run
// `flockdeck chat` in a plain terminal -- and a chat that stopped working because
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

// sessionStart announces the pane and returns the briefing Flockdeck answers with:
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
// without -- a tool asking permission -- which is the whole point of Flockdeck:
// twelve panes and one person, who needs to be told which one to look at.
func (r *reporter) notification(tool string) { r.send(eventNotification, hookInput{ToolName: tool}) }

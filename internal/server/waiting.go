package server

import (
	"encoding/json"
	"path/filepath"

	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// This file turns a waiting pane's tool and ToolInput (internal/hooks.Event,
// carried on internal/session.Session -- see SetStatusFull) into what the
// phone's chat view draws: the question AskUserQuestion is asking, or what a
// permission prompt for any other tool wants to do. Documented in the
// scratchpad's convo-protocol.md as fields on the state push's pane view,
// since neither is ever written to a transcript for the conversation stream
// to carry (internal/server/conversation.go) to pick up.

// askOptionView and askQuestionView are AskUserQuestion's own input, sent on
// so the phone can draw the question itself -- header, text, every option's
// description, whether it takes several -- rather than only the buttons
// choices.js reads off the rendered screen.
type askOptionView struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type askQuestionView struct {
	Header      string          `json:"header,omitempty"`
	Question    string          `json:"question"`
	MultiSelect bool            `json:"multiSelect,omitempty"`
	Options     []askOptionView `json:"options"`
}

// askView is one AskUserQuestion call, which may ask several questions at
// once (Claude Code draws these as a strip of tabs, one per header).
type askView struct {
	Questions []askQuestionView `json:"questions"`
}

// permissionView is what a permission prompt for a tool other than
// AskUserQuestion is asking: the command for Bash, the file (and its diff,
// built from the call's own before/after text -- there is no need to wait for
// the tool to run) for Edit, MultiEdit or Write. Anything else names just the
// tool, which is all a permission prompt for an MCP tool or a future builtin
// can say without guessing at its shape.
type permissionView struct {
	Tool        string `json:"tool"`
	Summary     string `json:"summary"`
	Command     string `json:"command,omitempty"`
	Description string `json:"description,omitempty"`
	File        string `json:"file,omitempty"`
	Diff        string `json:"diff,omitempty"`
	// Options are the choices the agent's prompt offers that Flockdeck can name
	// without reading the screen: see permissionOptions. Empty, never left out,
	// where it cannot, and a window then keeps to the terminal path.
	Options []permissionOption `json:"options"`
	// Redacted says this card was made fit for a window reached through the
	// relay: the command, description, file and diff are left out and Options is
	// empty, so the card is not answered from there. Summary says what kind of
	// thing was asked and that the rest is on the desktop (relayscrub.go).
	Redacted bool `json:"redacted,omitempty"`
}

// permissionOption is one choice of a permission prompt; see
// workspace.PermissionOption. The kind uses the names of the Agent Client
// Protocol and is the contract for structured panes; the id is what the
// terminal path sends.
type permissionOption = workspace.PermissionOption

// The kinds of a permission option. Terminal panes use only the first and the
// third: allow_always and reject_always are for structured panes.
const (
	optionAllowOnce    = workspace.OptionAllowOnce
	optionAllowAlways  = workspace.OptionAllowAlways
	optionRejectOnce   = workspace.OptionRejectOnce
	optionRejectAlways = workspace.OptionRejectAlways
)

// permissionOptionKinds lists every kind an option can have.
func permissionOptionKinds() []string {
	return []string{optionAllowOnce, optionAllowAlways, optionRejectOnce, optionRejectAlways}
}

// permissionOptions are the choices of a terminal pane's permission prompt for
// agent and tool. A pane says its own through its runtime, and a window's
// view does not call this; it stays for the terminal rule's tests.
func permissionOptions(agent, tool string) []permissionOption {
	return workspace.TerminalPermissionOptions(agent, tool)
}

// The ids of a terminal permission prompt's options.
const (
	permissionYes = workspace.PermissionYes
	permissionNo  = workspace.PermissionNo
)

// permissionOptionIDs lists every id an option can have.
func permissionOptionIDs() []string { return []string{permissionYes, permissionNo} }

// maxPermissionDiff bounds the diff a permission view carries inline. There is
// no on-demand detail fetch for a permission prompt the way a finished tool
// row has one -- it is gone the moment the prompt is answered -- so this is
// simply cut short rather than swapped for a summary.
const maxPermissionDiff = 64 << 10

// waitingToolInput is the shape hooks.buildToolInput marshals Event.ToolInput
// to: a small, already-capped subset of a PreToolUse call's tool_input,
// carried on the session as an opaque string (Session.ToolInput) until here,
// the one place that knows what a phone-facing card looks like.
type waitingToolInput struct {
	Command     string                 `json:"command"`
	Description string                 `json:"description"`
	FilePath    string                 `json:"filePath"`
	OldString   string                 `json:"oldString"`
	NewString   string                 `json:"newString"`
	Content     string                 `json:"content"`
	Edits       []waitingToolInputEdit `json:"edits"`
	Questions   []waitingToolInputQAsk `json:"questions"`
}

type waitingToolInputEdit struct {
	OldString string `json:"oldString"`
	NewString string `json:"newString"`
}

type waitingToolInputQAsk struct {
	Header      string                    `json:"header"`
	Question    string                    `json:"question"`
	MultiSelect bool                      `json:"multiSelect"`
	Options     []waitingToolInputQOption `json:"options"`
}

type waitingToolInputQOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// waitingViews builds what a waiting pane's tool and ToolInput say to a
// phone. Both return values are nil for a pane not waiting on either an
// AskUserQuestion call or a permission prompt this can describe -- an idle
// nudge, say, which names no tool and carries no input at all.
func waitingViews(tool, toolInputJSON string) (*askView, *permissionView) {
	if tool == "" || toolInputJSON == "" {
		return nil, nil
	}
	var in waitingToolInput
	if json.Unmarshal([]byte(toolInputJSON), &in) != nil {
		return nil, nil
	}
	if tool == "AskUserQuestion" {
		return askViewFor(in), nil
	}
	return nil, permissionViewFor(tool, in)
}

func askViewFor(in waitingToolInput) *askView {
	if len(in.Questions) == 0 {
		return nil
	}
	av := &askView{}
	for _, q := range in.Questions {
		qv := askQuestionView{Header: q.Header, Question: q.Question, MultiSelect: q.MultiSelect}
		for _, o := range q.Options {
			qv.Options = append(qv.Options, askOptionView{Label: o.Label, Description: o.Description})
		}
		av.Questions = append(av.Questions, qv)
	}
	return av
}

func permissionViewFor(tool string, in waitingToolInput) *permissionView {
	pv := &permissionView{Tool: tool, Options: []permissionOption{}}
	switch tool {
	case "Bash":
		pv.Command = in.Command
		pv.Description = in.Description
		pv.Summary = "Run: " + in.Command
	case "Edit":
		pv.File = in.FilePath
		pv.Summary = "Edit " + filepath.Base(in.FilePath)
		pv.Diff = clipDiff(transcript.EditDiff(in.FilePath, in.OldString, in.NewString))
	case "MultiEdit":
		pairs := make([]transcript.EditPair, len(in.Edits))
		for i, e := range in.Edits {
			pairs[i] = transcript.EditPair{OldString: e.OldString, NewString: e.NewString}
		}
		pv.File = in.FilePath
		pv.Summary = "Edit " + filepath.Base(in.FilePath)
		pv.Diff = clipDiff(transcript.MultiEditDiff(in.FilePath, pairs))
	case "Write":
		pv.File = in.FilePath
		pv.Summary = "Write " + filepath.Base(in.FilePath)
		pv.Diff = clipDiff(transcript.WriteDiff(in.FilePath, in.Content))
	default:
		pv.Summary = "Use " + tool
	}
	return pv
}

// clipDiff bounds a permission's inline diff at maxPermissionDiff, on a rune
// boundary so truncating it never produces invalid UTF-8.
func clipDiff(s string) string {
	if len(s) <= maxPermissionDiff {
		return s
	}
	n := maxPermissionDiff
	for n > 0 && !isRuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// isRuneStart reports whether b is not a UTF-8 continuation byte.
func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

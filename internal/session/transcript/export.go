package transcript

import (
	"errors"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
)

// ErrNoTranscript says an agent has no stored conversation to export: either
// it keeps none Flockdeck can read, or the one it was asked about is not on
// this machine (or is empty).
var ErrNoTranscript = errors.New("no stored conversation to export")

// ErrReplaced is what a Follower returns, with no events, when the stored
// conversation was replaced by one that is shorter than what had been read of it
// (rewritten, or deleted and started again). The Follower has started over, so
// the next Poll gives the conversation from its beginning, and whoever has been
// writing a transcript from it has to write that again from the start.
var ErrReplaced = errors.New("the stored conversation was replaced")

// ErrRead wraps the error of a Follower that could not read what is stored,
// as opposed to one that could not find it: a file another program has locked
// for a moment, say. It is worth trying again later, and nothing has been lost:
// the Follower has not moved past what it could not read.
var ErrRead = errors.New("could not read the stored conversation")

// ExportKind is what an ExportEvent is.
type ExportKind int

const (
	// ExportPrompt is something the person typed.
	ExportPrompt ExportKind = iota
	// ExportMessage is something the agent said.
	ExportMessage
	// ExportToolCall is the agent starting a tool.
	ExportToolCall
	// ExportToolResult is what a tool gave back.
	ExportToolResult
	// ExportTitle is the conversation being given a title, or a new one.
	ExportTitle
	// ExportCompact is earlier history being summarised to make room.
	ExportCompact
)

// ExportEvent is one thing that happened in a stored conversation, in the
// agent-neutral shape the exporter in internal/record turns into a transcript.
type ExportEvent struct {
	Time time.Time
	Kind ExportKind
	// Text is the prompt or the message, or the conversation's title for an
	// ExportTitle.
	Text      string
	Tool      string
	ToolUseID string
	// Model is the model that produced an assistant turn (a message, or a tool
	// call it made), as the stored conversation records it for that turn. Empty
	// where it records none or only a placeholder, and always empty for a prompt
	// or a tool result. It is never the pane's selected model, which is not the
	// conversation's.
	Model string
	// Usage is what the model's reply cost in tokens, on the first event of
	// each reply only and so that adding it up over a conversation counts each
	// reply once: Claude Code writes one entry per content block of a reply, each
	// repeating the reply's usage. Nil everywhere else, and where none is stored.
	Usage *ExportUsage
	// StopReason is why the reply ended ("end_turn", "tool_use", ...), on the
	// first event of the reply the stored conversation gives one for, once.
	StopReason string
	// GitBranch, Cwd and AgentVersion are the branch, the working directory and
	// the agent's own version the stored entry that gave this event records.
	// Empty where it records none, and for a title.
	GitBranch, Cwd, AgentVersion string
	// Trigger, TokensBefore and TokensAfter describe an ExportCompact: what
	// started it ("auto" or "manual") and the size of the conversation, in
	// tokens, before and after. Zero where the stored conversation says nothing.
	Trigger                   string
	TokensBefore, TokensAfter int
	// Input is a tool call's input, decoded from JSON.
	Input       any
	Output      string
	IsError     bool
	Interrupted bool
}

// ExportUsage is the token counts of one model reply.
type ExportUsage struct {
	InputTokens, OutputTokens, CacheCreationInputTokens, CacheReadInputTokens int
}

// ExportStats says how a read of a conversation went.
type ExportStats struct {
	// Skipped counts entries that could not be read: lines that were not JSON,
	// and entries too large to hold in memory.
	Skipped int
}

// Follower reads a stored conversation as it grows: each Poll gives the events
// written since the last. It is how a transcript is made, whether the
// conversation is over and the transcript is exported, or still going and it is
// being recorded; both go through the same Follower, which is what makes the
// two the same.
type Follower interface {
	// Poll calls yield for each event written since the last Poll, in order,
	// and stops at the first error yield returns. It returns ErrNoTranscript
	// while there is nothing stored under the conversation's id, and ErrReplaced
	// if what is stored is no longer what had been read.
	Poll(yield func(ExportEvent) error) (ExportStats, error)
}

// Exporter is a Reader whose stored conversations can be followed. An agent
// whose record nobody has written this for is not an Exporter, has no
// transcript, and cannot be exported or recorded.
type Exporter interface {
	// Follow returns a Follower for a conversation, from its beginning. The
	// conversation need not be stored yet.
	Follow(spec agent.Spec, sessionID string) Follower
	// Cwd is the working directory the stored conversation records, or "" if
	// it is not stored or does not say.
	Cwd(spec agent.Spec, sessionID string) string
}

// ExporterFor returns the exporter for an agent, and false for one whose
// stored conversations cannot be followed.
func ExporterFor(spec agent.Spec) (Exporter, bool) {
	e, ok := For(spec).(Exporter)
	return e, ok
}

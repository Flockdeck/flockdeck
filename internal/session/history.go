package session

import (
	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// Reading a transcript now belongs to internal/session/transcript, behind a
// Reader chosen from the agent's Spec. What is left here are the entry points
// that predate a pane being able to run anything but Claude Code: every one of
// their callers is still asking about a Claude pane, so they go on answering
// about one, and the callers can be moved to a Spec of their own one at a time
// rather than all at once.

// claudeSpec is enough of the catalog's Claude entry to choose its reader.
// The reader itself takes nothing else from a Spec -- where Claude Code keeps
// its transcripts is Claude Code's arrangement, not Flockdeck's.
var claudeSpec = agent.Spec{ID: "claude", Caps: agent.Caps{Transcript: true, Resume: true}}

// Conversation is a stored conversation that can be resumed.
type Conversation = transcript.Conversation

// NoPrompt stands in for the summary of a conversation that says nothing
// about itself.
const NoPrompt = transcript.NoPrompt

// Conversations lists the stored Claude Code conversations for a working
// directory, most recently used first.
func Conversations(cwd string) ([]Conversation, error) {
	return transcript.For(claudeSpec).Conversations(claudeSpec, cwd)
}

package session

import "github.com/jmwri/flockdeck/internal/session/transcript"

// RecentReplies returns what a pane's agent said in its last few turns, newest
// first and at most maxTurns of them: Claude Code's, or for an API pane
// Flockdeck's own chat client's.
//
// This is the text the agent actually wrote, read from its own transcript.
// Reading it off the terminal instead means reading a redrawn interface:
// bullets wrapped to the pane's width and so cut mid-sentence, spinner frames
// and token counters sitting exactly where a list item would be, and thinking
// the agent was only musing with. A plan is markdown, and the transcript is
// where the markdown is.
func RecentReplies(sessionID string, maxTurns int) []string {
	if said := transcript.For(claudeSpec).Replies(claudeSpec, sessionID, maxTurns); len(said) > 0 {
		return said
	}
	// A pane's id is its conversation's, so an API pane's plan is in the chat
	// client's folder under the same id. Only Claude Code's store was asked,
	// and a fan-out from an API pane read its plan off the screen instead.
	return transcript.For(chatSpec).Replies(chatSpec, sessionID, maxTurns)
}

// TranscriptPath returns the file Claude Code keeps a session's conversation
// in, or "" when there is none.
func TranscriptPath(sessionID string) string {
	return transcript.For(claudeSpec).Path(claudeSpec, sessionID)
}

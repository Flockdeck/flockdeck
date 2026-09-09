package session

import "github.com/jmwri/perch/internal/session/transcript"

// RecentReplies returns what a Claude pane's agent said in its last few turns,
// newest first and at most maxTurns of them.
//
// This is the text the agent actually wrote, read from its own transcript.
// Reading it off the terminal instead means reading a redrawn interface:
// bullets wrapped to the pane's width and so cut mid-sentence, spinner frames
// and token counters sitting exactly where a list item would be, and thinking
// the agent was only musing with. A plan is markdown, and the transcript is
// where the markdown is.
func RecentReplies(sessionID string, maxTurns int) []string {
	return transcript.For(claudeSpec).Replies(claudeSpec, sessionID, maxTurns)
}

// TranscriptPath returns the file Claude Code keeps a session's conversation
// in, or "" when there is none.
func TranscriptPath(sessionID string) string {
	return transcript.For(claudeSpec).Path(claudeSpec, sessionID)
}

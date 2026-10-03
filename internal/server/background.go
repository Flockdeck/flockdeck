package server

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/session"
)

// backgroundView is one piece of an agent's background work as the window
// and the phone list it: what Claude Code said of it, if anything, and when
// Flockdeck first heard of it. The hook has already redacted and shortened
// every string (hooks.BackgroundInfo); they are cut again here only so that
// what reaches a view is bounded whatever sent it.
type backgroundView struct {
	// ID is Claude Code's own id for the work, without the kind.
	ID string `json:"id"`
	// Kind is Claude Code's name for the kind of work -- "shell",
	// "subagent", "monitor", "workflow" -- or "" when nothing has said.
	Kind        string `json:"kind,omitempty"`
	AgentType   string `json:"agentType,omitempty"`
	Description string `json:"description,omitempty"`
	Command     string `json:"command,omitempty"`
	// Since is when Flockdeck first heard of it, RFC 3339.
	Since string `json:"since"`
	// Listed says it was first heard of in the list Claude Code sends when a
	// turn ends, not from its start, so Since is when it was first seen
	// running rather than when it started.
	Listed bool `json:"listed,omitempty"`
}

// maxBackgroundShown is how many pieces of background work a view lists; the
// count says how many there are in all.
const maxBackgroundShown = 8

// maxBackgroundViewText bounds each string of a backgroundView.
const maxBackgroundViewText = 200

// backgroundViews is work as views, at most maxBackgroundShown of them, or nil
// when there is none.
func backgroundViews(work []session.BackgroundWork) []backgroundView {
	if len(work) == 0 {
		return nil
	}
	if len(work) > maxBackgroundShown {
		work = work[:maxBackgroundShown]
	}
	out := make([]backgroundView, 0, len(work))
	for _, w := range work {
		kind, id, ok := strings.Cut(w.ID, ":")
		if !ok {
			kind, id = "", w.ID
		}
		// The kind a start was counted under, where nothing else said one:
		// "task:" is an end's, naming none.
		if w.Type != "" {
			kind = w.Type
		} else if kind == "agent" {
			kind = "subagent"
		} else if kind == "task" {
			kind = ""
		}
		out = append(out, backgroundView{
			ID:          cutText(id),
			Kind:        cutText(kind),
			AgentType:   cutText(w.AgentType),
			Description: cutText(w.Description),
			Command:     cutText(w.Command),
			Since:       w.Since.Format(time.RFC3339),
			Listed:      w.Listed,
		})
	}
	return out
}

// cutText is s cut to maxBackgroundViewText bytes on a rune boundary, with an
// ellipsis where it was cut.
func cutText(s string) string {
	if len(s) <= maxBackgroundViewText {
		return s
	}
	n := maxBackgroundViewText - len("…")
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

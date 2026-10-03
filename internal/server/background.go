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
	// Confirmed is when something last showed it running, RFC 3339, and
	// ConfirmedBy what that was. Unverified says that was longer ago than
	// session.BackgroundUnverifiedGrace.
	Confirmed   string `json:"confirmed,omitempty"`
	ConfirmedBy string `json:"confirmedBy,omitempty"`
	Unverified  bool   `json:"unverified,omitempty"`
	// Evidence and Ended are set only on a backgroundEnded entry: what showed
	// the work had ended, and when, RFC 3339.
	Evidence string `json:"evidence,omitempty"`
	Ended    string `json:"ended,omitempty"`
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
			Confirmed:   timeOrEmpty(w.Confirmed),
			ConfirmedBy: cutText(w.ConfirmedBy),
			Unverified:  w.Unverified,
		})
	}
	return out
}

// backgroundEndedViews is the work lately ended, as backgroundViews are, with
// what showed it had ended; nil when there is none.
func backgroundEndedViews(ended []session.BackgroundEnded) []backgroundView {
	if len(ended) == 0 {
		return nil
	}
	if len(ended) > maxBackgroundShown {
		ended = ended[len(ended)-maxBackgroundShown:]
	}
	work := make([]session.BackgroundWork, len(ended))
	for i, e := range ended {
		work[i] = e.BackgroundWork
	}
	out := backgroundViews(work)
	for i, e := range ended {
		out[i].Unverified, out[i].Confirmed, out[i].ConfirmedBy = false, "", ""
		out[i].Evidence = cutText(e.Evidence)
		out[i].Ended = timeOrEmpty(e.At)
	}
	return out
}

// timeOrEmpty is t in RFC 3339, or "" for the zero time.
func timeOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
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

// backgroundCheckTick is how often the workspace is asked which panes'
// background work is due a check (workspace.DueBackgroundChecks); each pane
// keeps its own, longer interval. A variable so a test need not wait it out.
var backgroundCheckTick = 15 * time.Second

// backgroundLoop has idle agents' background work checked against their
// stored conversations, so a count stuck on work that ended where no hook saw
// it corrects itself. Which panes are due is worked out on the workspace
// goroutine; the reading is done off it, one pane after another.
func (s *Server) backgroundLoop() {
	tick := time.NewTicker(backgroundCheckTick)
	defer tick.Stop()
	for {
		select {
		case <-s.closed:
			return
		case <-tick.C:
			s.do(func() {
				jobs := s.ws.DueBackgroundChecks(time.Now())
				if len(jobs) == 0 {
					return
				}
				go func() {
					for _, j := range jobs {
						s.ws.RunBackgroundCheck(j, time.Now())
					}
				}()
			})
		}
	}
}

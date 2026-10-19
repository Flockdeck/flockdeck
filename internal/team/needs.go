package team

import (
	"slices"
	"strconv"
	"strings"
	"time"
)

// Kinds of needs-you item, in the order they are listed after the prompts.
const (
	NeedPermission  = "permission"
	NeedAsk         = "ask"
	NeedReturned    = "returned"
	NeedConflict    = "conflict"
	NeedDown        = "down"
	NeedStaleUnread = "stale-unread"
)

const (
	// MaxNeeds is the most items kept for one team.
	MaxNeeds = 50
	// MaxConflictPaths is the most paths a conflict item names.
	MaxConflictPaths = 5
	// ReturnedWindow is how long after a task goes back from review that it
	// still counts as returned.
	ReturnedWindow = 24 * time.Hour
	// StaleUnreadAfter is how long unread mail may wait in an idle pane before
	// it is listed.
	StaleUnreadAfter = 5 * time.Minute
)

// PaneConflict is a pair of panes the radar reports as conflicting. The caller
// maps the radar's checkouts to panes.
type PaneConflict struct {
	A, B  string
	Paths []string
	// Since is when the radar first reported the pair, zero if unknown.
	Since time.Time
}

// TaskMove is one change of a task's state from the board's history.
type TaskMove struct {
	Task string
	From string
	To   string
	Time time.Time
	Note string
}

// NeedsItem is one thing that wants the user.
type NeedsItem struct {
	// ID stays the same across calls while the item stands, so a window can keep
	// its selection. It is made from the kind and the pane or task.
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Pane string `json:"pane"`
	// Other is the second pane of a conflict.
	Other string    `json:"other,omitempty"`
	Task  string    `json:"task,omitempty"`
	Time  time.Time `json:"time"`
	Text  string    `json:"text,omitempty"`
}

// NeedsYou lists, for each team, what needs the user, most urgent first. The
// map is keyed by team id; panes in no team are listed under the empty key.
// Teams and the empty key with nothing to show are left out.
//
// Order: permission and ask prompts, longest waiting first; then returned
// tasks, newest first; then conflicts; then panes that are down; then stale
// unread mail, oldest first. Each team keeps at most MaxNeeds items.
//
// panes and tasks are the snapshot BuildGraph was given. moves is the task
// history, and now is the time the lists are judged at.
func NeedsYou(g Graph, panes []GraphPane, tasks []GraphTask, conflicts []PaneConflict, moves []TaskMove, now time.Time) map[string][]NeedsItem {
	team := make(map[string]string, len(g.Nodes))
	for _, n := range g.Nodes {
		team[n.Pane] = n.Team
	}
	out := map[string][]NeedsItem{}
	add := func(pane string, it NeedsItem) {
		if _, ok := team[pane]; !ok {
			return
		}
		out[team[pane]] = append(out[team[pane]], it)
	}

	for _, p := range panes {
		waiting := p.Activity == activityWaiting || p.Activity == activityBlocked
		switch {
		case waiting && p.HasPrompt:
			kind := NeedPermission
			if p.PromptKind == "ask" {
				kind = NeedAsk
			}
			add(p.ID, NeedsItem{ID: kind + ":" + p.ID, Kind: kind, Pane: p.ID, Time: p.StatusSince, Text: clip(p.PromptText, MaxWaitText)})
		case p.Failed || p.Exited && p.ExitCode != 0:
			text := "failed"
			if !p.Failed {
				text = "exited with code " + strconv.Itoa(p.ExitCode)
			}
			add(p.ID, NeedsItem{ID: NeedDown + ":" + p.ID, Kind: NeedDown, Pane: p.ID, Time: p.StatusSince, Text: text})
		case p.Activity == "idle" && p.Unread > 0 && !p.UnreadSince.IsZero() && now.Sub(p.UnreadSince) > StaleUnreadAfter:
			add(p.ID, NeedsItem{ID: NeedStaleUnread + ":" + p.ID, Kind: NeedStaleUnread, Pane: p.ID, Time: p.UnreadSince, Text: strconv.Itoa(p.Unread) + " unread"})
		}
	}

	latest := map[string]TaskMove{}
	for _, m := range moves {
		if m.From != taskInReview || m.To != "in-progress" || now.Sub(m.Time) > ReturnedWindow {
			continue
		}
		if cur, ok := latest[m.Task]; !ok || m.Time.After(cur.Time) {
			latest[m.Task] = m
		}
	}
	for _, t := range tasks {
		m, ok := latest[t.ID]
		if !ok || t.State != "in-progress" || t.OwnerPane == "" {
			continue
		}
		add(t.OwnerPane, NeedsItem{ID: NeedReturned + ":" + t.ID, Kind: NeedReturned, Pane: t.OwnerPane, Task: t.ID, Time: m.Time, Text: clip(m.Note, MaxWaitText)})
	}

	for _, c := range conflicts {
		a, b := c.A, c.B
		if a > b {
			a, b = b, a
		}
		paths := c.Paths
		more := 0
		if len(paths) > MaxConflictPaths {
			more = len(paths) - MaxConflictPaths
			paths = paths[:MaxConflictPaths]
		}
		text := strings.Join(paths, ", ")
		if more > 0 {
			text += " and " + strconv.Itoa(more) + " more"
		}
		add(c.A, NeedsItem{ID: NeedConflict + ":" + a + ":" + b, Kind: NeedConflict, Pane: c.A, Other: c.B, Time: c.Since, Text: clip(text, MaxWaitText)})
	}

	for k, items := range out {
		slices.SortFunc(items, compareNeeds)
		if len(items) > MaxNeeds {
			items = items[:MaxNeeds]
		}
		out[k] = items
	}
	return out
}

// needRank is where a kind sits in the list. Permission and ask share a rank so
// they sort together by waiting time.
func needRank(kind string) int {
	switch kind {
	case NeedPermission, NeedAsk:
		return 0
	case NeedReturned:
		return 1
	case NeedConflict:
		return 2
	case NeedDown:
		return 3
	}
	return 4
}

func compareNeeds(a, b NeedsItem) int {
	if r := needRank(a.Kind) - needRank(b.Kind); r != 0 {
		return r
	}
	// A zero time means unknown and goes last within its kind.
	switch {
	case a.Time.IsZero() && !b.Time.IsZero():
		return 1
	case !a.Time.IsZero() && b.Time.IsZero():
		return -1
	}
	if c := a.Time.Compare(b.Time); c != 0 {
		// Prompts, conflicts, down and stale mail list the oldest first; returned
		// tasks list the newest first.
		if a.Kind == NeedReturned {
			return -c
		}
		return c
	}
	return strings.Compare(a.ID, b.ID)
}

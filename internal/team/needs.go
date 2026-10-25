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
	NeedSetup       = "setup"
	NeedStaleUnread = "stale-unread"
	NeedStuck       = "stuck"
)

// Values of GraphPane.PromptKind.
const (
	PromptAsk        = "ask"
	PromptPermission = "permission"
)

const activityIdle = "idle"

const (
	// MaxNeeds is the most items kept for one team, or for the solo list.
	MaxNeeds = 50
	// MaxConflictPaths is the most paths a conflict item names.
	MaxConflictPaths = 5
	// maxConflictPath is the most characters of one path a conflict item keeps.
	maxConflictPath = 80
	// ReturnedWindow is how long after a task goes back from review that it
	// still counts as returned.
	ReturnedWindow = 24 * time.Hour
	// StaleUnreadAfter is how long unread mail may wait in an idle pane before
	// it is listed. Mail younger than this is the ordinary lag of an agent that
	// is between turns, not something for the person to chase.
	StaleUnreadAfter = 30 * time.Minute
	// StuckAfter is how long a pane may be working without writing anything to
	// its terminal before it is listed as stuck. It is a quiet item: the agent may
	// be thinking, so it asks nothing of the person, it only says where to look.
	StuckAfter = 10 * time.Minute
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
	// Other is the second pane of a conflict. For a conflict, Pane and Other are
	// the pair in sorted order, whichever way the radar listed it.
	Other string    `json:"other,omitempty"`
	Task  string    `json:"task,omitempty"`
	Time  time.Time `json:"time"`
	Text  string    `json:"text,omitempty"`
}

// TeamNeeds is the list for one team.
type TeamNeeds struct {
	Items []NeedsItem `json:"items"`
	// Hidden counts the items left out because the list holds only MaxNeeds.
	Hidden int `json:"hidden,omitempty"`
}

// NeedsYou lists, for each team, what needs the user, most urgent first. The
// map is keyed by team id; panes in no team are listed under the empty key, with
// the same bound as a team. Teams and the empty key with nothing to show are
// left out. A conflict between panes of two teams is listed in both.
//
// Order: permission and ask prompts, longest waiting first; then returned
// tasks, newest first, until the owner pane has worked since the return; then conflicts; then panes that are down; then stale
// unread mail, which is one item for the team: the oldest pane's, with a count
// when more than one pane has such mail. An item with an unknown time goes after the others
// of its kind. Each list keeps at most MaxNeeds items and counts the rest.
//
// panes and tasks are the snapshot BuildGraph was given, and the caller fills
// them from the same view: Activity, not Status, is what idle and waiting mean
// here. moves is the task history, and now is the time the lists are judged at.
//
// Down is a pane that failed, or that exited with a code other than zero. The
// caller sets ClosedByUser on a pane the person closed or killed, which is not
// listed. A shell is listed only when it failed. A pane that is down is never
// listed as waiting on a prompt, since it cannot be answered. Stale unread
// needs UnreadSince: a pane with unread mail and a zero UnreadSince is never
// listed. It counts UnreadMail, which leaves out the notices Flockdeck wrote.
func NeedsYou(g Graph, panes []GraphPane, tasks []GraphTask, conflicts []PaneConflict, moves []TaskMove, now time.Time) map[string]TeamNeeds {
	team := make(map[string]string, len(g.Nodes))
	for _, n := range g.Nodes {
		team[n.Pane] = n.Team
	}
	lists := map[string][]NeedsItem{}
	add := func(pane string, it NeedsItem) {
		if t, ok := team[pane]; ok {
			lists[t] = append(lists[t], it)
		}
	}

	// Stale unread mail is one item for a team, not one per pane.
	type stale struct {
		pane  string
		since time.Time
		n     int
	}
	staleBy := map[string][]stale{}
	seen := map[string]bool{}
	for _, p := range panes {
		if seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		if p.Exited || p.Failed {
			unexpected := p.Failed || !p.Shell && p.ExitCode != 0
			if unexpected && !p.ClosedByUser {
				text := failedText(p)
				if !p.Failed {
					text = "exited with code " + strconv.Itoa(p.ExitCode)
				}
				add(p.ID, NeedsItem{ID: NeedDown + ":" + p.ID, Kind: NeedDown, Pane: p.ID, Time: p.StatusSince, Text: text})
			}
			continue
		}
		waiting := p.Activity == activityWaiting || p.Activity == activityBlocked
		switch {
		case waiting && p.HasPrompt:
			kind := NeedAsk
			if p.PromptKind == PromptPermission {
				kind = NeedPermission
			}
			add(p.ID, NeedsItem{ID: kind + ":" + p.ID, Kind: kind, Pane: p.ID, Time: p.StatusSince, Text: PlainText(p.PromptText, MaxWaitText)})
		case p.SetupText != "":
			add(p.ID, NeedsItem{ID: NeedSetup + ":" + p.ID, Kind: NeedSetup, Pane: p.ID, Time: p.StatusSince, Text: PlainText(p.SetupText, MaxWaitText)})
		case p.Activity == activityWorking && !p.LastOutput.IsZero() && now.Sub(p.LastOutput) > StuckAfter:
			add(p.ID, NeedsItem{ID: NeedStuck + ":" + p.ID, Kind: NeedStuck, Pane: p.ID, Time: p.LastOutput, Text: "no output for " + quietFor(now.Sub(p.LastOutput))})
		case p.Activity == activityIdle && p.UnreadMail > 0 && !p.UnreadSince.IsZero() && now.Sub(p.UnreadSince) > StaleUnreadAfter:
			if t, ok := team[p.ID]; ok {
				staleBy[t] = append(staleBy[t], stale{p.ID, p.UnreadSince, p.UnreadMail})
			}
		}
	}
	for t, list := range staleBy {
		slices.SortFunc(list, func(a, b stale) int {
			if c := a.since.Compare(b.since); c != 0 {
				return c
			}
			return strings.Compare(a.pane, b.pane)
		})
		oldest := list[0]
		it := NeedsItem{ID: NeedStaleUnread + ":" + oldest.pane, Kind: NeedStaleUnread, Pane: oldest.pane, Time: oldest.since, Text: strconv.Itoa(oldest.n) + " unread"}
		if len(list) > 1 {
			total := 0
			for _, s := range list {
				total += s.n
			}
			it.Text = strconv.Itoa(len(list)) + " agents, " + strconv.Itoa(total) + " unread"
		}
		lists[t] = append(lists[t], it)
	}

	byID := make(map[string]GraphPane, len(panes))
	for _, p := range panes {
		byID[p.ID] = p
	}
	latest := map[string]TaskMove{}
	for _, m := range moves {
		if m.From != taskInReview || m.To != taskInProgress || m.Time.After(now) || now.Sub(m.Time) > ReturnedWindow {
			continue
		}
		if cur, ok := latest[m.Task]; !ok || m.Time.After(cur.Time) {
			latest[m.Task] = m
		}
	}
	for _, t := range tasks {
		m, ok := latest[t.ID]
		if !ok || t.State != taskInProgress || t.OwnerPane == "" {
			continue
		}
		// Once the owner has picked the task up again there is nothing left for the
		// person to do about the return.
		if o, found := byID[t.OwnerPane]; found && (o.LastOutput.After(m.Time) || o.Activity == activityWorking && o.StatusSince.After(m.Time)) {
			continue
		}
		add(t.OwnerPane, NeedsItem{ID: NeedReturned + ":" + t.ID, Kind: NeedReturned, Pane: t.OwnerPane, Task: t.ID, Time: m.Time, Text: PlainText(m.Note, MaxWaitText)})
	}

	pairs := map[string]bool{}
	for _, c := range conflicts {
		a, b := c.A, c.B
		if a > b {
			a, b = b, a
		}
		id := NeedConflict + ":" + a + ":" + b
		if a == b || pairs[id] {
			continue
		}
		pairs[id] = true
		it := NeedsItem{ID: id, Kind: NeedConflict, Pane: a, Other: b, Time: c.Since, Text: conflictText(c.Paths)}
		ta, aok := team[a]
		tb, bok := team[b]
		if aok {
			add(a, it)
		}
		if bok && !(aok && ta == tb) {
			add(b, it)
		}
	}

	out := make(map[string]TeamNeeds, len(lists))
	for k, items := range lists {
		slices.SortFunc(items, compareNeeds)
		n := TeamNeeds{Items: items}
		if len(items) > MaxNeeds {
			n.Items, n.Hidden = items[:MaxNeeds], len(items)-MaxNeeds
		}
		out[k] = n
	}
	return out
}

// IsStuck says whether a needs item is the stuck rule's.
func IsStuck(kind string) bool { return kind == NeedStuck }

// AsksPerson says an item of this kind wants something from the person, as
// opposed to the quiet kinds (old unread mail, a pane gone quiet), which only say
// where to look.
func AsksPerson(kind string) bool {
	return kind != NeedStaleUnread && kind != NeedStuck
}

// quietFor is a quiet time as "12m" or "2h 5m".
func quietFor(d time.Duration) string {
	m := int(d / time.Minute)
	if m < 60 {
		return strconv.Itoa(m) + "m"
	}
	if m%60 == 0 {
		return strconv.Itoa(m/60) + "h"
	}
	return strconv.Itoa(m/60) + "h " + strconv.Itoa(m%60) + "m"
}

// conflictText names at most MaxConflictPaths paths, each cut on its own, and
// counts the rest, so the count is never cut off.
func conflictText(paths []string) string {
	shown := paths
	if len(shown) > MaxConflictPaths {
		shown = shown[:MaxConflictPaths]
	}
	parts := make([]string, len(shown))
	for i, p := range shown {
		parts[i] = PlainText(p, maxConflictPath)
	}
	text := strings.Join(parts, ", ")
	if more := len(paths) - len(shown); more > 0 {
		text += " and " + strconv.Itoa(more) + " more"
	}
	return text
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
	case NeedSetup:
		return 4
	case NeedStaleUnread:
		return 5
	case NeedStuck:
		return 6
	}
	return 7
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
		// Everything lists the oldest first except returned tasks, newest first.
		if a.Kind == NeedReturned {
			return -c
		}
		return c
	}
	return strings.Compare(a.ID, b.ID)
}

package spend

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 12, 14, 0, 0, 0, time.UTC)

// A chat pane reports each call's tokens and what they cost, and the header
// shows the sum: the conversation so far, priced a call at a time.
func TestCallsAddUp(t *testing.T) {
	b := NewBook()
	b.Add(Report{Pane: "p1", Model: "claude-opus-5", Tokens: Tokens{In: 1000, Out: 200},
		Cost: Cost{USD: 0.01, Known: true, Source: "table", Checked: "2026-06-24"}}, t0)
	b.Add(Report{Pane: "p1", Model: "claude-opus-5", Tokens: Tokens{In: 3000, Out: 100, CacheRead: 2000},
		Cost: Cost{USD: 0.0125, Known: true, Source: "table", Checked: "2026-06-24"}}, t0)

	v := b.Pane("p1", t0)
	if v == nil {
		t.Fatal("a pane that reported spend shows nothing")
	}
	if v.USD != 0.0225 || v.Tokens != 4300 || v.In != 4000 || v.Out != 300 || v.CacheRead != 2000 {
		t.Errorf("got %+v, want $0.0225 over 4300 tokens (4000 in, 2000 of them cached, 300 out)", v)
	}
	if v.Unpriced || v.Source != "table" || v.Checked != "2026-06-24" {
		t.Errorf("got %+v, want a table price, fully known, checked 2026-06-24", v)
	}
}

// A model with no price is counted in tokens. Once any call was, the money is
// a floor and says so; a pane with nothing priced shows tokens alone.
func TestUnpricedTokensMakeAFloor(t *testing.T) {
	b := NewBook()
	b.Add(Report{Pane: "local", Model: "llama3", Tokens: Tokens{In: 500, Out: 50}}, t0)
	v := b.Pane("local", t0)
	if v == nil || v.USD != 0 || v.Tokens != 550 {
		t.Fatalf("got %+v, want 550 tokens and no money", v)
	}

	b.Add(Report{Pane: "mixed", Model: "claude-opus-5", Tokens: Tokens{In: 100}, Cost: Cost{USD: 0.001, Known: true, Source: "table"}}, t0)
	b.Add(Report{Pane: "mixed", Model: "someone-elses", Tokens: Tokens{In: 100}}, t0)
	if v := b.Pane("mixed", t0); v == nil || !v.Unpriced || v.USD != 0.001 {
		t.Errorf("got %+v, want $0.001 marked as a floor", v)
	}
}

// Claude Code's status line gives running totals, which replace what was known
// rather than adding to it, and a tick before the first answer says nothing
// about cost and takes nothing away.
func TestCumulativeReportsReplace(t *testing.T) {
	b := NewBook()
	b.Add(Report{Pane: "c", Conversation: "s1", Cumulative: true, Tokens: Tokens{In: 1000, Out: 10},
		Cost: Cost{USD: 0.5, Known: true, Source: "agent"}}, t0)
	b.Add(Report{Pane: "c", Conversation: "s1", Cumulative: true, Tokens: Tokens{In: 2000, Out: 20},
		Cost: Cost{USD: 0.75, Known: true, Source: "agent"}}, t0)
	b.Add(Report{Pane: "c", Conversation: "s1", Cumulative: true}, t0)
	v := b.Pane("c", t0)
	if v == nil || v.USD != 0.75 || v.Tokens != 2020 || v.Source != "agent" {
		t.Errorf("got %+v, want the last running total: $0.75 over 2020 tokens, the agent's own", v)
	}
}

// /clear starts a new conversation, and with it the session's figures, as
// Claude Code's own figure does.
func TestANewConversationStartsAgain(t *testing.T) {
	b := NewBook()
	b.Add(Report{Pane: "p", Conversation: "one", Tokens: Tokens{In: 100}, Cost: Cost{USD: 1, Known: true}}, t0)
	b.Add(Report{Pane: "p", Conversation: "two", Tokens: Tokens{In: 5}, Cost: Cost{USD: 0.01, Known: true}}, t0)
	if v := b.Pane("p", t0); v == nil || v.USD != 0.01 || v.Tokens != 5 {
		t.Errorf("got %+v, want only the new conversation's $0.01 and 5 tokens", v)
	}
}

// A window belongs to the login, so a reading from one pane is shown in every
// pane on that login, and only there. The newest reading wins.
func TestWindowsAreTheAccounts(t *testing.T) {
	b := NewBook()
	five := func(used float64) Window {
		return Window{Account: "claude:/home/a/.claude", Name: "five_hour", Used: used, Percent: true, ResetsAt: t0.Add(2 * time.Hour)}
	}
	b.Add(Report{Pane: "a", Account: "claude:/home/a/.claude", Windows: []Window{five(40)}}, t0)
	// Pane b is on the same login and has not had an answer yet: the status
	// line says which login it is before it has any window to report.
	b.Add(Report{Pane: "b", Account: "claude:/home/a/.claude"}, t0)
	b.Add(Report{Pane: "other", Account: "claude:/home/b/.claude"}, t0)
	b.Add(Report{Pane: "a", Account: "claude:/home/a/.claude", Windows: []Window{five(72)}}, t0.Add(time.Minute))

	for _, id := range []string{"a", "b"} {
		v := b.Pane(id, t0.Add(time.Minute))
		if v == nil || len(v.Windows) != 1 || v.Windows[0].Pct != 72 || v.Windows[0].Level != "" {
			t.Errorf("pane %s: got %+v, want the login's newest reading, 72%%", id, v)
		}
	}
	if v := b.Pane("other", t0); v != nil {
		t.Errorf("a pane on another login shows %+v", v)
	}
}

// Claude Code hands each pane's status line the windows from that session's
// last answer, so an idle pane on the login sends an old, lower figure again
// every time its line refreshes. Within one window usage only rises, so that
// figure does not replace a fresher one; a later reset is a new window, and
// its reading does, however low.
func TestAnIdlePaneDoesNotSendTheWindowBack(t *testing.T) {
	b := NewBook()
	const acct = "claude:/home/a/.claude"
	resets := t0.Add(2 * time.Hour)
	five := func(used float64, resets time.Time) []Window {
		return []Window{{Account: acct, Name: "five_hour", Used: used, Percent: true, ResetsAt: resets}}
	}
	b.Add(Report{Pane: "busy", Account: acct, Windows: five(92, resets)}, t0.Add(time.Minute))
	b.Add(Report{Pane: "idle", Account: acct, Windows: five(40, resets)}, t0.Add(5*time.Minute))
	for _, id := range []string{"busy", "idle"} {
		v := b.Pane(id, t0.Add(5*time.Minute))
		if v == nil || len(v.Windows) != 1 || v.Windows[0].Pct != 92 || v.Windows[0].AsOf != t0.Add(time.Minute).Unix() {
			t.Errorf("pane %s: got %+v, want 92%% as of the reading that said so", id, v)
		}
	}

	later := resets.Add(5 * time.Hour)
	b.Add(Report{Pane: "busy", Account: acct, Windows: five(3, later)}, t0.Add(10*time.Minute))
	if v := b.Pane("idle", t0.Add(10*time.Minute)); v == nil || len(v.Windows) != 1 || v.Windows[0].Pct != 3 {
		t.Errorf("got %+v, want the new window's 3%%", v)
	}
}

// An idle pane's status line sends the same figure again every time it
// refreshes, which is the reading from its last answer and not a new one:
// taken as new, the header said "as of just now" of a figure an hour old, for
// as long as the pane sat there. The same figure is fresh only from a pane
// whose totals show it has just been answered; a higher one is fresh from
// anybody.
func TestTheSameFigureSentAgainIsNotANewReading(t *testing.T) {
	b := NewBook()
	const acct = "claude:/home/a/.claude"
	resets := t0.Add(3 * time.Hour)
	five := func(used float64) []Window {
		return []Window{{Account: acct, Name: "five_hour", Used: used, Percent: true, ResetsAt: resets}}
	}
	asOf := func(at time.Time) int64 {
		t.Helper()
		v := b.Pane("p", at)
		if v == nil || len(v.Windows) != 1 {
			t.Fatalf("got %+v, want one window", v)
		}
		return v.Windows[0].AsOf
	}
	totals := Tokens{In: 1000, Out: 10}
	b.Add(Report{Pane: "p", Account: acct, Cumulative: true, Tokens: totals, Windows: five(40)}, t0)

	// The line refreshes with nothing new to say.
	b.Add(Report{Pane: "p", Account: acct, Cumulative: true, Tokens: totals, Windows: five(40)}, t0.Add(30*time.Minute))
	if got := asOf(t0.Add(30 * time.Minute)); got != t0.Unix() {
		t.Errorf("as of %v, want the first reading's %v: nothing was answered since", time.Unix(got, 0).UTC(), t0)
	}

	// An answer since: the totals moved, and the same figure is a reading of now.
	b.Add(Report{Pane: "p", Account: acct, Cumulative: true, Tokens: Tokens{In: 2000, Out: 20}, Windows: five(40)}, t0.Add(40*time.Minute))
	if got := asOf(t0.Add(40 * time.Minute)); got != t0.Add(40*time.Minute).Unix() {
		t.Errorf("as of %v, want the answered pane's reading, %v", time.Unix(got, 0).UTC(), t0.Add(40*time.Minute))
	}

	// A higher figure is newer from any pane on the login.
	b.Add(Report{Pane: "q", Account: acct, Windows: five(45)}, t0.Add(50*time.Minute))
	if got := asOf(t0.Add(50 * time.Minute)); got != t0.Add(50*time.Minute).Unix() {
		t.Errorf("as of %v, want the higher reading's %v", time.Unix(got, 0).UTC(), t0.Add(50*time.Minute))
	}
}

// A window past its reset time describes one that no longer exists, and is
// dropped rather than shown at the figure it reached.
func TestAnExpiredWindowIsDropped(t *testing.T) {
	b := NewBook()
	w := Window{Account: "acct", Name: "five_hour", Used: 99, Percent: true, ResetsAt: t0.Add(time.Hour)}
	b.Add(Report{Pane: "p", Tokens: Tokens{In: 1}, Windows: []Window{w}}, t0)
	if v := b.Pane("p", t0); v == nil || len(v.Windows) != 1 || v.Windows[0].Level != "full" {
		t.Fatalf("got %+v, want the window at 99%%, marked full", v)
	}
	if v := b.Pane("p", t0.Add(time.Hour)); v == nil || len(v.Windows) != 0 {
		t.Errorf("got %+v after the reset, want no window", v)
	}
	// Nor is one already past its reset when it arrives.
	b.Add(Report{Pane: "p", Windows: []Window{w}}, t0.Add(2*time.Hour))
	if v := b.Pane("p", t0.Add(2*time.Hour)); v != nil && len(v.Windows) != 0 {
		t.Errorf("a reading past its reset was kept: %+v", v)
	}
}

// The tightest window comes first, since that is the one the header has room
// for, and each is coloured at the thresholds.
func TestTightestWindowFirst(t *testing.T) {
	b := NewBook()
	b.Add(Report{Pane: "p", Windows: []Window{
		{Account: "a", Name: "five_hour", Used: 81.4, Percent: true},
		{Account: "a", Name: "seven_day", Used: 96, Percent: true},
		{Account: "a", Name: "opus", Used: 12, Percent: true},
	}}, t0)
	v := b.Pane("p", t0)
	if v == nil || len(v.Windows) != 3 {
		t.Fatalf("got %+v, want three windows", v)
	}
	got := []WindowView{v.Windows[0], v.Windows[1], v.Windows[2]}
	if got[0].Name != "seven_day" || got[0].Level != "full" ||
		got[1].Name != "five_hour" || got[1].Pct != 81 || got[1].Level != "warn" ||
		got[2].Name != "opus" || got[2].Level != "" {
		t.Errorf("got %+v, want seven_day (full), five_hour 81%% (warn), opus", got)
	}
}

// Rounded to what is drawn: readings that differ only past the fourth decimal
// place of a dollar, or within the minute, make the same view -- which is what
// keeps the state message identical, and so unsent.
func TestTheViewIsRoundedToWhatIsShown(t *testing.T) {
	a, b := NewBook(), NewBook()
	w := Window{Account: "x", Name: "five_hour", Used: 40.2, Percent: true, ResetsAt: t0.Add(time.Hour)}
	a.Add(Report{Pane: "p", Cumulative: true, Cost: Cost{USD: 1.23401, Known: true}, Windows: []Window{w}}, t0)
	w.Used = 39.9
	b.Add(Report{Pane: "p", Cumulative: true, Cost: Cost{USD: 1.23399, Known: true}, Windows: []Window{w}}, t0.Add(20*time.Second))
	va, vb := a.Pane("p", t0.Add(30*time.Second)), b.Pane("p", t0.Add(30*time.Second))
	if va.USD != vb.USD || va.Windows[0] != vb.Windows[0] {
		t.Errorf("%+v and %+v differ, though the header draws them alike", va, vb)
	}
}

// A closed pane's figures go with it.
func TestRetainForgetsClosedPanes(t *testing.T) {
	b := NewBook()
	b.Add(Report{Pane: "open", Tokens: Tokens{In: 1}}, t0)
	b.Add(Report{Pane: "closed", Tokens: Tokens{In: 1}}, t0)
	b.Retain(func(id string) bool { return id == "open" })
	if b.Pane("closed", t0) != nil || b.Pane("open", t0) == nil {
		t.Error("Retain kept the wrong pane")
	}
}

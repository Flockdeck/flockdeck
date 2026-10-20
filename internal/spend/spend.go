// Package spend keeps what the agents in the panes have spent, and how close
// they are to the limits they run under.
//
// Two kinds of people run agents, and they want different numbers. Somebody
// paying per token wants money; somebody on a subscription pays the same
// whatever happens and is stopped by a usage window instead, so for them the
// headline is how much of the window is used and when it resets. Both numbers
// come from the agents themselves -- Flockdeck's own chat client counts every
// call it makes, and Claude Code hands its status line the session's cost and
// the subscription's windows -- and nothing here is fetched from anywhere: the
// figures arrive over the loopback hook server and never leave the machine.
//
// Nothing here is billed truth. A price is Flockdeck's table or the agent's
// own list price, and the interface marks every figure derived from one as an
// estimate. Tokens are counted exactly and carry no mark.
package spend

import (
	"math"
	"slices"
	"strings"
	"sync"
	"time"
)

// Tokens is what model calls read and wrote. Zero means none, not unknown.
type Tokens struct {
	In           int64 `json:"in,omitempty"`
	Out          int64 `json:"out,omitempty"`
	CacheRead    int64 `json:"cacheRead,omitempty"`
	CacheWrite5m int64 `json:"cacheWrite5m,omitempty"`
	CacheWrite1h int64 `json:"cacheWrite1h,omitempty"`
	Reasoning    int64 `json:"reasoning,omitempty"`
}

func (t *Tokens) add(v Tokens) {
	t.In += v.In
	t.Out += v.Out
	t.CacheRead += v.CacheRead
	t.CacheWrite5m += v.CacheWrite5m
	t.CacheWrite1h += v.CacheWrite1h
	t.Reasoning += v.Reasoning
}

// total is every token read and written, which is what a glance at a pane
// wants when there is no price to put on them.
func (t Tokens) total() int64 { return t.In + t.Out }

// Cost is money, and where the figure came from.
type Cost struct {
	USD float64 `json:"usd,omitempty"`
	// Known is false when some of the tokens had no price, so USD is a floor.
	Known bool `json:"known,omitempty"`
	// Source is "table" for tokens priced from Flockdeck's own table, and
	// "agent" for a figure the agent reported about itself at its own list
	// prices -- Claude Code's session cost is one. On a cost that is not
	// Known it says why, where the agent knows: "gateway" for calls through
	// an address of the user's own, which charges what it charges whatever
	// the model is listed at.
	Source string `json:"source,omitempty"`
	// Checked is the date the price table was read from the vendor's page, for
	// a "table" cost.
	Checked string `json:"checked,omitempty"`
}

// Window is one limit and how much of it is used.
//
// A limit belongs to an account, not to a pane: five Claude panes on one login
// draw on one five-hour window, as claude.ai and other machines on that login
// do, so a reading from any of them describes all of them.
type Window struct {
	// Account says whose limit it is, in terms that stay on this machine: the
	// Claude Code configuration folder for a Claude login. A key itself is
	// never used for this, hashed or otherwise.
	Account string `json:"account"`
	// Name is the window's own name, as the agent calls it: "five_hour",
	// "seven_day".
	Name string `json:"name"`
	// Used is 0 to 100 when Percent, and otherwise a count against Limit.
	Used    float64 `json:"used"`
	Limit   float64 `json:"limit,omitempty"`
	Percent bool    `json:"percent,omitempty"`
	// ResetsAt is when the window starts over. A reading past it describes a
	// window that no longer exists.
	ResetsAt time.Time `json:"resetsAt,omitzero"`
	// Seen is when the reading arrived. It is set here, on arrival, rather
	// than taken from the reporter.
	Seen   time.Time `json:"seen,omitzero"`
	Source string    `json:"source,omitempty"`
}

func (w Window) key() string { return w.Account + "\x00" + w.Name }

// expired reports whether the window a reading describes has already reset,
// which is when Claude Code itself stops showing it.
func (w Window) expired(now time.Time) bool {
	return !w.ResetsAt.IsZero() && !now.Before(w.ResetsAt)
}

// Report is what a pane's agent says it has spent, sent over the hook server.
type Report struct {
	// Pane is the pane's own id, which is what every lifecycle event is
	// reported against as well.
	Pane string `json:"session"`
	// Conversation is the agent's own id for the conversation, where it says.
	// A new one starts the pane's session figures again, as /clear does in
	// Claude Code.
	Conversation string `json:"conversation,omitempty"`
	Model        string `json:"model,omitempty"`
	// Account names the login the pane draws on, reported even before any
	// window is, so the windows another pane reports for it can be shown here.
	Account string `json:"account,omitempty"`
	Tokens  Tokens `json:"tokens"`
	Cost    Cost   `json:"cost"`
	// Cumulative says Tokens and Cost are the conversation's running totals as
	// the agent counts them, which replace what was known rather than adding
	// to it. Otherwise they are one call's, and are added up here.
	Cumulative bool     `json:"cumulative,omitempty"`
	Windows    []Window `json:"windows,omitempty"`
}

// Thresholds at which a window is worth a colour: amber at WarnAt, red at
// FullAt. They are fixed until the setting for them exists.
const (
	WarnAt = 80
	FullAt = 95
)

// level is what the interface colours a window by.
func level(pct float64) string {
	switch {
	case pct >= FullAt:
		return "full"
	case pct >= WarnAt:
		return "warn"
	}
	return ""
}

// Book is what every pane has spent in its current conversation, and the
// latest reading of every window, held in memory.
//
// It is safe for concurrent use: reports arrive on the hook server's
// goroutines and are read back on the workspace's.
type Book struct {
	mu      sync.Mutex
	panes   map[string]*paneEntry
	windows map[string]Window
}

type paneEntry struct {
	conv     string
	model    string
	tokens   Tokens
	usd      float64
	unpriced bool
	// unpricedWhy is the reason the last unpriced call gave, if it gave one.
	unpricedWhy string
	source      string
	checked     string
	accounts    []string
}

// NewBook returns an empty book.
func NewBook() *Book {
	return &Book{panes: map[string]*paneEntry{}, windows: map[string]Window{}}
}

// Add records one report, as of now.
func (b *Book) Add(r Report, now time.Time) {
	if r.Pane == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.panes[r.Pane]
	if e == nil {
		e = &paneEntry{}
		b.panes[r.Pane] = e
	}
	// A new conversation starts the session's figures again. The first report
	// a pane makes names the conversation it is already in.
	if r.Conversation != "" {
		if e.conv != "" && e.conv != r.Conversation {
			*e = paneEntry{accounts: e.accounts}
		}
		e.conv = r.Conversation
	}
	if r.Model != "" {
		e.model = r.Model
	}
	if r.Account != "" && !slices.Contains(e.accounts, r.Account) {
		e.accounts = append(e.accounts, r.Account)
	}
	before := e.tokens
	b.addCost(e, r)
	// Whether the pane has had an answer since it last reported: its totals
	// move with every one. The windows a status line is handed are those of
	// the session's last answer, so only then is a figure it sends again a
	// reading taken now.
	answered := e.tokens != before

	for _, w := range r.Windows {
		if w.Account == "" || w.Name == "" || w.expired(now) {
			continue
		}
		w.Seen = now
		// A limit is the account's, so a reading from any pane on it is the
		// whole of it -- but not always the newest. Claude Code hands a pane's
		// status line the windows from that session's last answer, and an idle
		// pane whose line refreshes sent back a figure the other panes on the
		// login had long since passed, which every one of them then showed "as
		// of just now". Within one window usage only rises, so the larger figure
		// is the fresher; a later reset time is a new window, whose reading
		// replaces the old one however low it is.
		//
		// The same figure again is a fresh reading only from a pane that has
		// just been answered. From one sitting idle it is the old reading sent
		// back, and taking it as new kept every idle pane's reading "as of just
		// now" for as long as its line refreshed, so the header never said how
		// old it was.
		old, ok := b.windows[w.key()]
		fresher := !ok || w.ResetsAt.After(old.ResetsAt)
		if !fresher && w.ResetsAt.Equal(old.ResetsAt) {
			fresher = w.Used > old.Used || (w.Used == old.Used && answered)
		}
		if fresher {
			b.windows[w.key()] = w
		}
		if !slices.Contains(e.accounts, w.Account) {
			e.accounts = append(e.accounts, w.Account)
		}
	}
}

func (b *Book) addCost(e *paneEntry, r Report) {
	if r.Cumulative {
		// The agent's own running figure replaces the last one. A report that
		// carries none, which Claude Code's status line does until the first
		// answer, leaves what was known alone.
		if r.Tokens != (Tokens{}) {
			e.tokens = r.Tokens
		}
		if r.Cost.Known {
			e.usd, e.unpriced = r.Cost.USD, false
			e.source, e.checked = r.Cost.Source, r.Cost.Checked
		}
		return
	}
	if r.Tokens == (Tokens{}) {
		return
	}
	e.tokens.add(r.Tokens)
	if !r.Cost.Known {
		// Tokens nobody here can price. What is known is a floor from now on.
		e.unpriced = true
		if r.Cost.Source != "" {
			e.unpricedWhy = r.Cost.Source
		}
		return
	}
	e.usd += r.Cost.USD
	e.source, e.checked = r.Cost.Source, r.Cost.Checked
}

// Retain forgets every pane keep says no to, which is how a closed pane's
// figures go with it.
func (b *Book) Retain(keep func(pane string) bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id := range b.panes {
		if !keep(id) {
			delete(b.panes, id)
		}
	}
}

// PaneView is what the pane header draws: the session's spend, and every
// window the pane draws on, tightest first. Its figures are rounded to what
// the header shows, so that a reading which changes nothing on screen does
// not make a new state for every window to be sent.
type PaneView struct {
	// USD is the session's cost in dollars, an estimate: Source says whose.
	USD      float64 `json:"usd,omitempty"`
	Unpriced bool    `json:"unpriced,omitempty"`
	// UnpricedWhy says why tokens went unpriced, where the agent said:
	// "gateway" for calls through an address of the user's own. Empty is a
	// model with no price here, which is what the tooltip said of both.
	UnpricedWhy string `json:"unpricedWhy,omitempty"`
	Source      string `json:"source,omitempty"`
	Checked     string `json:"checked,omitempty"`
	// Tokens is everything read and written, and the four after it the parts
	// the tooltip names.
	Tokens    int64        `json:"tokens,omitempty"`
	In        int64        `json:"in,omitempty"`
	Out       int64        `json:"out,omitempty"`
	CacheRead int64        `json:"cacheRead,omitempty"`
	Reasoning int64        `json:"reasoning,omitempty"`
	Windows   []WindowView `json:"windows,omitempty"`
	// ContextUsed and ContextSize are how much of the model's context window the
	// conversation fills, in tokens, for an agent that reports it through the
	// Agent Client Protocol. Size is 0 when the agent has not said.
	ContextUsed int64 `json:"contextUsed,omitempty"`
	ContextSize int64 `json:"contextSize,omitempty"`
	// Cost and Currency are a cost the agent reported in a currency other than
	// dollars. It is shown as it came and added to nothing; USD holds only dollars.
	Cost     float64 `json:"cost,omitempty"`
	Currency string  `json:"currency,omitempty"`
	// Partial says the agent reports its spend in part: how full its context is
	// and, if it chooses, a cost, with no split of tokens into read, written and
	// cached, and no window of the account's. Any figure missing from the view
	// is then unknown, not zero, and a total that includes this pane is a floor.
	Partial bool `json:"partial,omitempty"`
}

// Context is what an agent says about its spend through the Agent Client
// Protocol's usage_update: how much of its context window is used, how big the
// window is, and optionally a cost. It carries no token split and no account
// windows, which is why a pane that reports only this is Partial.
type Context struct {
	Used, Size int64
	// Cost is a running total for the session, 0 when the agent gave none (a
	// free tier reports 0, and so does an agent that does not price). Currency
	// is its ISO 4217 code. An empty currency is not assumed to be dollars: the
	// cost is then taken as not reported, since a figure in an unknown currency
	// added to dollars would be wrong in a way nobody could see.
	Cost     float64
	Currency string
}

// MaxSessionCost is the largest cost, in the agent's currency, that a
// usage_update is believed to carry for one session. A figure above it, and one
// that is not a finite number, is taken as not reported: no real session costs a
// million dollars, and a larger number only exists to overflow the sums and the
// JSON made from them.
const MaxSessionCost = 1e6

// WithContext adds a structured pane's usage_update report to what the book
// holds for it, which is nothing unless it is also a pane the hook server hears
// from. It returns nil when the report says nothing and v is nil. The report
// replaces rather than adds: the agent's figures are the session's own running
// ones.
//
// The figures come from an agent and are checked first. A negative Used or Size
// is taken as 0. A cost that is not finite, is not above zero, is above
// MaxSessionCost, has no currency, or rounds to nothing in dollars is not
// reported, and the view then has no cost and no "agent" source. A Used above
// Size is shown as given, since both are the agent's own words. Spend is not
// carried across a restart of the pane: a new process reports from zero, so a
// team's total can fall, which is why a total with a partial pane is a floor.
func WithContext(v *PaneView, c Context) *PaneView {
	if v == nil {
		v = &PaneView{}
	} else {
		cp := *v
		v = &cp
	}
	v.Partial = true
	v.ContextUsed, v.ContextSize = max(c.Used, 0), max(c.Size, 0)
	cur := strings.ToUpper(strings.TrimSpace(c.Currency))
	if c.Cost > 0 && c.Cost <= MaxSessionCost && !math.IsNaN(c.Cost) && cur != "" {
		if cur == "USD" {
			// A ten-thousandth of a dollar is finer than the header writes.
			if usd := math.Round(c.Cost*1e4) / 1e4; usd > 0 {
				v.USD = usd
				v.Source = "agent"
			}
		} else {
			v.Cost, v.Currency = c.Cost, cur
		}
	}
	if v.USD == 0 && v.Cost == 0 && v.ContextUsed == 0 && v.ContextSize == 0 && v.Tokens == 0 && len(v.Windows) == 0 {
		return nil
	}
	return v
}

// WindowView is one window as the header draws it.
type WindowView struct {
	Name string `json:"name"`
	// Pct is how much of it is used, to the whole percent.
	Pct float64 `json:"pct"`
	// ResetsAt and AsOf are Unix seconds; AsOf to the minute, since it is only
	// ever said of a reading that is some minutes old.
	ResetsAt int64  `json:"resetsAt,omitempty"`
	AsOf     int64  `json:"asOf,omitempty"`
	Level    string `json:"level,omitempty"`
}

// Pane returns what a pane has spent and the windows it draws on, or nil when
// it has reported nothing worth showing.
func (b *Book) Pane(id string, now time.Time) *PaneView {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.panes[id]
	if e == nil {
		return nil
	}
	v := &PaneView{
		Tokens: e.tokens.total(),
		In:     e.tokens.In, Out: e.tokens.Out,
		CacheRead: e.tokens.CacheRead, Reasoning: e.tokens.Reasoning,
		Unpriced: e.unpriced && e.tokens.total() > 0,
	}
	if v.Unpriced {
		v.UnpricedWhy = e.unpricedWhy
	}
	if e.usd > 0 {
		// A ten-thousandth of a dollar is finer than the header ever writes.
		v.USD = math.Round(e.usd*1e4) / 1e4
		v.Source, v.Checked = e.source, e.checked
	}
	for key, w := range b.windows {
		if w.expired(now) {
			delete(b.windows, key)
			continue
		}
		if !w.Percent || !slices.Contains(e.accounts, w.Account) {
			continue
		}
		pct := math.Round(math.Max(0, math.Min(100, w.Used)))
		wv := WindowView{Name: w.Name, Pct: pct, Level: level(pct), AsOf: w.Seen.Truncate(time.Minute).Unix()}
		if !w.ResetsAt.IsZero() {
			wv.ResetsAt = w.ResetsAt.Unix()
		}
		v.Windows = append(v.Windows, wv)
	}
	// Tightest first, which is the one the header has room for; by name after
	// that, so two windows at the same figure do not trade places between
	// snapshots and make each one look new.
	slices.SortFunc(v.Windows, func(a, b WindowView) int {
		if a.Pct != b.Pct {
			if a.Pct > b.Pct {
				return -1
			}
			return 1
		}
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	if v.USD == 0 && v.Tokens == 0 && len(v.Windows) == 0 {
		return nil
	}
	return v
}

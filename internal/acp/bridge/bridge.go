// Package bridge turns an ACP session's traffic into what Flockdeck already
// shows: transcript.Entry values for the conversation, a status (working,
// waiting, idle), the activity line, and a permission card that carries the
// agent's own option ids. It has no workspace or server dependency; the pane
// layer owns a Bridge, feeds it the client's calls and asks it what changed.
//
// Behaviour taken from the ACP spike:
//
//   - Consecutive agent_thought_chunk pieces, and consecutive message chunks,
//     are one entry.
//   - A tool is one entry that is updated in place. opencode sent about two
//     tool_call_update a second, each repeating the whole raw input, so the
//     raw input is never kept, and Flush hands a changed entry out at most
//     once per MinInterval unless something worth showing at once happened
//     (a new entry, a status change).
//   - A pending permission request dominates: the status is waiting and the
//     activity names the request, whatever tool updates arrive before or after
//     it.
//   - A rejected tool arrives as failed and the turn ends end_turn. There is no
//     denied stop reason, so nothing here looks for one.
//   - Tool output is capped (transcript.ToolOutputCap) in the entry's summary
//     and the full text is kept for Detail.
//   - Unknown update kinds and content block types produce no entry and no
//     error. Subagent, compaction and image entries are not produced: ACP gave
//     nothing for them.
//
// A Bridge is safe for concurrent use. The tool call ids and every string in
// the traffic come from the agent, so all of it is capped.
package bridge

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// Status is the vocabulary of session.Status that a structured pane can
// derive without hooks or screen reading.
type Status string

const (
	StatusWorking Status = "working"
	StatusWaiting Status = "waiting"
	StatusIdle    Status = "idle"
)

const (
	defaultMinInterval = 250 * time.Millisecond
	// defaultDetailBudget bounds the full text kept for Detail across a
	// session. The oldest entry's detail is dropped first.
	defaultDetailBudget = 64 << 20
	// maxText bounds what is kept of one reply, one thinking block, or one
	// tool's output.
	maxText = 4 << 20

	labelCap   = 200
	summaryCap = 120
)

// Config tunes a Bridge. The zero value works.
type Config struct {
	// Now is the clock, for tests. Default time.Now.
	Now func() time.Time
	// MinInterval is the least time between two hand-outs of the same entry
	// when only its content grew. Default 250ms.
	MinInterval time.Duration
	// DetailBudget bounds the bytes kept for Detail. Default 64 MiB.
	DetailBudget int
}

// Usage is what the agent last reported with usage_update: tokens in the
// context window, the window's size, and a cost if it gave one.
type Usage struct {
	Used, Size int64
	Cost       float64
	Currency   string
}

// Card is a pending permission request as the pane shows it. Options are the
// agent's own; an answer must use one of their ids.
type Card struct {
	RequestID string
	SessionID string
	Tool      string
	Kind      string
	Summary   string
	File      string
	Command   string
	Options   []acp.PermissionOption
}

type item struct {
	e transcript.Entry
	// text is what Detail returns as Text: a thinking block's text, a tool's
	// full output. diff is a tool's full diff.
	text, diff string
	detail     int // bytes held in text and diff
	lastEmit   time.Time
	dirty      bool
	urgent     bool
	toolID     string
	merges     string // "reply" or "thinking" while consecutive chunks may still append
}

// Bridge is one session's conversation state.
type Bridge struct {
	cfg Config

	mu       sync.Mutex
	items    []*item
	tools    map[string]*item
	inFlight bool
	pending  []Card
	plan     []acp.PlanEntry
	usage    Usage
	detail   int
	evicted  int // items before this index have had their detail dropped
}

// New returns an empty Bridge.
func New(cfg Config) *Bridge {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.MinInterval <= 0 {
		cfg.MinInterval = defaultMinInterval
	}
	if cfg.DetailBudget <= 0 {
		cfg.DetailBudget = defaultDetailBudget
	}
	return &Bridge{cfg: cfg, tools: map[string]*item{}}
}

// add appends an entry. Call with b.mu held.
func (b *Bridge) add(e transcript.Entry) *item {
	now := b.cfg.Now()
	e.ID = "e" + strconv.Itoa(len(b.items)+1)
	e.TS = now.UTC().Format(time.RFC3339)
	it := &item{e: e, dirty: true, urgent: true}
	b.items = append(b.items, it)
	return it
}

// Status derives the pane's status: waiting while any permission request is
// open, whatever else is happening, then working while a turn is in flight.
func (b *Bridge) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case len(b.pending) > 0:
		return StatusWaiting
	case b.inFlight:
		return StatusWorking
	}
	return StatusIdle
}

// Activity is the line for what the agent is doing now: the open permission
// request if there is one, otherwise the most recent tool still running.
func (b *Bridge) Activity() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pending) > 0 {
		return "waiting for permission: " + b.pending[0].Tool
	}
	if !b.inFlight {
		return ""
	}
	for i := len(b.items) - 1; i >= 0; i-- {
		if e := b.items[i].e; e.Kind == transcript.KindTool && e.Status == transcript.StatusRunning {
			return e.Label
		}
	}
	return ""
}

// Pending returns the oldest open permission request, or nil.
func (b *Bridge) Pending() *Card {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pending) == 0 {
		return nil
	}
	c := b.pending[0]
	c.Options = append([]acp.PermissionOption(nil), c.Options...)
	return &c
}

// Plan returns the agent's latest plan.
func (b *Bridge) Plan() []acp.PlanEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]acp.PlanEntry(nil), b.plan...)
}

// Usage returns the agent's latest usage report.
func (b *Bridge) Usage() Usage {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.usage
}

// Snapshot returns every entry so far, oldest first, as they stand now.
func (b *Bridge) Snapshot() []transcript.Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]transcript.Entry, len(b.items))
	for i, it := range b.items {
		out[i] = it.e
	}
	return out
}

// Detail returns the full body of an entry that arrived trimmed: a tool's full
// output or diff, a thinking block's text. It reports false for an entry with
// none, or whose detail was dropped to stay inside the budget.
func (b *Bridge) Detail(id string) (transcript.Detail, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, it := range b.items {
		if it.e.ID != id {
			continue
		}
		if it.text == "" && it.diff == "" {
			return transcript.Detail{}, false
		}
		return transcript.Detail{Text: it.text, Diff: it.diff}, true
	}
	return transcript.Detail{}, false
}

// TurnStart records the person's prompt and marks the turn in flight. The
// returned entries are for the caller to hand on at once.
func (b *Bridge) TurnStart(text string) []transcript.Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.add(transcript.Entry{Kind: transcript.KindPrompt, Text: text})
	b.inFlight = true
	b.closeMerges()
	return b.take(true)
}

// TurnEnd marks the turn over and returns everything not yet handed out, so
// the end state is complete. A stop reason other than end_turn adds a notice.
// A tool the agent refused to run is already a failed tool by now; end_turn
// after one is the normal case.
func (b *Bridge) TurnEnd(stop string) []transcript.Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.inFlight = false
	b.closeMerges()
	switch stop {
	case acp.StopEndTurn, "":
	case acp.StopCancelled:
		b.add(transcript.Entry{Kind: transcript.KindNotice, Text: "Turn cancelled."})
	default:
		b.add(transcript.Entry{Kind: transcript.KindNotice, Text: "Turn ended: " + oneLine(stop, labelCap)})
	}
	return b.take(true)
}

func (b *Bridge) closeMerges() {
	if n := len(b.items); n > 0 {
		b.items[n-1].merges = ""
	}
}

// PermissionOpen records a permission request and returns its card.
func (b *Bridge) PermissionOpen(req acp.PermissionRequest) Card {
	c := Card{
		RequestID: req.RequestID,
		SessionID: req.SessionID,
		Tool:      oneLine(req.ToolCall.Title, labelCap),
		Kind:      req.ToolCall.Kind,
		Options:   append([]acp.PermissionOption(nil), req.Options...),
	}
	c.Summary = c.Kind + ": " + c.Tool
	if len(req.ToolCall.Locations) > 0 {
		c.File = req.ToolCall.Locations[0].Path
		c.Summary = strings.TrimSuffix(c.Kind+" "+filepath.Base(c.File), " ")
	}
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(req.ToolCall.RawInput, &in) == nil {
		c.Command = oneLine(in.Command, 4<<10)
	}
	b.mu.Lock()
	b.pending = append(b.pending, c)
	b.mu.Unlock()
	return c
}

// PermissionClosed removes a permission request, answered or not.
func (b *Bridge) PermissionClosed(requestID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, c := range b.pending {
		if c.RequestID == requestID {
			b.pending = append(b.pending[:i], b.pending[i+1:]...)
			return
		}
	}
}

// Handler returns an acp.Handler that feeds the Bridge. answer is called for
// each permission request, on its own goroutine, with the card to show; it
// returns the chosen option id or "" for cancelled, and should return when ctx
// is done.
func (b *Bridge) Handler(answer func(ctx context.Context, card Card) string) acp.Handler {
	return handler{b: b, answer: answer}
}

type handler struct {
	b      *Bridge
	answer func(context.Context, Card) string
}

func (h handler) Update(u acp.Update) { h.b.Update(u) }

func (h handler) Permission(ctx context.Context, req acp.PermissionRequest) string {
	card := h.b.PermissionOpen(req)
	defer h.b.PermissionClosed(card.RequestID)
	return h.answer(ctx, card)
}

// Update folds one session/update into the state. It is called in wire order
// and does not block.
func (b *Bridge) Update(u acp.Update) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch u.Kind {
	case acp.UpdateAgentMessageChunk:
		b.chunk(u, transcript.KindReply)
	case acp.UpdateAgentThoughtChunk:
		b.chunk(u, transcript.KindThinking)
	case acp.UpdateToolCall, acp.UpdateToolCallUpdate:
		b.tool(u)
	case acp.UpdatePlan:
		b.plan = append([]acp.PlanEntry(nil), u.Plan...)
	case acp.UpdateUsage:
		if u.Used != nil {
			b.usage.Used = *u.Used
		}
		if u.Size != nil {
			b.usage.Size = *u.Size
		}
		if u.Cost != nil {
			b.usage.Cost, b.usage.Currency = u.Cost.Amount, u.Cost.Currency
		}
	}
	// Anything else is a kind this bridge does not map; it is ignored.
}

// chunkText is the text a content block adds to a message.
func chunkText(c *acp.ContentBlock) string {
	switch {
	case c == nil:
		return ""
	case c.Type == "text":
		return c.Text
	case c.Type != "":
		return "[" + oneLine(c.Type, 40) + "]"
	}
	return ""
}

func (b *Bridge) chunk(u acp.Update, kind string) {
	text := chunkText(u.Content)
	if text == "" {
		return
	}
	var it *item
	if n := len(b.items); n > 0 && b.items[n-1].merges == kind {
		it = b.items[n-1]
	} else {
		b.closeMerges()
		it = b.add(transcript.Entry{Kind: kind})
		it.merges = kind
	}
	it.dirty = true
	switch kind {
	case transcript.KindReply:
		if room := maxText - len(it.e.Markdown); room > 0 {
			if len(text) > room {
				text = text[:room]
			}
			it.e.Markdown += text
		}
	case transcript.KindThinking:
		it.e.Chars += len(text)
		it.e.HasDetail = true
		if room := maxText - len(it.text); room > 0 {
			if len(text) > room {
				text = text[:room]
			}
			it.text += text
			it.detail += len(text)
			b.detail += len(text)
			b.trim()
		}
	}
}

func toolStatus(s string) string {
	switch s {
	case "completed":
		return transcript.StatusOK
	case "failed":
		return transcript.StatusError
	case "pending", "in_progress":
		return transcript.StatusRunning
	}
	return ""
}

func (b *Bridge) tool(u acp.Update) {
	id := u.ToolCallID
	if len(id) > 256 {
		id = id[:256]
	}
	it := b.tools[id]
	if it == nil {
		b.closeMerges()
		it = b.add(transcript.Entry{Kind: transcript.KindTool, Name: "tool", Label: "tool", Status: transcript.StatusRunning})
		it.toolID = id
		b.tools[id] = it
	}
	e := &it.e
	if u.ToolKind != "" {
		e.Name = oneLine(u.ToolKind, 40)
	}
	if u.Title != "" {
		e.Label = oneLine(u.Title, labelCap)
	}
	if s := toolStatus(u.Status); s != "" && s != e.Status {
		e.Status = s
		it.urgent = true
	}
	it.dirty = true

	// The raw input is not kept: an update repeats it whole every time. Output
	// and diff come from the content the update carries, which replaces the
	// previous content.
	if len(u.ToolContent) == 0 && len(u.RawOutput) == 0 {
		return
	}
	var texts []string
	var diff string
	for _, c := range u.ToolContent {
		switch c.Type {
		case "content":
			if c.Content != nil && c.Content.Type == "text" && c.Content.Text != "" {
				texts = append(texts, c.Content.Text)
			}
		case "diff":
			if c.OldText == nil {
				diff += transcript.WriteDiff(c.Path, c.NewText)
			} else {
				diff += transcript.EditDiff(c.Path, *c.OldText, c.NewText)
			}
		}
	}
	out := strings.Join(texts, "\n")
	if out == "" {
		out = rawOutputText(u.RawOutput)
	}
	if len(out) > maxText {
		out = out[:maxText]
	}
	if len(diff) > maxText {
		diff = diff[:maxText]
	}
	b.setOutput(it, out, diff)
}

// rawOutputText is a tool's rawOutput as text: a JSON string as itself,
// anything else as compact JSON.
func rawOutputText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

func (b *Bridge) setOutput(it *item, out, diff string) {
	b.detail -= it.detail
	it.text, it.diff, it.detail = "", "", 0
	e := &it.e
	e.Summary, e.Diff, e.HasDetail = "", "", false

	if diff != "" {
		if len(diff) <= transcript.DiffCap {
			e.Diff = diff
		} else {
			e.Summary = transcript.DiffSummary(diff)
			e.HasDetail = true
			it.diff = diff
		}
	}
	if out != "" && !(e.Diff != "" && e.Status == transcript.StatusOK) {
		// An edit's confirmation text is not worth a row when its diff says
		// what changed, except when it failed.
		summary, hasDetail := transcript.SummarizeToolOutput(out)
		if e.Summary == "" {
			e.Summary = summary
		}
		if hasDetail || len(out) > transcript.ToolOutputCap {
			e.HasDetail = true
			it.text = out
		}
	}
	it.detail = len(it.text) + len(it.diff)
	b.detail += it.detail
	b.trim()
}

// trim drops the oldest entries' detail until the total is inside the budget.
// The entry stays; it just no longer has a detail to fetch.
func (b *Bridge) trim() {
	for b.detail > b.cfg.DetailBudget && b.evicted < len(b.items) {
		it := b.items[b.evicted]
		b.evicted++
		if it.detail == 0 {
			continue
		}
		b.detail -= it.detail
		it.text, it.diff, it.detail = "", "", 0
		it.e.HasDetail = false
		it.dirty = true
	}
}

// Flush returns the entries that are due: new ones and ones whose status
// changed at once, ones whose content only grew once MinInterval has passed
// since they were last handed out. Each is as it stands now; an entry updated
// ten times in the interval is handed out once.
func (b *Bridge) Flush() []transcript.Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.take(false)
}

// take collects due entries. With all, every dirty entry is due. Call with b.mu
// held.
func (b *Bridge) take(all bool) []transcript.Entry {
	now := b.cfg.Now()
	var out []transcript.Entry
	for _, it := range b.items {
		if !it.dirty {
			continue
		}
		if !all && !it.urgent && now.Sub(it.lastEmit) < b.cfg.MinInterval {
			continue
		}
		it.dirty, it.urgent, it.lastEmit = false, false, now
		out = append(out, it.e)
	}
	return out
}

// oneLine is the first line of s, cut to max bytes.
func oneLine(s string, max int) string { return transcript.OneLine(s, max) }

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
//     error. Subagent and image entries are not produced: ACP gave nothing for
//     them.
//   - Session state is kept apart from the conversation (state.go): the slash
//     commands, the mode, the models and config options, the markdown plan and
//     the title. A compaction notice is a notice entry. A user_message_chunk
//     from a replay (BeginReplay) is a prompt entry; any other is
//     the agent echoing the prompt TurnStart already recorded, and is ignored.
//
// Everything in the traffic comes from the agent and is treated as hostile: every
// string is cleaned of control characters, ESC sequences and bidi marks and
// capped; entries, inline text and kept detail each have a budget; tool call
// ids are keyed by hash; and Flush limits how fast new entries and status
// changes are handed out. Dropped entries are counted by Dropped.
//
// A Bridge is safe for concurrent use. The tool call ids and every string in
// the traffic come from the agent, so all of it is capped.
package bridge

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"hash/maphash"
	"math"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// Status is the vocabulary of session.Status that a structured pane can
// derive without hooks or screen reading.
type Status string

const (
	// maxRequestID is the most of an agent's request or session id that is kept.
	maxRequestID = 64

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

	// labelCap is in runes; the other caps below are too, for one-line fields.
	labelCap   = 200
	summaryCap = 120

	defaultMaxEntries = 10000
	defaultTextBudget = 64 << 20
	// maxUrgent is how many entries that are due at once (new ones, status
	// changes) one Flush window hands out; the rest wait for the next window.
	maxUrgent = 64
	// maxFlaps is how many status changes of one entry are handed out at once
	// in a second.
	maxFlaps   = 4
	maxOptions = 16
	maxPlan    = 100
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
	// MaxEntries bounds the entries kept. Past it the oldest entries other
	// than prompts are dropped. Default 10000.
	MaxEntries int
	// TextBudget bounds the text held inline in entries (replies, diffs,
	// summaries, labels). Past it the oldest entries other than prompts are
	// dropped. Default 64 MiB.
	TextBudget int
	// AcceptQuestions says the agent is one whose requests of kind QuestionKind
	// are drawn as a form of questions: Flockdeck's own Claude Code adapter. For
	// any other agent a request of that kind is an ordinary one, with the kind
	// "other": its options and its command are all the person is shown.
	AcceptQuestions bool
	// ShownInput are the keys of a command's input that the adapter is known to
	// send and that are shown or harmless (manifest.Adapter.ShownInputKeys). Any
	// other key marks the card with NoteExtra. Nil means a default list.
	ShownInput []string
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
	// Seq is made by Flockdeck, one more for each card the bridge opens, and
	// never comes from the agent. RequestID is the agent's own, cleaned and
	// cut, and an agent can reuse it for a different request; an answer
	// names the Seq of the card it was given for, so that an answer to one
	// card is never taken for another that has the same RequestID.
	Seq       uint64
	RequestID string
	SessionID string
	Tool      string
	Kind      string
	Summary   string
	File      string
	Command   string
	// CommandText is the command with its line breaks, cleaned and cut at
	// cardCommandCap bytes; Command is only its first line. CommandAltered says
	// it is not the command the agent sent: cleaning took a character out of it
	// or it was cut. A reader that has to show the whole command (the phone's
	// approval card) goes by these and treats an altered one as unchecked.
	CommandText    string
	CommandAltered bool
	// Cwd is the directory the call says it runs in, when the adapter sends one
	// that Config.ShownInput lists: one line, cleaned. Notes are fixed codes (the
	// Note constants) for what the call does that the card does not show.
	Cwd     string
	Notes   []string
	Options []acp.PermissionOption
	// Body is the text the request shows about its call, such as the plan of
	// ExitPlanMode, and Diff the unified diff of the edit it asks about. Both
	// are plain text from the agent, cleaned and cut.
	Body, Diff string
	// Questions are those of a request of kind QuestionKind. A card with a
	// kind of QuestionKind and none is a malformed request.
	Questions []Question
}

type item struct {
	e transcript.Entry
	// text is what Detail returns as Text: a thinking block's text, a tool's
	// full output. diff is a tool's full diff.
	text, diff string
	detail     int // bytes held in text and diff
	inl        int // bytes held inline in e, counted against the text budget
	seq        int
	lastEmit   time.Time
	dirty      bool
	urgent     bool
	key        [sha256.Size]byte // the tool call id's hash, when keyed
	keyed      bool
	merges     string // "reply" or "thinking" while consecutive chunks may still append
	burstStart time.Time
	burstN     int
	out        outKey
}

// outKey identifies the output a tool entry was last built from, so an update
// that repeats it is not summarized again.
type outKey struct {
	set          bool
	n, dn        int
	h, dh        uint64
	status, sums string
}

// Bridge is one session's conversation state.
type Bridge struct {
	cfg  Config
	seed maphash.Seed

	mu       sync.Mutex
	items    []*item // ascending seq
	tools    map[[sha256.Size]byte]*item
	mails    map[mailKey]*item
	seq      int
	inFlight bool
	// replaying is set between BeginReplay and EndReplay: only then are updates
	// marked as replay taken in.
	replaying bool
	// liveSeen is set when an update that is not replay has come in while
	// replaying is set.
	liveSeen bool
	pending   []Card
	plan     []acp.PlanEntry
	usage    Usage
	state    SessionState
	seeded   seeded
	detail   int
	inline   int
	dropped  int
	// summaries counts the times a tool's output was taken in, for tests.
	summaries int
	// detailFrom is a seq: entries before it hold no detail.
	detailFrom int
	// windowStart and urgentUsed limit the entries handed out at once.
	windowStart time.Time
	urgentUsed  int
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
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = defaultMaxEntries
	}
	if cfg.TextBudget <= 0 {
		cfg.TextBudget = defaultTextBudget
	}
	return &Bridge{cfg: cfg, seed: maphash.MakeSeed(), tools: map[[sha256.Size]byte]*item{}}
}

// add appends an entry. Call with b.mu held.
func (b *Bridge) add(e transcript.Entry) *item {
	now := b.cfg.Now()
	b.seq++
	e.ID = "e" + strconv.Itoa(b.seq)
	e.TS = now.UTC().Format(time.RFC3339)
	it := &item{e: e, seq: b.seq, dirty: true, urgent: true}
	b.items = append(b.items, it)
	b.reinline(it)
	return it
}

// reinline recounts the text an entry holds inline.
func (b *Bridge) reinline(it *item) {
	e := &it.e
	n := len(e.Markdown) + len(e.Diff) + len(e.Text) + len(e.Summary) + len(e.Label)
	b.inline += n - it.inl
	it.inl = n
}

// Dropped is how many entries were dropped to stay inside MaxEntries and
// TextBudget.
func (b *Bridge) Dropped() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
}

// enforce drops the oldest entries when there are too many or they hold too
// much text. Prompts and the newest entry are kept. It drops to 90% of the
// limit so the work is not repeated on every update. Call with b.mu held.
func (b *Bridge) enforce() {
	if n := b.cfg.MaxEntries; len(b.items) > n {
		b.drop(func(removed int) bool { return len(b.items)-removed <= n-n/10 })
	}
	if n := b.cfg.TextBudget; b.inline > n {
		b.drop(func(int) bool { return b.inline <= n-n/10 })
	}
}

// drop removes entries, oldest first, until done reports true.
func (b *Bridge) drop(done func(removed int) bool) {
	last := len(b.items) - 1
	out := make([]*item, 0, len(b.items))
	removed := 0
	for i, it := range b.items {
		if i < last && it.e.Kind != transcript.KindPrompt && !done(removed) {
			b.detail -= it.detail
			b.inline -= it.inl
			if it.keyed {
				delete(b.tools, it.key)
			}
			if it.e.Kind == transcript.KindMessage {
				delete(b.mails, mailKey{it.e.MsgID, it.e.Dir})
			}
			b.dropped++
			removed++
			continue
		}
		out = append(out, it)
	}
	b.items = out
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
	n, err := strconv.Atoi(strings.TrimPrefix(id, "e"))
	if err != nil || !strings.HasPrefix(id, "e") {
		return transcript.Detail{}, false
	}
	i := sort.Search(len(b.items), func(i int) bool { return b.items[i].seq >= n })
	if i == len(b.items) || b.items[i].seq != n {
		return transcript.Detail{}, false
	}
	it := b.items[i]
	if it.text == "" && it.diff == "" {
		return transcript.Detail{}, false
	}
	return transcript.Detail{Text: it.text, Diff: it.diff}, true
}

// TurnStart records the person's prompt and marks the turn in flight. The
// returned entries are for the caller to hand on at once.
func (b *Bridge) TurnStart(text string) []transcript.Entry {
	_, entries := b.StartTurn(text)
	return entries
}

// StartTurn is TurnStart that also returns the id of the prompt entry.
func (b *Bridge) StartTurn(text string) (string, []transcript.Entry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeMerges()
	id := b.add(transcript.Entry{Kind: transcript.KindPrompt, Text: text}).e.ID
	b.inFlight = true
	b.enforce()
	return id, b.take(true)
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
		b.add(transcript.Entry{Kind: transcript.KindNotice, Text: "Turn ended: " + line(stop, labelCap)})
	}
	b.enforce()
	return b.take(true)
}

func (b *Bridge) closeMerges() {
	if n := len(b.items); n > 0 {
		b.items[n-1].merges = ""
	}
}

// cardSeqs numbers the cards of the whole process, not of one Bridge: a pane
// that is restarted gets a new Bridge, and a click made for a card of the old
// one must not match a card of the new one. The high bits are a tag chosen when
// the process starts, so that a window kept open across a restart of Flockdeck
// does not match either. The number stays under 2^53, which a browser keeps
// exactly.
//
// The tag has 21 bits, the lowest always set, so 20 of them are random and the
// tag is never zero. The count below it has 32 bits and wraps after 2^32 cards
// of one process, which is accepted: a click would have to name a card that old
// and still be open.
var (
	cardSeqTag = func() uint64 {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			t := uint64(time.Now().UnixNano())
			b = [4]byte{byte(t), byte(t >> 8), byte(t >> 16), byte(t >> 24)}
		}
		return cardTag(b)
	}()
	cardSeqNext atomic.Uint64
)

// cardTag is the high part of a card number, made from four bytes: 21 bits of
// them with the lowest set, shifted above the 32 bits of the count.
func cardTag(b [4]byte) uint64 {
	return (uint64(binary.LittleEndian.Uint32(b[:]))&(1<<21-1) | 1) << 32
}

func nextCardSeq() uint64 { return cardSeqTag | (cardSeqNext.Add(1) & (1<<32 - 1)) }

// PermissionOpen records a permission request and returns its card. Every
// string on the card is cleaned and capped, and at most 16 options are kept.
func (b *Bridge) PermissionOpen(req acp.PermissionRequest) Card {
	c, _ := b.open(req)
	return c
}

// open is PermissionOpen that also returns the agent's own option ids by the
// id shown on the card, for answering.
func (b *Bridge) open(req acp.PermissionRequest) (Card, map[string]string) {
	c := Card{
		RequestID: line(req.RequestID, maxRequestID),
		SessionID: line(req.SessionID, 200),
		Tool:      line(req.ToolCall.Title, labelCap),
		Kind:      line(req.ToolCall.Kind, 40),
	}
	// The ids shown are unique and never empty, so that choosing one always
	// means one option, and "" can only mean cancelled. An option whose own id
	// is empty cannot be told apart from cancelled and is not offered.
	own := map[string]string{}
	for i, o := range req.Options {
		if len(c.Options) == maxOptions {
			break
		}
		if o.OptionID == "" {
			continue
		}
		id := line(o.OptionID, labelCap)
		if id == "" {
			id = "option-" + strconv.Itoa(i+1)
		}
		for _, taken := own[id]; taken; _, taken = own[id] {
			id += "~" + strconv.Itoa(i+1)
		}
		own[id] = o.OptionID
		c.Options = append(c.Options, acp.PermissionOption{OptionID: id, Name: line(o.Name, labelCap), Kind: line(o.Kind, 40)})
	}
	c.Summary = line(c.Kind+": "+c.Tool, labelCap)
	if len(req.ToolCall.Locations) > 0 {
		c.File = line(req.ToolCall.Locations[0].Path, 1024)
		c.Summary = line(strings.TrimSuffix(c.Kind+" "+filepath.Base(c.File), " "), labelCap)
	}
	in := readInput(req.ToolCall.RawInput, b.cfg.ShownInput, c.Kind == "execute")
	c.Command = line(in.command, 4<<10)
	c.CommandText, c.CommandAltered = commandText(in.command)
	c.CommandAltered = c.CommandAltered || in.altered
	c.Cwd = line(in.cwd, 1024)
	c.Notes = in.notes
	if c.Cwd != in.cwd && !slices.Contains(c.Notes, NoteExtra) {
		// Cleaning changed it, so the directory shown is not the one named.
		c.Notes = append(c.Notes[:len(c.Notes):len(c.Notes)], NoteExtra)
	}
	cardContent(&c, req.ToolCall.Content)
	if c.Kind == QuestionKind {
		if b.cfg.AcceptQuestions {
			c.Questions = parseQuestions(req.ToolCall.RawInput)
		} else {
			c.Kind = "other"
		}
	}
	b.mu.Lock()
	c.Seq = nextCardSeq()
	cp := c
	cp.Options = append([]acp.PermissionOption(nil), c.Options...)
	b.pending = append(b.pending, cp)
	b.mu.Unlock()
	return c, own
}

// closeCard removes the card with this Seq, answered or not.
func (b *Bridge) closeCard(seq uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, c := range b.pending {
		if c.Seq == seq {
			b.pending = append(b.pending[:i], b.pending[i+1:]...)
			return
		}
	}
}

// PermissionClosed removes a permission request, answered or not. It takes the
// first card with this RequestID; a Handler removes its own card by Seq.
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
	return handler{b: b, answer: func(ctx context.Context, c Card) Reply { return Reply{OptionID: answer(ctx, c)} }}
}

// AnswerHandler is Handler for answers that carry more than an option id: the
// answers to the questions of a question card. They are checked against the
// card here, and an answer that does not fit is cancelled.
func (b *Bridge) AnswerHandler(answer func(ctx context.Context, card Card) Reply) acp.AnswerHandler {
	return handler{b: b, answer: answer}
}

type handler struct {
	b      *Bridge
	answer func(context.Context, Card) Reply
}

func (h handler) Update(u acp.Update) { h.b.Update(u) }

func (h handler) Permission(ctx context.Context, req acp.PermissionRequest) string {
	id, _ := h.PermissionAnswers(ctx, req)
	return id
}

func (h handler) PermissionAnswers(ctx context.Context, req acp.PermissionRequest) (string, json.RawMessage) {
	card, own := h.b.open(req)
	defer h.b.closeCard(card.Seq)
	reply := h.answer(ctx, card)
	// The agent's own id for the option chosen. "" is cancelled, and so is
	// anything that is not an id on the card.
	id := own[reply.OptionID]
	if id == "" {
		return "", nil
	}
	if card.Kind != QuestionKind {
		if len(reply.Answers) > 0 {
			return "", nil
		}
		return id, nil
	}
	// A question card is answered with its answers, or refused with none. An
	// allowing option without answers that fit, or answers on a refusal, are
	// cancelled: the agent is never told something the person did not say.
	allows, otherAllow := false, false
	for _, o := range card.Options {
		if o.OptionID == reply.OptionID {
			allows = o.Kind == AllowOnce
			otherAllow = !allows && strings.HasPrefix(o.Kind, "allow")
		}
	}
	if !allows {
		if len(reply.Answers) > 0 || otherAllow {
			return "", nil
		}
		return id, nil
	}
	checked, err := CheckAnswers(card.Questions, reply.Answers)
	if err != nil {
		return "", nil
	}
	return id, answersMeta(checked)
}

// Update folds one session/update into the state. It is called in wire order
// and does not block.
func (b *Bridge) Update(u acp.Update) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if u.Replay {
		b.replayUpdate(u)
		return
	}
	if b.replaying && !b.liveSeen {
		// The first update that is not replay: whatever it adds starts a new
		// entry, not more of the last one replayed.
		b.liveSeen = true
		b.closeMerges()
	}
	b.update(u)
}

// update is Update for an update that is not part of a replay. Call with b.mu
// held.
func (b *Bridge) update(u acp.Update) {
	switch u.Kind {
	case acp.UpdateAgentMessageChunk:
		b.chunk(u, transcript.KindReply)
	case acp.UpdateAgentThoughtChunk:
		b.chunk(u, transcript.KindThinking)
	case acp.UpdateToolCall, acp.UpdateToolCallUpdate:
		b.tool(u)
	case acp.UpdatePlan:
		n := len(u.Plan)
		if n > maxPlan {
			n = maxPlan
		}
		b.plan = make([]acp.PlanEntry, 0, n)
		for _, p := range u.Plan[:n] {
			b.plan = append(b.plan, acp.PlanEntry{Content: line(p.Content, labelCap), Priority: line(p.Priority, 40), Status: line(p.Status, 40)})
		}
	case acp.UpdateUserMessageChunk:
		// Ignored. Our own prompt is recorded by TurnStart, and an entry made
		// here would look like something the person typed. A replayed history
		// has these too: replayUpdate takes them, because the client marked them.
	case acp.UpdateUsage:
		if u.Used != nil {
			b.usage.Used = *u.Used
		}
		if u.Size != nil {
			b.usage.Size = *u.Size
		}
		if u.Cost != nil && !math.IsNaN(u.Cost.Amount) && !math.IsInf(u.Cost.Amount, 0) {
			b.usage.Cost, b.usage.Currency = u.Cost.Amount, line(u.Cost.Currency, 16)
		}
	default:
		// Session state, or a kind this bridge does not map, which is ignored.
		b.sessionUpdate(u)
	}
	b.enforce()
}

// chunkText is the text a content block adds to a message.
func chunkText(c *acp.ContentBlock) string {
	switch {
	case c == nil:
		return ""
	case c.Type == "text":
		return clean(capBytes(c.Text, maxText))
	case c.Type != "":
		return "[" + line(c.Type, 40) + "]"
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
		// The only prompt chunks that get here are a replay's.
		it = b.add(transcript.Entry{Kind: kind, Replayed: kind == transcript.KindPrompt})
		it.merges = kind
	}
	it.dirty = true
	switch kind {
	case transcript.KindPrompt:
		if room := maxText - len(it.e.Text); room > 0 {
			it.e.Text += capBytes(text, room)
			b.reinline(it)
		}
	case transcript.KindReply:
		if room := maxText - len(it.e.Markdown); room > 0 {
			it.e.Markdown += capBytes(text, room)
			b.reinline(it)
		}
	case transcript.KindThinking:
		it.e.Chars += utf8.RuneCountInString(text)
		it.e.HasDetail = true
		if room := maxText - len(it.text); room > 0 {
			text = capBytes(text, room)
			it.text += text
			it.detail += len(text)
			b.detail += len(text)
			b.gainedDetail(it)
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
	var it *item
	if u.ToolCallID != "" {
		// Keyed by a hash of the whole id: ids that share a long prefix are
		// still different tools.
		key := sha256.Sum256([]byte(u.ToolCallID))
		it = b.tools[key]
		if it != nil && u.Kind == acp.UpdateToolCall {
			// A second announcement of a known call: the first one stands.
			return
		}
		if it == nil {
			it = b.newTool()
			it.key, it.keyed = key, true
			b.tools[key] = it
		}
	} else {
		// An update with no id cannot be matched to anything.
		it = b.newTool()
	}
	e := &it.e
	if u.ToolKind != "" {
		e.Name = line(u.ToolKind, 40)
	}
	if u.Title != "" {
		e.Label = line(u.Title, labelCap)
	}
	if s := toolStatus(u.Status); s != "" && s != e.Status {
		e.Status = s
		now := b.cfg.Now()
		if now.Sub(it.burstStart) >= time.Second {
			it.burstStart, it.burstN = now, 0
		}
		if it.burstN++; it.burstN <= maxFlaps {
			it.urgent = true
		}
	}
	it.dirty = true
	defer b.reinline(it)

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
			path, newText := line(c.Path, 1024), clean(capBytes(c.NewText, maxText))
			if c.OldText == nil {
				diff += transcript.WriteDiff(path, newText)
			} else {
				diff += transcript.EditDiff(path, clean(capBytes(*c.OldText, maxText)), newText)
			}
		}
	}
	out := strings.Join(texts, "\n")
	if out == "" {
		out = rawOutputText(u.RawOutput)
	}
	out = clean(capBytes(out, maxText))
	diff = capBytes(diff, maxText)

	// An update that repeats what it said before costs a comparison, not a
	// summary of up to 4 MiB.
	k := outKey{set: true, n: len(out), h: maphash.String(b.seed, out), dn: len(diff), dh: maphash.String(b.seed, diff), status: e.Status}
	if it.out == k {
		return
	}
	it.out = k
	b.setOutput(it, out, diff)
}

// newTool adds a tool entry that has no id yet.
func (b *Bridge) newTool() *item {
	b.closeMerges()
	return b.add(transcript.Entry{Kind: transcript.KindTool, Name: "tool", Label: "tool", Status: transcript.StatusRunning})
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
	b.summaries++
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
	b.gainedDetail(it)
	b.trim()
}

// gainedDetail notes that an entry holds detail, so trim looks at it even if
// it is older than entries trim has already passed.
func (b *Bridge) gainedDetail(it *item) {
	if it.detail > 0 && it.seq < b.detailFrom {
		b.detailFrom = it.seq
	}
}

// trim drops the oldest entries' detail until the total is inside the budget.
// The entry stays; it just no longer has a detail to fetch. An old entry that
// gains detail again is found by detailFrom, so the budget holds whatever order
// entries are updated in.
func (b *Bridge) trim() {
	if b.detail <= b.cfg.DetailBudget {
		return
	}
	i := sort.Search(len(b.items), func(i int) bool { return b.items[i].seq >= b.detailFrom })
	for ; i < len(b.items) && b.detail > b.cfg.DetailBudget; i++ {
		it := b.items[i]
		if it.detail == 0 {
			continue
		}
		b.detail -= it.detail
		it.text, it.diff, it.detail = "", "", 0
		it.e.HasDetail = false
		it.dirty = true
	}
	if i < len(b.items) {
		b.detailFrom = b.items[i].seq
	} else {
		b.detailFrom = b.seq + 1
	}
}

// Flush returns the entries that are due. New entries and ones whose status
// changed are due at once, at most 64 per MinInterval and, for status, about
// four changes per entry per second; ones whose content only grew are due once
// MinInterval has passed since they were last handed out. Each is as it
// stands now; an entry updated ten times in the interval is handed out once.
func (b *Bridge) Flush() []transcript.Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.take(false)
}

// take collects due entries. With all, every dirty entry is due. Call with b.mu
// held.
func (b *Bridge) take(all bool) []transcript.Entry {
	now := b.cfg.Now()
	if now.Sub(b.windowStart) >= b.cfg.MinInterval {
		b.windowStart, b.urgentUsed = now, 0
	}
	var out []transcript.Entry
	for _, it := range b.items {
		if !it.dirty {
			continue
		}
		if !all {
			if it.urgent {
				if b.urgentUsed >= maxUrgent {
					continue
				}
				b.urgentUsed++
			} else if now.Sub(it.lastEmit) < b.cfg.MinInterval {
				continue
			}
		}
		it.dirty, it.urgent, it.lastEmit = false, false, now
		out = append(out, it.e)
	}
	return out
}

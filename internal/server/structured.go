package server

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/structured"
	"github.com/jmwri/flockdeck/internal/team"
)

// This file is what a window is told about a structured pane: structuredSnapshot,
// structuredWatch, structuredUnwatch and structuredDetail. They are the backend
// of the minimal drawer. The conversation hub of stage 3 replaces them later, so
// the entries keep the fields of transcript.Entry and add nothing to that
// vocabulary but a flag that says text was cut.
//
// All four are for a window on this machine. A window reached through the relay
// is refused, a full device included, and a viewer is refused by the table of
// commands (commandAccess), not let in as a watcher: the push carries tool
// commands, file paths and reply text that are clipped but not scrubbed, and a
// structured pane has no sealed channel for them as a terminal pane has. The
// refusals are one place each, marked as the place to lift when that changes.
//
// The reference is docs/structured-pane.md and the shapes are in
// docs/structured-pane.schema.json.

// Limits on what is sent. Every cut is made on a character boundary, never in
// the middle of a UTF-8 sequence.
const (
	// structuredEntriesMax is how many entries a push lists, newest last.
	structuredEntriesMax = 50
	// structuredTextCut is how many bytes of a prompt, reply or notice an entry
	// keeps. The rest is fetched with structuredDetail.
	structuredTextCut = 4 << 10
	// structuredLabelCut and structuredNoteCut are the cuts of one-line fields
	// and of a subagent's description.
	structuredLabelCut = 1 << 10
	structuredNoteCut  = 1 << 10
	// structuredPushCap is the most a push is, in bytes. Past it the oldest
	// entries are left out whole and `older` says how many.
	structuredPushCap = 128 << 10
	// structuredDetailCut is the most of an entry's text, and of its diff, that
	// structuredDetail sends.
	structuredDetailCut = 64 << 10
	// structuredPlanMax and structuredPlanCut bound the plan.
	structuredPlanMax = 100
	structuredPlanCut = 200
	// structuredCardCut bounds one field of the permission card, and
	// structuredCommandCut its command.
	structuredCardCut    = 300
	structuredCommandCut = 500
	// structuredErrCut is how many characters the cleaned error keeps.
	structuredErrCut = 200
	// maxStructuredWatches is how many panes may be watched at once, over all
	// windows.
	maxStructuredWatches = 16
	// structuredInterval is the least time between two pushes: two a second.
	structuredInterval = 500 * time.Millisecond
)

const (
	structuredDeskOnly = "A structured pane is used on the machine Flockdeck runs on, not from a window reached through the relay"
	structuredNotPane  = "That pane is not a structured pane"
	structuredTooMany  = "Too many structured panes are being watched. Stop watching one first"
	structuredNoEntry  = "That entry is not there"
)

// structuredEntry is a transcript entry cut down to what a push carries: no
// diff, no report body and no long text. Clipped says something was cut or left
// out, and the entry's detail has the rest.
type structuredEntry struct {
	transcript.Entry
	Clipped bool `json:"clipped,omitempty"`
}

type structuredPlanRow struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"`
	Status   string `json:"status,omitempty"`
}

type structuredOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// structuredCard is the permission request that is open. It is answered with
// permissionAnswer, which is also for a window on this machine only.
type structuredCard struct {
	Seq       uint64             `json:"seq"`
	RequestID string             `json:"requestId"`
	Tool      string             `json:"tool,omitempty"`
	Kind      string             `json:"kind,omitempty"`
	Summary   string             `json:"summary,omitempty"`
	File      string             `json:"file,omitempty"`
	Command   string             `json:"command,omitempty"`
	Options   []structuredOption `json:"options"`
	Clipped   bool               `json:"clipped,omitempty"`
}

type structuredUsage struct {
	Used     int64   `json:"used"`
	Size     int64   `json:"size"`
	Cost     float64 `json:"cost,omitempty"`
	Currency string  `json:"currency,omitempty"`
}

type structuredSetup struct {
	Kind    string `json:"kind"`
	Message string `json:"message,omitempty"`
	Fix     string `json:"fix,omitempty"`
}

// structuredMsg is what structuredSnapshot answers with and what a watching
// window is pushed. Gone is the last message of a watch whose pane has closed.
type structuredMsg struct {
	Type     string              `json:"type"`
	Req      string              `json:"req,omitempty"`
	ID       string              `json:"id"`
	Kind     string              `json:"kind"`
	Gone     bool                `json:"gone,omitempty"`
	Phase    string              `json:"phase,omitempty"`
	Status   string              `json:"status,omitempty"`
	Activity string              `json:"activity,omitempty"`
	Queued   int                 `json:"queued,omitempty"`
	Err      string              `json:"err,omitempty"`
	Setup    *structuredSetup    `json:"setup,omitempty"`
	Entries  []structuredEntry   `json:"entries"`
	Older    int                 `json:"older,omitempty"`
	Plan     []structuredPlanRow `json:"plan"`
	Card     *structuredCard     `json:"card,omitempty"`
	Usage    structuredUsage     `json:"usage"`
}

// structuredDetailMsg answers structuredDetail. Omitted says there was more
// than this version sends: an image's bytes, or nested entries.
type structuredDetailMsg struct {
	Type    string `json:"type"`
	Req     string `json:"req,omitempty"`
	ID      string `json:"id"`
	Entry   string `json:"entry"`
	Text    string `json:"text,omitempty"`
	Diff    string `json:"diff,omitempty"`
	Clipped bool   `json:"clipped,omitempty"`
	Omitted bool   `json:"omitted,omitempty"`
}

// clipStructured makes s fit for a window and cuts it to at most max bytes. It
// replaces bytes that are not UTF-8, removes control characters other than a
// line break and a tab, and cuts only between characters. It says whether
// anything was cut.
func clipStructured(s string, max int) (string, bool) {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	if strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) >= 0 {
		s = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				return -1
			}
			return r
		}, s)
	}
	if len(s) <= max {
		return s, false
	}
	n := max
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

// clipLine is clipStructured for a field that is one line: a line break is a
// space.
func clipLine(s string, max int) (string, bool) {
	s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(s)
	return clipStructured(s, max)
}

// structuredEntryOf cuts one entry down to what a push carries.
func structuredEntryOf(e transcript.Entry) structuredEntry {
	out := structuredEntry{Entry: e}
	cut := func(p *string, max int, line bool) {
		var c bool
		if line {
			*p, c = clipLine(*p, max)
		} else {
			*p, c = clipStructured(*p, max)
		}
		out.Clipped = out.Clipped || c
	}
	cut(&out.ID, structuredLabelCut, true)
	cut(&out.Kind, structuredLabelCut, true)
	cut(&out.TS, structuredLabelCut, true)
	// A prompt, reply or notice cut here is fetched whole (to its own cut) by
	// structuredDetail, so it says so.
	before := out.Clipped
	out.Clipped = false
	cut(&out.Text, structuredTextCut, false)
	cut(&out.Markdown, structuredTextCut, false)
	if out.Clipped {
		out.HasDetail = true
	}
	out.Clipped = out.Clipped || before
	cut(&out.Name, structuredLabelCut, true)
	cut(&out.Label, structuredLabelCut, true)
	cut(&out.Status, structuredLabelCut, true)
	cut(&out.Summary, structuredLabelCut, true)
	cut(&out.Description, structuredNoteCut, false)
	cut(&out.MediaType, structuredLabelCut, true)
	// A diff and a report are fetched, not pushed.
	if out.Diff != "" || out.Report != "" {
		out.Diff, out.Report = "", ""
		out.HasDetail, out.Clipped = true, true
	}
	return out
}

// buildStructured is the message for one runtime's state. It reads nothing but
// the snapshot it is given, which is that one pane's: no other pane's text, no
// mail, and nothing of the agent's environment is in it.
func buildStructured(id string, sn structured.Snapshot) structuredMsg {
	m := structuredMsg{Type: "structured", ID: id, Kind: "structured",
		Phase: string(sn.Phase), Status: string(sn.Status), Queued: sn.Queued,
		Entries: []structuredEntry{}, Plan: []structuredPlanRow{},
		Usage: structuredUsage{Used: sn.Usage.Used, Size: sn.Usage.Size, Cost: sn.Usage.Cost, Currency: sn.Usage.Currency}}
	m.Activity, _ = clipLine(sn.Activity, structuredLabelCut)
	m.Err = team.CleanLine(sn.Err, structuredErrCut)
	if s := sn.Setup; s != nil {
		msg, _ := clipLine(s.Message, structuredCardCut)
		fix, _ := clipLine(s.Login, structuredCardCut)
		m.Setup = &structuredSetup{Kind: s.Kind, Message: msg, Fix: fix}
	}
	for i, p := range sn.Plan {
		if i == structuredPlanMax {
			break
		}
		row := structuredPlanRow{}
		row.Content, _ = clipLine(p.Content, structuredPlanCut)
		row.Priority, _ = clipLine(p.Priority, 40)
		row.Status, _ = clipLine(p.Status, 40)
		m.Plan = append(m.Plan, row)
	}
	if c := sn.Pending; c != nil {
		card := &structuredCard{Seq: c.Seq, RequestID: c.RequestID, Options: []structuredOption{}}
		var cl bool
		field := func(p *string, from string, max int) {
			var c bool
			*p, c = clipLine(from, max)
			cl = cl || c
		}
		field(&card.RequestID, c.RequestID, structuredCardCut)
		field(&card.Tool, c.Tool, structuredCardCut)
		field(&card.Kind, c.Kind, structuredCardCut)
		field(&card.Summary, c.Summary, structuredCardCut)
		field(&card.File, c.File, structuredCardCut)
		field(&card.Command, c.Command, structuredCommandCut)
		for _, o := range c.Options {
			var opt structuredOption
			field(&opt.ID, o.OptionID, 100)
			field(&opt.Name, o.Name, 100)
			field(&opt.Kind, o.Kind, 100)
			card.Options = append(card.Options, opt)
		}
		card.Clipped = cl
		m.Card = card
	}

	// The newest entries, whole ones left out from the oldest end when the push
	// would pass its cap.
	entries := sn.Entries
	older := 0
	if len(entries) > structuredEntriesMax {
		older = len(entries) - structuredEntriesMax
		entries = entries[older:]
	}
	cut := make([]structuredEntry, len(entries))
	sizes := make([]int, len(entries))
	total := 0
	for i, e := range entries {
		cut[i] = structuredEntryOf(e)
		b, _ := json.Marshal(cut[i])
		sizes[i] = len(b) + 1
		total += sizes[i]
	}
	m.Entries = []structuredEntry{}
	// Older is written last and may grow by the entries left out, so the base is
	// measured with it at its largest.
	m.Older = len(sn.Entries)
	base, _ := json.Marshal(m)
	drop := 0
	for drop < len(cut) && len(base)+total > structuredPushCap {
		total -= sizes[drop]
		drop++
	}
	m.Entries = cut[drop:]
	m.Older = older + drop
	return m
}

// structuredDetailOf is the answer for one entry of a runtime.
func structuredDetailOf(id, entry string, rt *structured.Runtime) (structuredDetailMsg, bool) {
	m := structuredDetailMsg{Type: "structuredDetail", ID: id, Entry: entry}
	var inline *transcript.Entry
	for _, e := range rt.Snapshot().Entries {
		if e.ID == entry {
			e := e
			inline = &e
			break
		}
	}
	if inline == nil {
		return m, false
	}
	text, diff := "", inline.Diff
	switch inline.Kind {
	case transcript.KindPrompt, transcript.KindNotice:
		text = inline.Text
	case transcript.KindReply:
		text = inline.Markdown
	}
	if d, ok := rt.Detail(entry); ok {
		if d.Text != "" {
			text = d.Text
		}
		if d.Diff != "" {
			diff = d.Diff
		}
		m.Omitted = d.Data != "" || len(d.Entries) > 0
	}
	var c1, c2 bool
	m.Text, c1 = clipStructured(text, structuredDetailCut)
	m.Diff, c2 = clipStructured(diff, structuredDetailCut)
	m.Clipped = c1 || c2
	return m, true
}

// structuredCommand answers the structured pane commands.
func (s *Server) structuredCommand(c *controlClient, cmd command) {
	// structured-pane-relay: the refusal to lift when a sealed channel exists.
	if c.remote {
		c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, Error: structuredDeskOnly})
		c.notify(structuredDeskOnly, true)
		return
	}
	switch cmd.Cmd {
	case "structuredSnapshot":
		type out struct {
			data []byte
			ok   bool
		}
		r, ok := ask(s, func() out {
			rt := s.ws.StructuredRuntime(cmd.ID)
			if rt == nil {
				return out{}
			}
			m := buildStructured(cmd.ID, rt.Snapshot())
			m.Req = cmd.Req
			data, err := json.Marshal(m)
			return out{data, err == nil}
		})
		if !ok {
			return
		}
		if !r.ok {
			refuse(c, cmd, errStructured(structuredNotPane))
			return
		}
		c.send(r.data)
	case "structuredWatch":
		s.do(func() { s.structuredWatch(c, cmd) })
	case "structuredUnwatch":
		s.do(func() { c.structID, c.structSent = "", nil })
	case "structuredDetail":
		type out struct {
			m  structuredDetailMsg
			ok int
		}
		r, ok := ask(s, func() out {
			rt := s.ws.StructuredRuntime(cmd.ID)
			if rt == nil {
				return out{ok: -1}
			}
			m, found := structuredDetailOf(cmd.ID, cmd.Entry, rt)
			if !found {
				return out{ok: 0}
			}
			return out{m, 1}
		})
		if !ok {
			return
		}
		switch r.ok {
		case -1:
			refuse(c, cmd, errStructured(structuredNotPane))
		case 0:
			refuse(c, cmd, errStructured(structuredNoEntry))
		default:
			r.m.Req = cmd.Req
			c.sendJSON(r.m)
		}
	}
}

type errStructured string

func (e errStructured) Error() string { return string(e) }

// structuredWatch subscribes the window to one pane, replacing any it watched.
// It must run on the workspace goroutine.
func (s *Server) structuredWatch(c *controlClient, cmd command) {
	rt := s.ws.StructuredRuntime(cmd.ID)
	if rt == nil {
		c.structID, c.structSent = "", nil
		refuse(c, cmd, errStructured(structuredNotPane))
		return
	}
	watched := map[string]bool{cmd.ID: true}
	for _, o := range s.greetedClients() {
		if o != c && o.structID != "" {
			watched[o.structID] = true
		}
	}
	if len(watched) > maxStructuredWatches {
		c.structID, c.structSent = "", nil
		refuse(c, cmd, errStructured(structuredTooMany))
		return
	}
	m := buildStructured(cmd.ID, rt.Snapshot())
	plain, err := json.Marshal(m)
	if err != nil {
		return
	}
	m.Req = cmd.Req
	first, err := json.Marshal(m)
	if err != nil {
		return
	}
	c.structID, c.structSent = cmd.ID, nil
	if c.trySend(first) {
		c.structSent = plain
		// The reply counts as a push, so a change within the interval waits for
		// the interval instead of making a second one at once.
		s.structAt = time.Now()
	} else {
		s.armStructured()
	}
}

// armStructured arranges for a push in a moment, once.
func (s *Server) armStructured() {
	if s.structArmed {
		return
	}
	s.structArmed = true
	wait := max(structuredInterval-time.Since(s.structAt), 50*time.Millisecond)
	time.AfterFunc(wait, func() {
		s.do(func() {
			s.structArmed = false
			s.pushStructured()
		})
	})
}

// pushStructured sends each window watching a structured pane that pane's state
// when it has changed, at most twice a second; a push that comes too soon is
// made when the interval is up. It runs on the workspace goroutine and is called
// with each state push. A pane that is gone is told to the window once, as a
// message with gone set, and the watch ends. A window whose queue was full is
// sent the state again rather than the push being skipped as unchanged.
func (s *Server) pushStructured() {
	clients := s.greetedClients()
	watching := false
	for _, c := range clients {
		if c.structID != "" {
			watching = true
		}
	}
	if !watching {
		return
	}
	if time.Since(s.structAt) < structuredInterval {
		s.armStructured()
		return
	}
	s.structAt = time.Now()
	cache := map[string][]byte{}
	retry := false
	for _, c := range clients {
		id := c.structID
		if id == "" {
			continue
		}
		data, ok := cache[id]
		if !ok {
			if rt := s.ws.StructuredRuntime(id); rt != nil {
				data, _ = json.Marshal(buildStructured(id, rt.Snapshot()))
			} else {
				data, _ = json.Marshal(structuredMsg{Type: "structured", ID: id, Kind: "structured", Gone: true,
					Entries: []structuredEntry{}, Plan: []structuredPlanRow{}})
			}
			cache[id] = data
		}
		if bytes.Equal(data, c.structSent) {
			continue
		}
		if c.trySend(data) {
			c.structSent = data
			if s.ws.StructuredRuntime(id) == nil {
				c.structID, c.structSent = "", nil
			}
		} else {
			retry = true
		}
	}
	if retry {
		s.armStructured()
	}
}

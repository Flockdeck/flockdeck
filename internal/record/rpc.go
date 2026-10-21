package record

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// A stream recording is the JSON-RPC traffic between Flockdeck and a structured
// agent (see internal/acp), one JSON object per line. It lives beside the
// terminal recordings of the project, in a file whose name ends in
// ".rpc.jsonl", and is counted in the same retention. The line format is in
// docs/rpc-recording-format.md and docs/rpc-recording-line.schema.json.
//
// The lines are prompts, file contents and tool output, so the file is as
// sensitive as a terminal recording: it is made owner-only, it passes through
// Redact and the field limit before anything reaches the disk, and nothing
// here sends it anywhere.

// RPCVersion is the "v" of a stream recording's lines.
const RPCVersion = 1

// Line types of a stream recording.
const (
	TypeRPCStarted = "rpc_started"
	TypeRPCMessage = "rpc_message"
	TypeRPCDropped = "rpc_dropped"
	TypeRPCGap     = "rpc_gap"
	TypeRPCStopped = "rpc_stopped"
)

// Directions of a message: In came from the agent, Out was sent to it.
const (
	RPCIn  = "in"
	RPCOut = "out"
)

const (
	rpcExt = ".rpc.jsonl"

	// MaxRPCFileBytes caps one stream recording. At the cap the oldest lines
	// are dropped, down to half of it, and a rpc_dropped line says how many.
	MaxRPCFileBytes = 16 << 20
	// DefaultRPCInterval is the longest a coalesced message is held back.
	DefaultRPCInterval = 250 * time.Millisecond

	// rpcMaxRaw is the most of one line of traffic that is parsed. A longer
	// one is recorded as a note saying how long it was. Parsing costs about a
	// hundred times the line's length in memory, so this is small.
	rpcMaxRaw = 1 << 20
	// rpcMaxElements is the most array items and object members a line may
	// have outside its strings: a line of a million small numbers is under
	// rpcMaxRaw and still costs far more to parse than a line of text.
	rpcMaxElements = 50000
	// rpcHoldBack is the most of the end of a message's text that is kept back
	// when a held line is written, so that a secret split between two chunks is
	// found whole when the second arrives.
	rpcHoldBack = 256
	// rpcMaxHeld bounds the kinds of message text with something held back.
	rpcMaxHeld = 16
	// rpcContextBytes is how much of the text already written is kept, for each
	// kind of message text, so that what follows it is redacted with it in view:
	// "password:" in one chunk and the password in the next, or the first lines
	// of a private key and the rest. A label further back than this is not seen.
	// A private key whose BEGIN line is further back and which has not ended
	// keeps that one line (see contextTail).
	rpcContextBytes = 16 << 10
	// rpcBufferBytes is how much is held before the file is written.
	rpcBufferBytes = 64 << 10
)

// RPCLine is one line of a stream recording.
type RPCLine struct {
	V   int   `json:"v"`
	Seq int64 `json:"seq"`
	// Time is when the message was seen; for a coalesced one, the first of the
	// messages it stands for, and Until the last.
	Time    string `json:"time"`
	Until   string `json:"until,omitempty"`
	Merged  int    `json:"merged,omitempty"`
	Session string `json:"session"`
	Type    string `json:"type"`
	Dir     string `json:"dir,omitempty"`
	// Msg is the JSON-RPC message as it was, after redaction and clipping.
	Msg json.RawMessage `json:"msg,omitempty"`
	// Raw is a line that was not a JSON object, clipped.
	Raw string `json:"raw,omitempty"`
	// Text says what a rpc_started, rpc_dropped, rpc_gap or rpc_stopped line is, or what
	// became of a message too long to keep.
	Text         string `json:"text,omitempty"`
	Project      string `json:"project,omitempty"`
	Agent        string `json:"agent,omitempty"`
	Conversation string `json:"conversation,omitempty"`
	// Dropped is the number of lines a rpc_dropped or rpc_gap line stands for.
	Dropped int64 `json:"dropped,omitempty"`
	// Redacted says Redact replaced something in the message. Clipped is the
	// number of strings that were cut.
	Redacted bool `json:"redacted,omitempty"`
	Clipped  int  `json:"clipped,omitempty"`
}

// RPCHook is the place to clip or remove fields of a message that Redact and
// the field limit cannot know about. It is given the decoded message after both,
// may change it in place, and reports whether to keep it. It runs once for each
// line written, on the coalesced message, with the recorder's lock held, so it
// must be quick and must not call the recorder. A panic drops the message.
type RPCHook func(dir string, msg map[string]any) (keep bool)

// RPCConfig describes a stream recording.
type RPCConfig struct {
	Meta Meta
	// MaxBytes lowers the cap from MaxRPCFileBytes.
	MaxBytes int64
	// Interval is the longest a coalesced message is held. Default 250ms.
	Interval time.Duration
	Hook     RPCHook
	// Now is the clock for the lines' times, for tests. It must be safe for
	// concurrent calls: Tap and Gap read it before they take the recorder's lock.
	Now func() time.Time
}

// RPCRecorder writes one stream recording. Tap is safe for concurrent use.
type RPCRecorder struct {
	cfg  RPCConfig
	m    *Manager
	path string
	id   string

	mu      sync.Mutex
	f       *os.File
	w       *bufio.Writer
	seq     int64
	size    int64 // bytes written or buffered
	pend    *pending
	timer   *time.Timer
	closed  bool
	err     error
	dropped int64 // lines dropped over the life of the file
	// held is, for each kind of message text, the end of the last line written
	// of it, kept back unwritten until more of that text arrives or the
	// recording closes. See splitHeld.
	held map[string]*held
}

// held is the state of a run of message text: the end of it not yet written,
// and the last of what was.
type held struct {
	ctx         string // the end of the raw text already written, up to rpcContextBytes
	tail        string
	tmpl        map[string]any // the message the text was in, with its text emptied
	dir         string
	merged      int // the messages the text so far stands for
	first, last time.Time
}

type pending struct {
	key       string
	kind      string // "chunk" or "tool"
	msg       map[string]any
	time      time.Time
	until     time.Time
	merged    int
	dir       string
	textBytes int
}

// StartRPC opens a stream recording beside the project's terminal recordings,
// applies the retention rule to the folder, and writes the opening line.
func (m *Manager) StartRPC(cfg RPCConfig) (*RPCRecorder, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultRPCInterval
	}
	if cfg.MaxBytes <= 0 || cfg.MaxBytes > MaxRPCFileBytes {
		cfg.MaxBytes = MaxRPCFileBytes
	}
	// Nothing is opened after the Manager is closed. Whether a recording is to
	// be made at all is the caller's to decide: a pane with recording off never
	// calls this, and a relay window cannot turn it on (docs/rpc-recording-format.md).
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return nil, errors.New("the recorder is closed")
	}
	base, err := m.Dir()
	if err != nil {
		return nil, err
	}
	first := cfg.Now()
	id := SessionID(first, cfg.Meta) + ".rpc"
	folder := filepath.Join(base, recordingsDir, Folder(cfg.Meta.Project, cfg.Meta.ProjectRoot))
	if err := os.MkdirAll(folder, folderMode); err != nil {
		return nil, fmt.Errorf("create the recordings folder: %w", err)
	}
	var f *os.File
	var path string
	for n := 1; ; n++ {
		name := id
		if n > 1 {
			name += "-" + strconv.Itoa(n)
		}
		path = filepath.Join(folder, name+sessionFileExt)
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
		if err == nil {
			id = name
			break
		}
		if !errors.Is(err, os.ErrExist) || n > 50 {
			return nil, fmt.Errorf("create the stream recording: %w", err)
		}
	}
	r := &RPCRecorder{cfg: cfg, m: m, path: path, id: id, f: f, w: bufio.NewWriterSize(f, rpcBufferBytes), held: map[string]*held{}}
	m.mu.Lock()
	if m.closed {
		// Closed while the file was being made.
		m.mu.Unlock()
		_ = f.Close()
		_ = os.Remove(path)
		return nil, errors.New("the recorder is closed")
	}
	m.rpcs[r] = true
	m.sweepStale(folder)
	prune(folder, m.protectedLocked(path), m.now())
	m.mu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	r.commitLocked(RPCLine{Time: stamp(first), Type: TypeRPCStarted, Text: "start of the stream recording",
		Project: cfg.Meta.Project, Agent: cfg.Meta.Agent, Conversation: cutID(cfg.Meta.Conversation)})
	return r, r.err
}

// Path is the recording's file.
func (r *RPCRecorder) Path() string { return r.path }

// Tap is given each line of traffic: dir is RPCIn for what the agent sent and
// RPCOut for what was sent to it, raw is one JSON-RPC message without its
// newline. raw is not kept. It does no file I/O except when the buffer fills.
func (r *RPCRecorder) Tap(dir string, raw []byte) {
	now := r.cfg.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.err != nil {
		return
	}
	if len(raw) > rpcMaxRaw || elementsOver(raw, rpcMaxElements) {
		// Not parsed: what it would cost is out of proportion to its worth.
		r.flushPendingLocked()
		r.commitLocked(RPCLine{Time: stamp(now), Type: TypeRPCMessage, Dir: dir, Clipped: 1,
			Text: fmt.Sprintf("a message of %d bytes was not recorded", len(raw))})
		return
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil || dec.More() {
		v = nil
	}
	msg, ok := v.(map[string]any)
	if !ok {
		r.flushPendingLocked()
		clipped := 0
		s := string(raw)
		if len(s) > MaxFieldBytes {
			clipped = 1
		}
		red := Redact(s)
		r.commitLocked(RPCLine{Time: stamp(now), Type: TypeRPCMessage, Dir: dir, Raw: Clip(red, MaxFieldBytes), Clipped: clipped, Redacted: red != s})
		return
	}

	if dir == RPCIn {
		if key, kind, ok := coalesceKey(msg); ok {
			if p := r.pend; p != nil && p.key == key {
				// A line of message text holds at most MaxMessageBytes: the next
				// piece starts another.
				if kind != "chunk" || p.textBytes+len(chunkTextOf(msg)) <= MaxMessageBytes {
					r.mergeLocked(p, msg, kind, now)
					return
				}
			}
			r.flushPendingLocked()
			p := &pending{key: key, kind: kind, msg: msg, time: now, until: now, merged: 1, dir: dir}
			p.textBytes = len(chunkTextOf(msg))
			r.pend = p
			r.armLocked()
			return
		}
	}
	r.flushPendingLocked()
	r.commitMsgLocked(dir, msg, now, now, 1, "")
}

// Gap records that n lines of traffic were not given to Tap, as when the
// client had to drop lines it could not hand over in time (acp.Client.TapDropped).
// It writes one rpc_gap line at the place in the stream where they were lost,
// after any text held back, which ends the run of text it was in. What was
// written of that text stays as the context the text after the gap is redacted
// with, so a label before the gap still finds its value after it.
// It writes nothing for n of zero or less, and nothing once the recording is
// closed or has failed.
func (r *RPCRecorder) Gap(n int) {
	if n <= 0 {
		return
	}
	now := r.cfg.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.err != nil {
		return
	}
	// Text on the two sides of the gap is not one run: what came before it is
	// written first, so that the gap sits where the lines were lost.
	r.flushPendingLocked()
	r.emitHeldLocked(true)
	r.commitLocked(RPCLine{Time: stamp(now), Type: TypeRPCGap, Dropped: int64(n),
		Text: fmt.Sprintf("%d lines of traffic were not recorded", n)})
}

// Flush writes whatever is held to the file.
func (r *RPCRecorder) Flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flushPendingLocked()
	r.flushFileLocked()
	return r.err
}

// Close writes the closing line and closes the file. It is safe to call twice.
func (r *RPCRecorder) Close() error {
	err := r.closeOnce()
	// Leave the Manager's set, after the recorder's own lock is released.
	r.m.mu.Lock()
	delete(r.m.rpcs, r)
	r.m.mu.Unlock()
	return err
}

func (r *RPCRecorder) closeOnce() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return r.err
	}
	r.flushPendingFinalLocked()
	r.emitHeldLocked(false)
	r.commitLocked(RPCLine{Time: stamp(r.cfg.Now()), Type: TypeRPCStopped, Text: "end of the stream recording"})
	r.closed = true
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	r.flushFileLocked()
	if err := r.f.Close(); err != nil && r.err == nil {
		r.err = err
	}
	return r.err
}

// emitHeldLocked writes, for each kind of text with something kept back, a line
// with the end of it, redacted. With keepCtx the kind stays in r.held with what
// has been written of it as its context and nothing kept back, so that text
// which arrives later is redacted with that in view; without it the kind is let
// go.
func (r *RPCRecorder) emitHeldLocked(keepCtx bool) {
	keys := make([]string, 0, len(r.held))
	for k := range r.held {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h := r.held[k]
		delete(r.held, k)
		if h.tail != "" {
			setChunkText(h.tmpl, h.tail)
			r.commitMsgLocked(h.dir, h.tmpl, h.first, h.last, max(h.merged, 1), h.ctx)
		}
		if keepCtx && h.ctx+h.tail != "" {
			// first is zero: the next piece of this text has its own time.
			r.held[k] = &held{ctx: contextTail(h.ctx + h.tail), dir: h.dir}
		}
	}
}

// armLocked makes sure what is held reaches the file within Interval.
func (r *RPCRecorder) armLocked() {
	if r.timer != nil {
		return
	}
	r.timer = time.AfterFunc(r.cfg.Interval, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.timer = nil
		if r.closed {
			return
		}
		r.flushPendingLocked()
		r.flushFileLocked()
	})
}

func (r *RPCRecorder) flushFileLocked() {
	if r.err != nil {
		return
	}
	if err := r.w.Flush(); err != nil {
		r.err = err
	}
}

// coalesceKey says whether msg is an update that may be merged with the ones
// next to it: the chunks of one kind of message in one session, or the updates
// of one tool call. It is the same rule the transcript bridge applies.
func coalesceKey(msg map[string]any) (key, kind string, ok bool) {
	if s, _ := msg["method"].(string); s != "session/update" {
		return "", "", false
	}
	params, _ := msg["params"].(map[string]any)
	update, _ := params["update"].(map[string]any)
	sid, _ := params["sessionId"].(string)
	k, _ := update["sessionUpdate"].(string)
	switch k {
	case "agent_message_chunk", "agent_thought_chunk", "user_message_chunk":
		content, _ := update["content"].(map[string]any)
		if t, _ := content["type"].(string); t != "text" {
			return "", "", false
		}
		if _, ok := content["text"].(string); !ok {
			return "", "", false
		}
		return k + "\x00" + sid, "chunk", true
	case "tool_call_update":
		id, _ := update["toolCallId"].(string)
		if id == "" {
			return "", "", false
		}
		return k + "\x00" + sid + "\x00" + id, "tool", true
	}
	return "", "", false
}

func updateOf(msg map[string]any) map[string]any {
	params, _ := msg["params"].(map[string]any)
	u, _ := params["update"].(map[string]any)
	return u
}

func chunkTextOf(msg map[string]any) string {
	content, _ := updateOf(msg)["content"].(map[string]any)
	s, _ := content["text"].(string)
	return s
}

// mergeLocked folds msg, which has p's key, into p: chunks add their text, tool
// updates replace the fields they carry.
func (r *RPCRecorder) mergeLocked(p *pending, msg map[string]any, kind string, now time.Time) {
	p.until = now
	p.merged++
	switch kind {
	case "chunk":
		content, _ := updateOf(p.msg)["content"].(map[string]any)
		add := chunkTextOf(msg)
		content["text"] = content["text"].(string) + add
		p.textBytes += len(add)
	case "tool":
		dst := updateOf(p.msg)
		for k, v := range updateOf(msg) {
			dst[k] = v
		}
	}
}

// flushPendingLocked writes the held line, except the end of its text (see
// splitHeld), which waits for the text that follows it.
func (r *RPCRecorder) flushPendingLocked() { r.flushPending(false) }

// flushPendingFinalLocked writes the held line whole.
func (r *RPCRecorder) flushPendingFinalLocked() { r.flushPending(true) }

func (r *RPCRecorder) flushPending(final bool) {
	p := r.pend
	if p == nil {
		return
	}
	r.pend = nil
	if p.kind != "chunk" {
		r.commitMsgLocked(p.dir, p.msg, p.time, p.until, p.merged, "")
		return
	}
	// A secret can begin in one chunk and end in the next, with this line
	// written, or other messages, in between. So the end of every line of text
	// is kept back, and put in front of the next piece of the same text before
	// it is redacted. Nothing of a key that is still arriving is on the disk.
	text, first, total, ctx := chunkTextOf(p.msg), p.time, p.merged, ""
	if prev := r.held[p.key]; prev != nil {
		text, total, ctx = prev.tail+text, total+prev.merged, prev.ctx
		if !prev.first.IsZero() {
			first = prev.first
		}
		delete(r.held, p.key)
	}
	head, tail := text, ""
	if !final {
		head, tail = splitHeld(text)
	}
	if !final && (tail != "" || head != "") {
		if len(r.held) >= rpcMaxHeld {
			// Too many kinds of text at once: what is held is written, and the
			// context of every kind is let go.
			r.emitHeldLocked(false)
		}
		h := &held{ctx: contextTail(ctx + head), tail: tail, dir: p.dir, first: p.until, last: p.until}
		if tail != "" {
			h.tmpl = cloneMsg(p.msg)
		}
		switch {
		case head == "":
			// Nothing is written, so the line that ends up carrying this text
			// is the one that stands for all of it.
			h.first, h.merged = first, total
		case tail != "" && total > 1:
			// The text is written as two lines, this one and the one the end of
			// it will be in. Each message is counted once: the last one, whose
			// text the end belongs to, goes to that line. A single message split
			// in two is counted by both, since a line cannot stand for none.
			h.merged = 1
			total--
		}
		r.held[p.key] = h
	}
	if head == "" {
		return
	}
	setChunkText(p.msg, head)
	r.commitMsgLocked(p.dir, p.msg, first, p.until, total, ctx)
}

// splitHeld cuts text into what can be written now and the end of it, which is
// kept back: the last two words, so that "password: " and what follows it, or
// half of a key, stay together, and never more than rpcHoldBack bytes.
func splitHeld(text string) (head, tail string) {
	isSpace := func(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }
	i := len(text)
	for words := 0; words < 2; words++ {
		for i > 0 && isSpace(text[i-1]) {
			i--
		}
		for i > 0 && !isSpace(text[i-1]) {
			i--
		}
	}
	if len(text)-i > rpcHoldBack {
		i = len(text) - rpcHoldBack
		for i < len(text) && !utf8.RuneStart(text[i]) {
			i++
		}
	}
	return text[:i], text[i:]
}

// tailBytes is the last n bytes of s, starting at a character.
func tailBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}

// contextTail is the end of s that is kept as context, rpcContextBytes of it.
// When a private key began further back than that and has not ended, its BEGIN
// line is kept in front, so that the rest of the key, still arriving, is seen
// as part of it and not as text with nothing secret about it. The line is a few
// dozen bytes.
func contextTail(s string) string {
	tail := tailBytes(s, rpcContextBytes)
	cut := len(s) - len(tail)
	if cut == 0 {
		return tail
	}
	for _, sp := range findPEM(s, nil) {
		if sp.Start >= cut || sp.End <= cut || strings.Contains(s[sp.Start:sp.End], "-----END ") {
			continue
		}
		if h, ok := pemLabel(s, sp.Start+len("-----BEGIN ")); ok && h-sp.Start <= 128 {
			return s[sp.Start:h] + "\n" + tail
		}
	}
	return tail
}

// redactAfter redacts text as it would be if it followed ctx, which is already
// written, and returns only the part that follows. The two are redacted
// together so that a label in ctx and its value in text, or the start of a
// private key in ctx and the rest of it in text, are found; what is returned is
// what redacting text alone would have missed. Where the redacted ctx and the
// redacted whole part ways, the whole's text from there on is returned, which
// can repeat a few characters of ctx but never shows a secret.
func redactAfter(ctx, text string) string {
	if ctx == "" {
		return Redact(text)
	}
	before, whole := Redact(ctx), Redact(ctx+text)
	n := 0
	for n < len(before) && n < len(whole) && before[n] == whole[n] {
		n++
	}
	for n > 0 && n < len(whole) && !utf8.RuneStart(whole[n]) {
		n--
	}
	return whole[n:]
}

func setChunkText(msg map[string]any, s string) {
	if content, _ := updateOf(msg)["content"].(map[string]any); content != nil {
		content["text"] = s
	}
}

// cloneMsg copies a decoded JSON value.
func cloneMsg(m map[string]any) map[string]any {
	out, _ := cloneValue(m).(map[string]any)
	return out
}

func cloneValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = cloneValue(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = cloneValue(e)
		}
		return out
	}
	return v
}

// elementsOver reports whether a JSON text has more than limit array items and
// object members outside its strings, counted by its commas. It reads the text
// once and builds nothing.
func elementsOver(raw []byte, limit int) bool {
	n, inString, escaped := 0, false, false
	for _, c := range raw {
		switch {
		case escaped:
			escaped = false
		case inString:
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
		case c == '"':
			inString = true
		case c == ',':
			if n++; n > limit {
				return true
			}
		}
	}
	return false
}

// redactKeys redacts and clips the keys of every object in a decoded JSON
// value: a key that holds a secret is replaced whole, and a key over 256 bytes
// is cut. Keys that come out the same are told apart by a number.
func redactKeys(v any, clipped *int) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = redactKeys(e, clipped)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			nk := k
			if len(nk) > 256 {
				nk = Clip(nk, 256)
				*clipped++
			}
			if Redact(nk) != nk {
				nk = Redacted
			}
			base := nk
			for n := 2; ; n++ {
				if _, taken := out[nk]; !taken {
					break
				}
				nk = fmt.Sprintf("%s#%d", base, n)
			}
			out[nk] = redactKeys(e, clipped)
		}
		return out
	}
	return v
}

// commitMsgLocked redacts, clips and hooks a message and writes it. ctx is the
// end of the text of the same kind already written, which the message text is
// redacted after; "" for a message that continues no text.
func (r *RPCRecorder) commitMsgLocked(dir string, msg map[string]any, first, last time.Time, merged int, ctx string) {
	line := RPCLine{Time: stamp(first), Type: TypeRPCMessage, Dir: dir}
	if merged > 1 {
		line.Merged, line.Until = merged, stamp(last)
	}
	var v any = msg
	// Message text is the point of a chunk or a prompt and gets the larger limit,
	// kept apart from the generic clip.
	texts := takeMessageTexts(msg)
	clipped := 0
	v = redactKeys(v, &clipped)
	v = RedactValue(v)
	v = clipCount(v, MaxFieldBytes, &clipped)
	out, _ := v.(map[string]any)
	for _, t := range texts {
		t.text = redactAfter(ctx, t.text)
		if len(t.text) > MaxMessageBytes {
			clipped++
		}
		t.set(out, Clip(t.text, MaxMessageBytes))
	}
	if keep, ok := r.runHook(dir, out); !ok || !keep {
		return
	}
	b, err := json.Marshal(out)
	if err != nil {
		return
	}
	line.Redacted = bytes.Contains(b, []byte(Redacted))
	line.Clipped = clipped
	if int64(len(b)) > r.cfg.MaxBytes/4 {
		line.Text, line.Clipped, line.Msg = fmt.Sprintf("a message of %d bytes was not recorded", len(b)), line.Clipped+1, nil
	} else {
		line.Msg = b
	}
	r.commitLocked(line)
}

func (r *RPCRecorder) runHook(dir string, msg map[string]any) (keep, ok bool) {
	if r.cfg.Hook == nil {
		return true, true
	}
	defer func() {
		if recover() != nil {
			keep, ok = false, false
		}
	}()
	return r.cfg.Hook(dir, msg), true
}

// A messageText is a string of a message that is a person's or the model's own
// words, which has the larger limit.
type messageText struct {
	text string
	set  func(root map[string]any, s string)
}

// takeMessageTexts removes the message text from a chunk update or a prompt
// request and returns it, so that the generic limit does not cut it short.
func takeMessageTexts(msg map[string]any) []messageText {
	var out []messageText
	if u := updateOf(msg); u != nil {
		if content, _ := u["content"].(map[string]any); content != nil {
			if k, _ := u["sessionUpdate"].(string); strings.HasSuffix(k, "_message_chunk") || k == "agent_thought_chunk" {
				if s, ok := content["text"].(string); ok {
					content["text"] = ""
					out = append(out, messageText{s, func(root map[string]any, s string) {
						if c, _ := updateOf(root)["content"].(map[string]any); c != nil {
							c["text"] = s
						}
					}})
				}
			}
		}
	}
	if m, _ := msg["method"].(string); m == "session/prompt" {
		params, _ := msg["params"].(map[string]any)
		blocks, _ := params["prompt"].([]any)
		for i, b := range blocks {
			bm, _ := b.(map[string]any)
			if s, ok := bm["text"].(string); ok {
				bm["text"] = ""
				idx := i
				out = append(out, messageText{s, func(root map[string]any, s string) {
					p, _ := root["params"].(map[string]any)
					bl, _ := p["prompt"].([]any)
					if idx < len(bl) {
						if x, _ := bl[idx].(map[string]any); x != nil {
							x["text"] = s
						}
					}
				}})
			}
		}
	}
	return out
}

// clipCount is ClipValue that counts the strings it cut.
func clipCount(v any, n int, count *int) any {
	switch x := v.(type) {
	case string:
		if len(x) > n {
			*count++
		}
		return Clip(x, n)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = clipCount(e, n, count)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = clipCount(e, n, count)
		}
		return out
	}
	return v
}

// commitLocked gives a line its number and session, and writes it. At the cap
// the oldest lines go first.
func (r *RPCRecorder) commitLocked(l RPCLine) {
	if r.err != nil {
		return
	}
	l.V, l.Session = RPCVersion, r.id
	l.Seq = r.seq + 1
	b, err := json.Marshal(l)
	if err != nil {
		return
	}
	b = append(b, '\n')
	if r.size+int64(len(b)) > r.cfg.MaxBytes {
		r.rotateLocked()
		if r.err != nil {
			return
		}
	}
	n, err := r.w.Write(b)
	if err != nil {
		r.err = err
		return
	}
	r.size += int64(n)
	r.seq++
	if r.w.Buffered() > 0 && r.timer == nil && !r.closed {
		r.armLocked()
	}
}

// rotateLocked drops the oldest lines to make room: the file is rewritten with
// its opening line, a rpc_dropped line, and the newest lines that fit in half
// the cap, and replaces the old file.
func (r *RPCRecorder) rotateLocked() {
	if err := r.w.Flush(); err != nil {
		r.err = err
		return
	}
	if err := r.f.Close(); err != nil {
		r.err = err
		return
	}
	data, err := os.ReadFile(r.path)
	if err != nil {
		r.err = err
		return
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	if n := len(lines); n > 0 && len(lines[n-1]) == 0 {
		lines = lines[:n-1]
	}
	if len(lines) < 2 {
		r.err = errors.New("the stream recording is smaller than one line")
		return
	}
	header, body := lines[0], lines[1:]
	// An earlier note of what was dropped is replaced by a new one that adds up.
	var dropped, lastSeq int64
	if len(body) > 0 {
		var l RPCLine
		if json.Unmarshal(body[0], &l) == nil && l.Type == TypeRPCDropped {
			dropped = l.Dropped
			body = body[1:]
		}
	}
	note := func(d, seq int64) []byte {
		b, _ := json.Marshal(RPCLine{V: RPCVersion, Seq: seq, Time: stamp(r.cfg.Now()), Session: r.id, Type: TypeRPCDropped, Dropped: d,
			Text: fmt.Sprintf("%d older lines were dropped to keep the file within its size cap", d)})
		return append(b, '\n')
	}
	target := r.cfg.MaxBytes/2 - int64(len(header)) - int64(len(note(dropped+int64(len(body)), 1<<40)))
	keepFrom := len(body)
	var kept int64
	for i := len(body) - 1; i >= 0; i-- {
		if kept+int64(len(body[i])) > target {
			break
		}
		kept += int64(len(body[i]))
		keepFrom = i
	}
	for _, b := range body[:keepFrom] {
		var l RPCLine
		if json.Unmarshal(b, &l) == nil && l.Seq > lastSeq {
			lastSeq = l.Seq
		}
	}
	dropped += int64(keepFrom)
	r.dropped = dropped

	var out bytes.Buffer
	out.Write(header)
	out.Write(note(dropped, lastSeq))
	for _, b := range body[keepFrom:] {
		out.Write(b)
	}
	tmp := tempName(r.path)
	if err := writeSynced(tmp, out.Bytes()); err != nil {
		_ = os.Remove(tmp)
		r.err = err
		return
	}
	if err := renameRetry(tmp, r.path, 20); err != nil {
		_ = os.Remove(tmp)
		r.err = err
		return
	}
	syncDir(filepath.Dir(r.path))
	f, err := os.OpenFile(r.path, os.O_WRONLY|os.O_APPEND, fileMode)
	if err != nil {
		r.err = err
		return
	}
	r.f, r.w, r.size = f, bufio.NewWriterSize(f, rpcBufferBytes), int64(out.Len())
}

// writeSynced writes a new file and flushes it to the disk before returning, so
// that the rename that puts it in place cannot leave an empty file after a
// crash.
func writeSynced(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// RPCFile is a stream recording read back.
type RPCFile struct {
	Lines []RPCLine
	// Damaged counts the lines that could not be read: a line that is not a
	// JSON object, and a last line cut short by a crash.
	Damaged int
	// TornTail says the last line had no newline or could not be read, which is
	// what a crash while writing leaves.
	TornTail bool
}

// MaxRPCLineBytes is the longest line ReadRPC will take; a longer one counts as
// damaged and is skipped.
const MaxRPCLineBytes = 8 << 20

// MaxRPCReadBytes is the most of one file the readers take. A recording is
// capped at MaxRPCFileBytes when it is written; a file much larger than that
// was not written by this recorder.
const MaxRPCReadBytes = 2 * MaxRPCFileBytes

// ErrRPCTooLarge is returned by the readers for input over MaxRPCReadBytes.
var ErrRPCTooLarge = errors.New("the stream recording is larger than the reader takes")

// RPCStats is what ScanRPC saw besides the lines.
type RPCStats struct {
	Damaged  int
	TornTail bool
}

// ScanRPC reads a stream recording one line at a time, calling fn for each line
// it can read, so that a caller need not hold the file. A line that cannot be
// read is counted and skipped; one at the very end is the torn tail a crash
// leaves and is not an error. Input over MaxRPCReadBytes ends with
// ErrRPCTooLarge, after the lines before it. An error from fn ends the scan.
func ScanRPC(r io.Reader, fn func(RPCLine) error) (RPCStats, error) {
	var st RPCStats
	lr := &io.LimitedReader{R: r, N: MaxRPCReadBytes + 1}
	br := bufio.NewReaderSize(lr, 64<<10)
	for {
		line, err := readCapped(br, MaxRPCLineBytes)
		if lr.N <= 0 {
			return st, ErrRPCTooLarge
		}
		complete := err == nil
		if len(line) > 0 {
			var l RPCLine
			if len(line) > MaxRPCLineBytes || json.Unmarshal(bytes.TrimSpace(line), &l) != nil || l.Type == "" || l.V == 0 {
				st.Damaged++
				if !complete {
					st.TornTail = true
				}
			} else {
				if !complete {
					// Whole JSON with no newline: kept, but it is the tail.
					st.TornTail = true
				}
				if ferr := fn(l); ferr != nil {
					return st, ferr
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return st, nil
			}
			return st, err
		}
	}
}

// ReadRPC reads a whole stream recording into memory, up to MaxRPCReadBytes.
// For anything that may be larger, or a player, use ScanRPC.
func ReadRPC(r io.Reader) (RPCFile, error) {
	var out RPCFile
	st, err := ScanRPC(r, func(l RPCLine) error {
		out.Lines = append(out.Lines, l)
		return nil
	})
	out.Damaged, out.TornTail = st.Damaged, st.TornTail
	return out, err
}

// readCapped reads up to and including a newline. A line over max is read to
// its end and returned truncated to max+1 bytes, so the caller sees it was too
// long.
func readCapped(br *bufio.Reader, max int) ([]byte, error) {
	var out []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(out) <= max {
			out = append(out, chunk...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return out, err
	}
}

// ReadRPCFile reads the stream recording at path.
func ReadRPCFile(path string) (RPCFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return RPCFile{}, err
	}
	defer f.Close()
	return ReadRPC(f)
}

// IsRPC reports whether a recording's file name is that of a stream recording.
func IsRPC(name string) bool { return strings.HasSuffix(name, rpcExt) }

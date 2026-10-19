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
	"strconv"
	"strings"
	"sync"
	"time"
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
	// one is recorded as a note saying how long it was.
	rpcMaxRaw = 8 << 20
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
	// Text says what a rpc_started, rpc_dropped or rpc_stopped line is, or what
	// became of a message too long to keep.
	Text         string `json:"text,omitempty"`
	Project      string `json:"project,omitempty"`
	Agent        string `json:"agent,omitempty"`
	Conversation string `json:"conversation,omitempty"`
	// Dropped is the number of lines a rpc_dropped line stands for.
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
	// Now is the clock for the lines' times, for tests.
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
	m.mu.Lock()
	m.sweepStale(folder)
	prune(folder, path, m.now())
	m.mu.Unlock()

	r := &RPCRecorder{cfg: cfg, m: m, path: path, id: id, f: f, w: bufio.NewWriterSize(f, rpcBufferBytes)}
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
	if len(raw) > rpcMaxRaw {
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
	r.commitMsgLocked(dir, msg, now, now, 1)
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
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return r.err
	}
	r.flushPendingLocked()
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

func (r *RPCRecorder) flushPendingLocked() {
	p := r.pend
	if p == nil {
		return
	}
	r.pend = nil
	r.commitMsgLocked(p.dir, p.msg, p.time, p.until, p.merged)
}

// commitMsgLocked redacts, clips and hooks a message and writes it.
func (r *RPCRecorder) commitMsgLocked(dir string, msg map[string]any, first, last time.Time, merged int) {
	line := RPCLine{Time: stamp(first), Type: TypeRPCMessage, Dir: dir}
	if merged > 1 {
		line.Merged, line.Until = merged, stamp(last)
	}
	var v any = msg
	// Message text is the point of a chunk or a prompt and gets the larger limit,
	// kept apart from the generic clip.
	texts := takeMessageTexts(msg)
	v = RedactValue(v)
	clipped := 0
	v = clipCount(v, MaxFieldBytes, &clipped)
	out, _ := v.(map[string]any)
	for _, t := range texts {
		t.text = Redact(t.text)
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
	if err := os.WriteFile(tmp, out.Bytes(), fileMode); err != nil {
		r.err = err
		return
	}
	if err := renameRetry(tmp, r.path, 20); err != nil {
		_ = os.Remove(tmp)
		r.err = err
		return
	}
	f, err := os.OpenFile(r.path, os.O_WRONLY|os.O_APPEND, fileMode)
	if err != nil {
		r.err = err
		return
	}
	r.f, r.w, r.size = f, bufio.NewWriterSize(f, rpcBufferBytes), int64(out.Len())
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
const MaxRPCLineBytes = 32 << 20

// ReadRPC reads a stream recording. A line that cannot be read is counted and
// skipped; one at the very end is the torn tail a crash leaves and is not an
// error.
func ReadRPC(r io.Reader) (RPCFile, error) {
	var out RPCFile
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := readCapped(br, MaxRPCLineBytes)
		complete := err == nil
		if len(line) > 0 {
			var l RPCLine
			if len(line) > MaxRPCLineBytes || json.Unmarshal(bytes.TrimSpace(line), &l) != nil || l.Type == "" || l.V == 0 {
				out.Damaged++
				if !complete {
					out.TornTail = true
				}
			} else if !complete {
				// Complete JSON with no newline: keep it, but it is the tail.
				out.Lines = append(out.Lines, l)
				out.TornTail = true
			} else {
				out.Lines = append(out.Lines, l)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return out, err
		}
	}
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

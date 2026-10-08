// Package recordview reads a transcript file (see docs/recording-format.md) and
// turns it into pages of structured entries for a client that is not trusted
// with the files.
//
// No file bytes leave the package, only Entry values. Every line is parsed here,
// checked against the format version, cut down to an allowlist of fields, cleaned
// of terminal escape sequences and invisible characters, redacted again (the
// rules improve over time and an old recording predates them) and clipped.
//
// The package is wired into nothing. The caller chooses the root, the host's own
// recordings folder, and the name inside it; neither may come from a client.
//
// # What redaction does not do
//
// Redaction is by patterns, in record.Redact, run on every string an entry
// carries and on every key of a tool call's input. It is best effort, and a
// caller should say so where it shows the result. These are not covered:
//
//   - A secret with no recognisable shape, in text that does not name it as one.
//   - A secret split over two entries, such as the end of one tool output and the
//     start of the next, or over two lines with a line break the pattern does not
//     allow in it.
//   - A quoted value that runs over more than 4 KiB: it is redacted to the end of
//     the line it opens on.
//   - Command-line forms other than --flag value, curl -u user:password, mysql
//     -ppassword and sshpass -p password. A password given with -p to any other
//     program is shown.
//   - Fields that are not text of the conversation: the session, conversation,
//     agent and project names, the tool name and tool use id, and the model. These
//     get the patterns and nothing else.
//   - File paths and commands inside a tool call's input, apart from what the
//     patterns take.
//   - The withholding of a secret file's output. The writer withholds the output
//     of a tool call that touched a secret file by name; this package does not
//     decide that again, so a recording written before that rule existed shows it.
//
// What is removed from text before it is matched, and so does not stay in the
// output: invalid UTF-8, control characters other than a line feed and a tab, the
// C1 range, terminal escape sequences whole (not only their first byte), and every
// character that has no width or only changes how its neighbours are drawn, such
// as the zero-width space and joiner, the word joiner, the soft hyphen, the byte
// order mark and the bidirectional marks. Tabs and the spaces of other widths stay
// in the text but do not end a secret.
//
// # Bounds
//
// Memory and time are bounded per call, not per file. A line is read through a
// fixed buffer and one over MaxLineBytes is skipped without being held. A page has
// at most MaxPageEntries entries and about MaxPageBytes of encoded data, an entry
// over MaxEntryBytes is dropped, and one call scans at most MaxScanBytes of the
// file. A file over MaxFileBytes is refused.
//
// A call also has a time budget, DefaultBudget or the deadline of the context it
// is given if that is sooner. It is checked between lines: a page that runs out of
// it is returned with what it has and Done false, and a call that has done no work
// when it runs out returns ErrBudget. A line is never cut: either it is parsed and
// redacted whole or it is not read yet.
//
// # Paths
//
// Open takes a root and a name. The root must be an absolute path to a folder. The
// name is a relative path of at most a few components: no ".." or "." component,
// no drive, UNC or device path, no colon (which on Windows names an alternate data
// stream), no reserved device name, no backslash. Every component under the root
// is looked at with Lstat and none may be a link of any kind (a symbolic link, a
// junction or another reparse point); the root itself must be a folder and not a
// link, and what lies above it is the caller's. The last component must be a
// regular file. A file with more than one hard link is accepted.
//
// # Cursors
//
// A cursor is a byte offset that a previous page returned as Next (or 0). The
// reader checks that the byte before it is a line break, so a client cannot
// start a read in the middle of a line.
//
// # Errors
//
// The errors carry no path and no operating system text, so a caller may log
// them but a client must only ever see a fixed message.
package recordview

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"strconv"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/record"
)

// Limits. See the package comment.
const (
	// MaxFileBytes is the largest file Open accepts: the writer's cap plus room
	// for the closing line.
	MaxFileBytes = record.MaxFileBytes + 1<<20
	// MaxLineBytes is the longest line that is parsed. Longer ones are skipped.
	// The writer cuts every string it records to 32 KiB at most, so a line of a
	// recording is far below this.
	MaxLineBytes = 256 << 10
	// MaxScanBytes is how much of the file one Page call reads.
	MaxScanBytes = 4 << 20
	// DefaultBudget is the time one Page or Open call may take.
	DefaultBudget = 500 * time.Millisecond
	// DefaultPageEntries is used when Page is given a max below 1.
	DefaultPageEntries = 50
	// MaxPageEntries caps the max a caller may ask for.
	MaxPageEntries = 200
	// MaxPageBytes is the encoded size after which a page is closed. A page may
	// pass it by one entry, so that an entry is never unservable.
	MaxPageBytes = 256 << 10
	// MaxEntryBytes is the encoded size over which an entry is dropped.
	MaxEntryBytes = 160 << 10
	// MaxInputBytes is the encoded size over which a tool call's input is
	// replaced by a marker.
	MaxInputBytes = 64 << 10

	maxIDBytes    = 256
	maxInputDepth = 12
	maxInputNodes = 2000
	omittedInput  = "[input omitted: too large]"
)

var (
	// ErrUnavailable is returned when the file cannot be opened or read.
	ErrUnavailable = errors.New("recording unavailable")
	// ErrUnsupported is returned for a file that is not a version 2 transcript:
	// another version, a first line that is not the start of a transcript, a file
	// that is not regular, a path that reaches it through a link or that is not an
	// acceptable name, or a file that is too large.
	ErrUnsupported = errors.New("recording unsupported")
	// ErrBadCursor is returned for a cursor that is not the start of a line.
	ErrBadCursor = errors.New("bad cursor")
	// ErrBudget is returned by a call whose time budget or context was already
	// spent when it began, so that it did nothing.
	ErrBudget = errors.New("recording read out of time")
	// ErrChanged is returned by Page when the file is no longer the one Open
	// looked at, or is smaller than it was. Open it again.
	ErrChanged = errors.New("recording changed")
)

// Usage is the token counts of one model reply. A count the file does not give
// is nil.
type Usage struct {
	InputTokens              *int `json:"inputTokens,omitempty"`
	OutputTokens             *int `json:"outputTokens,omitempty"`
	CacheCreationInputTokens *int `json:"cacheCreationInputTokens,omitempty"`
	CacheReadInputTokens     *int `json:"cacheReadInputTokens,omitempty"`
}

// Entry is one line of a transcript, after parsing, redaction and clipping.
// Which fields are set depends on Type, as in docs/recording-format.md. The
// working directory, git branch and agent version of a line are not carried.
//
// The text of an entry is redacted by patterns and is best effort; see the
// package comment for what it does not cover. In short: Tool, ToolUseID and
// Model get the patterns and nothing else, the file paths and commands inside
// Input are shown as recorded apart from what the patterns take, the output of a
// tool call that touched a secret file is not withheld again here, and a secret
// split over two entries is not seen whole. Redacted says that this pass changed
// something; it does not say that nothing was missed.
//
// Keys of Input that collapse to the same string once cleaned, or that are cut at
// 256 bytes to the same prefix, are kept apart by a suffix "#2", "#3" and so on,
// in the sorted order of the original keys.
type Entry struct {
	Seq          int64          `json:"seq"`
	Time         string         `json:"time"`
	Type         string         `json:"type"`
	Text         string         `json:"text,omitempty"`
	Model        string         `json:"model,omitempty"`
	Usage        *Usage         `json:"usage,omitempty"`
	StopReason   string         `json:"stopReason,omitempty"`
	Title        string         `json:"title,omitempty"`
	Trigger      string         `json:"trigger,omitempty"`
	TokensBefore *int           `json:"tokensBefore,omitempty"`
	TokensAfter  *int           `json:"tokensAfter,omitempty"`
	Tool         string         `json:"tool,omitempty"`
	ToolUseID    string         `json:"toolUseId,omitempty"`
	Input        any            `json:"input,omitempty"`
	Output       string         `json:"output,omitempty"`
	IsError      *bool          `json:"isError,omitempty"`
	Interrupted  *bool          `json:"interrupted,omitempty"`
	Redacted     bool           `json:"redacted,omitempty"`
	Clipped      map[string]int `json:"clipped,omitempty"`
}

// Page is one page of entries. Next is the cursor to ask for the page after it.
// Done is true when the reader reached the end of the complete lines in the
// file; a page closed by a limit or by the time budget has Done false, and the
// next call may return no entries and Done true. Skipped counts lines in this
// page's span that were not shown: not JSON, another version, an unknown type,
// too long or too large.
type Page struct {
	Entries []Entry `json:"entries"`
	Next    int     `json:"next"`
	Done    bool    `json:"done"`
	Skipped int     `json:"skipped,omitempty"`
}

// Header is what the first line of the file says about the transcript.
type Header struct {
	Session      string `json:"session,omitempty"`
	Conversation string `json:"conversation,omitempty"`
	Agent        string `json:"agent,omitempty"`
	Project      string `json:"project,omitempty"`
	Started      string `json:"started,omitempty"`
}

// Reader serves pages of one transcript file. It holds no file handle between
// calls, so it does not keep a program that replaces the file waiting, and it
// is safe for concurrent use. Every call walks the path again and checks that it
// leads to the file Open looked at, and that the file has not shrunk; if not, it
// returns ErrChanged.
type Reader struct {
	root, name string
	header     Header
	id         os.FileInfo
	size       atomic.Int64 // the largest size seen
}

// Open checks that name, inside root, is a version 2 transcript and returns a
// Reader for it. See the package comment for what root and name may be.
func Open(ctx context.Context, root, name string) (*Reader, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultBudget)
	defer cancel()
	if ctx.Err() != nil {
		return nil, ErrBudget
	}
	f, fi, err := openChecked(root, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h, err := readHeader(f, fi.Size())
	if err != nil {
		return nil, err
	}
	r := &Reader{root: root, name: name, header: h, id: fi}
	r.size.Store(fi.Size())
	return r, nil
}

// Header is what the file's first line said when the Reader was opened.
func (r *Reader) Header() Header { return r.header }

// Page returns up to max entries starting at cursor. A max below 1 means
// DefaultPageEntries, and one above MaxPageEntries is lowered to it.
func (r *Reader) Page(ctx context.Context, cursor int, max int) (Page, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultBudget)
	defer cancel()
	if ctx.Err() != nil {
		return Page{}, ErrBudget
	}
	f, fi, err := openChecked(r.root, r.name)
	if err != nil {
		return Page{}, err
	}
	defer f.Close()
	if !os.SameFile(r.id, fi) {
		return Page{}, ErrChanged
	}
	for {
		seen := r.size.Load()
		if fi.Size() < seen {
			return Page{}, ErrChanged
		}
		if r.size.CompareAndSwap(seen, fi.Size()) {
			break
		}
	}
	return pageOf(ctx, f, fi.Size(), cursor, max)
}

// openChecked opens name inside root for reading if every component on the way
// is an ordinary folder or file, not a link, and the file is of an acceptable
// size. Lstat before and the handle's own Stat after must name the same file, so
// a swap between them is refused.
func openChecked(root, name string) (*os.File, os.FileInfo, error) {
	path, err := resolve(root, name)
	if err != nil {
		return nil, nil, err
	}
	lst, err := os.Lstat(path)
	if err != nil {
		return nil, nil, ErrUnavailable
	}
	if !lst.Mode().IsRegular() {
		return nil, nil, ErrUnsupported
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, ErrUnavailable
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || !os.SameFile(lst, fi) {
		f.Close()
		return nil, nil, ErrUnavailable
	}
	if fi.Size() > MaxFileBytes {
		f.Close()
		return nil, nil, ErrUnsupported
	}
	return f, fi, nil
}

func readHeader(ra io.ReaderAt, size int64) (Header, error) {
	n := min(size, MaxLineBytes+1)
	buf := make([]byte, n)
	if _, err := ra.ReadAt(buf, 0); err != nil && err != io.EOF {
		return Header{}, ErrUnavailable
	}
	i := bytes.IndexByte(buf, '\n')
	if i < 0 {
		return Header{}, ErrUnsupported
	}
	var l rawLine
	if json.Unmarshal(buf[:i], &l) != nil || l.V == nil || *l.V != record.Version || l.Type != record.TypeStarted {
		return Header{}, ErrUnsupported
	}
	h := Header{
		Session:      clean(l.Session, maxIDBytes),
		Conversation: clean(l.Conversation, maxIDBytes),
		Agent:        clean(l.Agent, maxIDBytes),
		Project:      clean(l.Project, record.MaxFieldBytes),
	}
	if t, ok := parseTime(l.Time); ok {
		h.Started = t
	}
	return h, nil
}

// pageOf is Page over any reader, so that it can be fuzzed without files. The
// context is looked at between lines, after the first.
func pageOf(ctx context.Context, ra io.ReaderAt, size int64, cursor int, max int) (Page, error) {
	if max < 1 {
		max = DefaultPageEntries
	}
	if max > MaxPageEntries {
		max = MaxPageEntries
	}
	if cursor < 0 || int64(cursor) > size {
		return Page{}, ErrBadCursor
	}
	if cursor > 0 {
		var b [1]byte
		if n, _ := ra.ReadAt(b[:], int64(cursor)-1); n != 1 || b[0] != '\n' {
			return Page{}, ErrBadCursor
		}
	}
	page := Page{Entries: []Entry{}, Next: cursor}
	br := bufio.NewReaderSize(io.NewSectionReader(ra, int64(cursor), size-int64(cursor)), 64<<10)

	var (
		line     []byte
		tooLong  bool
		consumed int64
		encoded  int
	)
	for {
		chunk, err := br.ReadSlice('\n')
		consumed += int64(len(chunk))
		if !tooLong {
			if len(line)+len(chunk) > MaxLineBytes {
				tooLong, line = true, line[:0]
			} else {
				line = append(line, chunk...)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			if err != io.EOF {
				return Page{}, ErrUnavailable
			}
			// The last line has no line break yet: it may be half written or cut by
			// a crash, so it is left for a later call and not consumed.
			page.Done = true
			return page, nil
		}
		// A complete line.
		if tooLong {
			page.Skipped++
		} else if e, ok := parseLine(bytes.TrimSpace(line)); !ok {
			if len(bytes.TrimSpace(line)) > 0 {
				page.Skipped++
			}
		} else if b, err := json.Marshal(e); err != nil || len(b) > MaxEntryBytes {
			page.Skipped++
		} else {
			if len(page.Entries) > 0 && encoded+len(b) > MaxPageBytes {
				// This entry does not fit; it is the next page's first.
				return page, nil
			}
			page.Entries = append(page.Entries, e)
			encoded += len(b)
		}
		page.Next = cursor + int(consumed)
		line, tooLong = line[:0], false
		if len(page.Entries) >= max || encoded >= MaxPageBytes || consumed >= MaxScanBytes || ctx.Err() != nil {
			return page, nil
		}
	}
}

// rawLine is the allowlist: only these fields of a line are decoded. A field
// the format adds later, or one a hostile file invents, is not here and so does
// not reach the viewer.
type rawLine struct {
	V            *int            `json:"v"`
	Seq          int64           `json:"seq"`
	Time         string          `json:"time"`
	Session      string          `json:"session"`
	Conversation string          `json:"conversation"`
	Agent        string          `json:"agent"`
	Project      string          `json:"project"`
	Type         string          `json:"type"`
	Text         string          `json:"text"`
	Model        string          `json:"model"`
	Usage        *rawUsage       `json:"usage"`
	StopReason   string          `json:"stopReason"`
	Title        string          `json:"title"`
	Trigger      string          `json:"trigger"`
	Before       *int            `json:"tokensBefore"`
	After        *int            `json:"tokensAfter"`
	Tool         string          `json:"tool"`
	ToolUseID    string          `json:"toolUseId"`
	Input        json.RawMessage `json:"input"`
	Output       string          `json:"output"`
	IsError      *bool           `json:"isError"`
	Interrupt    *bool           `json:"interrupted"`
	Redacted     bool            `json:"redacted"`
	Clipped      map[string]int  `json:"clipped"`
}

type rawUsage struct {
	InputTokens              *int `json:"inputTokens"`
	OutputTokens             *int `json:"outputTokens"`
	CacheCreationInputTokens *int `json:"cacheCreationInputTokens"`
	CacheReadInputTokens     *int `json:"cacheReadInputTokens"`
}

var knownTypes = map[string]bool{
	record.TypeStarted: true, record.TypeStopped: true, record.TypeTruncated: true,
	record.TypePrompt: true, record.TypeAssistant: true, record.TypeToolCall: true,
	record.TypeToolResult: true, record.TypeTitle: true, record.TypeCompacted: true,
}

// clippedKeys are the keys of a line's clipped object that an Entry carries.
var clippedKeys = map[string]bool{"text": true, "output": true, "input": true, "title": true}

// parseLine turns one line into an Entry, or reports false for a line that is
// not shown. It never panics on any input.
func parseLine(b []byte) (Entry, bool) {
	var l rawLine
	if len(b) == 0 || json.Unmarshal(b, &l) != nil {
		return Entry{}, false
	}
	if l.V == nil || *l.V != record.Version || !knownTypes[l.Type] || l.Seq < 0 {
		return Entry{}, false
	}
	when, ok := parseTime(l.Time)
	if !ok {
		return Entry{}, false
	}
	st := &state{redacted: l.Redacted, clipped: map[string]int{}}
	for k, n := range l.Clipped {
		if clippedKeys[k] && n >= 0 && n <= 1<<40 {
			st.clipped[k] = n
		}
	}
	e := Entry{Seq: l.Seq, Time: when, Type: l.Type}
	switch l.Type {
	case record.TypeStarted, record.TypeStopped, record.TypeTruncated:
		e.Text = st.text("text", l.Text, record.MaxFieldBytes)
	case record.TypePrompt:
		e.Text = st.text("text", l.Text, record.MaxMessageBytes)
	case record.TypeAssistant, record.TypeToolCall:
		if l.Type == record.TypeAssistant {
			e.Text = st.text("text", l.Text, record.MaxMessageBytes)
		} else {
			e.Tool = st.id(l.Tool)
			e.ToolUseID = st.id(l.ToolUseID)
			e.Input = st.input(l.Input)
		}
		e.Model = st.id(l.Model)
		e.StopReason = st.id(l.StopReason)
		e.Usage = usageOf(l.Usage)
	case record.TypeToolResult:
		e.Tool = st.id(l.Tool)
		e.ToolUseID = st.id(l.ToolUseID)
		e.Output = st.text("output", l.Output, record.MaxFieldBytes)
		e.IsError, e.Interrupted = l.IsError, l.Interrupt
	case record.TypeTitle:
		e.Title = st.text("title", l.Title, record.MaxFieldBytes)
	case record.TypeCompacted:
		e.Trigger = st.id(l.Trigger)
		e.TokensBefore, e.TokensAfter = nonNeg(l.Before), nonNeg(l.After)
	}
	e.Redacted = st.redacted
	if len(st.clipped) > 0 {
		e.Clipped = st.clipped
	}
	return e, true
}

// state collects the redacted and clipped flags while one line is cleaned.
type state struct {
	redacted bool
	clipped  map[string]int
}

// redact cleans v, which may be a whole string or a key, and redacts it. The
// order matters: removing the characters first lets redaction see a secret that
// was broken up by them.
func (s *state) redact(v string) string {
	c := cleanText(v)
	r := redactCleaned(c)
	if r != c.text {
		s.redacted = true
	}
	return r
}

// text cleans and redacts a string field, then clips it.
func (s *state) text(name, v string, limit int) string {
	r := s.redact(v)
	c := record.Clip(r, limit)
	if c != r {
		s.clipped[name] = max(s.clipped[name], len(r))
	}
	return c
}

// id is text for the short fields (names and ids), which are clipped at
// maxIDBytes without a marker.
func (s *state) id(v string) string {
	return cutBytes(s.redact(v), maxIDBytes)
}

// input bounds, redacts and clips a tool call's input. A value that is too deep,
// too wide or too long once clipped is replaced by a marker.
func (s *state) input(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil || v == nil {
		return nil
	}
	nodes := 0
	bounded, ok := bound(v, 0, &nodes, s)
	if !ok {
		s.clipped["input"] = max(s.clipped["input"], len(raw))
		return omittedInput
	}
	clipped := record.ClipValue(bounded, record.MaxFieldBytes)
	out, _ := json.Marshal(clipped)
	if len(out) > MaxInputBytes {
		s.clipped["input"] = max(s.clipped["input"], len(raw))
		return omittedInput
	}
	if full, _ := json.Marshal(bounded); !bytes.Equal(out, full) {
		s.clipped["input"] = max(s.clipped["input"], len(full))
	}
	return clipped
}

// bound copies a decoded JSON value, cleaning and redacting every string and
// every key, and fails if it is deeper than maxInputDepth or has more than
// maxInputNodes values. The value under a key that names a secret is replaced
// whole; the name is looked at in full, before the key is cut to a length that
// might take the telling word off its end.
func bound(v any, depth int, nodes *int, st *state) (any, bool) {
	if depth > maxInputDepth {
		return nil, false
	}
	if *nodes++; *nodes > maxInputNodes {
		return nil, false
	}
	switch x := v.(type) {
	case string:
		return st.redact(x), true
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			c, ok := bound(e, depth+1, nodes, st)
			if !ok {
				return nil, false
			}
			out[i] = c
		}
		return out, true
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(x))
		for _, k := range keys {
			name := stripUnsafe(k)
			var c any
			if record.KeyIsSecret(name) {
				c = record.Redacted
				st.redacted = true
			} else {
				var ok bool
				if c, ok = bound(x[k], depth+1, nodes, st); !ok {
					return nil, false
				}
			}
			out[freeKey(out, cutBytes(st.redact(k), maxIDBytes))] = c
		}
		return out, true
	}
	return v, true
}

// freeKey returns key, or key with the first suffix "#2", "#3", ... that is not
// taken, so that two keys that come out the same do not lose a value.
func freeKey(m map[string]any, key string) string {
	if _, taken := m[key]; !taken {
		return key
	}
	for n := 2; ; n++ {
		if k := key + "#" + strconv.Itoa(n); !has(m, k) {
			return k
		}
	}
}

func has(m map[string]any, k string) bool { _, ok := m[k]; return ok }

func usageOf(u *rawUsage) *Usage {
	if u == nil {
		return nil
	}
	out := &Usage{nonNeg(u.InputTokens), nonNeg(u.OutputTokens), nonNeg(u.CacheCreationInputTokens), nonNeg(u.CacheReadInputTokens)}
	if *out == (Usage{}) {
		return nil
	}
	return out
}

func nonNeg(n *int) *int {
	if n == nil || *n < 0 {
		return nil
	}
	return n
}

// parseTime accepts an RFC 3339 time and gives it back in UTC, which is how the
// writer formats it, so the viewer never gets a string it did not expect.
func parseTime(s string) (string, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return "", false
	}
	return t.UTC().Format(time.RFC3339Nano), true
}

// clean is stripUnsafe, redaction and a byte cut, for a header field.
func clean(v string, limit int) string {
	return cutBytes(redactCleaned(cleanText(v)), limit)
}

// cutBytes cuts s to at most n bytes on a character boundary.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

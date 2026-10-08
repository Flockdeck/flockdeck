// Package recordview reads a transcript file (see docs/recording-format.md)
// and turns it into pages of structured entries that are safe to send to a
// remote viewer.
//
// A remote device never gets file bytes. Every line is parsed here, checked
// against the format version, cut down to an allowlist of fields, redacted again
// with record.RedactValue and record.Redact (rules improve over time and an old
// recording predates them), cleaned of control and bidirectional characters,
// and clipped. What leaves the package is Entry values and nothing else.
//
// The package is wired into nothing. The caller chooses the path (it must come
// from the host's own recordings folder, never from a client) and owns root
// confinement; Open adds its own checks on the final component and the size.
//
// # Bounds
//
// Memory is bounded per call, not per file. A line is read through a fixed
// buffer and one over MaxLineBytes is skipped without being held. A page has at
// most MaxPageEntries entries and about MaxPageBytes of encoded data, an entry
// over MaxEntryBytes is dropped, and one call scans at most MaxScanBytes of the
// file. A file over MaxFileBytes is refused.
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
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
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
	MaxLineBytes = 1 << 20
	// MaxScanBytes is how much of the file one Page call reads.
	MaxScanBytes = 4 << 20
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
	// that is not regular, or one that is too large.
	ErrUnsupported = errors.New("recording unsupported")
	// ErrBadCursor is returned for a cursor that is not the start of a line.
	ErrBadCursor = errors.New("bad cursor")
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
// file; a page closed by a limit has Done false, and the next call may return no
// entries and Done true. Skipped counts lines in this page's span that were not
// shown: not JSON, another version, an unknown type, too long or too large.
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
// is safe for concurrent use.
type Reader struct {
	path   string
	header Header
}

// Open checks that path is a version 2 transcript and returns a Reader for it.
func Open(path string) (*Reader, error) {
	f, size, err := openChecked(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h, err := readHeader(f, size)
	if err != nil {
		return nil, err
	}
	return &Reader{path: path, header: h}, nil
}

// Header is what the file's first line said when the Reader was opened.
func (r *Reader) Header() Header { return r.header }

// Page returns up to max entries starting at cursor. A max below 1 means
// DefaultPageEntries, and one above MaxPageEntries is lowered to it.
func (r *Reader) Page(cursor int, max int) (Page, error) {
	f, size, err := openChecked(r.path)
	if err != nil {
		return Page{}, err
	}
	defer f.Close()
	return pageOf(f, size, cursor, max)
}

// openChecked opens path for reading if it is a regular file, not reached
// through a link, of an acceptable size. Lstat before and the handle's own
// Stat after must name the same file, so a swap between them is refused.
func openChecked(path string) (*os.File, int64, error) {
	lst, err := os.Lstat(path)
	if err != nil {
		return nil, 0, ErrUnavailable
	}
	if !lst.Mode().IsRegular() {
		return nil, 0, ErrUnsupported
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, ErrUnavailable
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || !os.SameFile(lst, fi) {
		f.Close()
		return nil, 0, ErrUnavailable
	}
	if fi.Size() > MaxFileBytes {
		f.Close()
		return nil, 0, ErrUnsupported
	}
	return f, fi.Size(), nil
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

// pageOf is Page over any reader, so that it can be fuzzed without files.
func pageOf(ra io.ReaderAt, size int64, cursor int, max int) (Page, error) {
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
		if len(page.Entries) >= max || encoded >= MaxPageBytes || consumed >= MaxScanBytes {
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

// text cleans a string field: control and bidirectional characters out, then
// redaction, then the clip. The order matters: removing characters first lets
// redaction see a secret that was broken up by them.
func (s *state) text(name, v string, limit int) string {
	v = stripUnsafe(v)
	r := record.Redact(v)
	if r != v {
		s.redacted = true
	}
	c := record.Clip(r, limit)
	if c != r {
		s.clipped[name] = max(s.clipped[name], len(v))
	}
	return c
}

// id is text for the short fields (names and ids), which are clipped at
// maxIDBytes without a marker.
func (s *state) id(v string) string {
	v = stripUnsafe(v)
	r := record.Redact(v)
	if r != v {
		s.redacted = true
	}
	return cutBytes(r, maxIDBytes)
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
	before, _ := json.Marshal(bounded)
	red := record.RedactValue(bounded)
	if after, _ := json.Marshal(red); !bytes.Equal(before, after) {
		s.redacted = true
	}
	clipped := record.ClipValue(red, record.MaxFieldBytes)
	out, _ := json.Marshal(clipped)
	if len(out) > MaxInputBytes {
		s.clipped["input"] = max(s.clipped["input"], len(raw))
		return omittedInput
	}
	if redJSON, _ := json.Marshal(red); !bytes.Equal(out, redJSON) {
		s.clipped["input"] = max(s.clipped["input"], len(before))
	}
	return clipped
}

// bound copies a decoded JSON value, cleaning every string and redacting every key, and fails if
// it is deeper than maxInputDepth or has more than maxInputNodes values.
func bound(v any, depth int, nodes *int, st *state) (any, bool) {
	if depth > maxInputDepth {
		return nil, false
	}
	if *nodes++; *nodes > maxInputNodes {
		return nil, false
	}
	switch x := v.(type) {
	case string:
		return stripUnsafe(x), true
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
		out := make(map[string]any, len(x))
		for k, e := range x {
			c, ok := bound(e, depth+1, nodes, st)
			if !ok {
				return nil, false
			}
			out[st.id(k)] = c
		}
		return out, true
	}
	return v, true
}

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
	return cutBytes(record.Redact(stripUnsafe(v)), limit)
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

// stripUnsafe makes a string safe to show: invalid UTF-8 becomes U+FFFD, and
// control characters (other than a line feed and a tab), the C1 range, byte
// order marks and the bidirectional formatting characters are removed. The
// client renders with textContent, so this is not what stops markup; it stops a
// terminal escape or a reordering mark from misleading a reader or hiding text.
func stripUnsafe(s string) string {
	if !strings.ContainsFunc(s, unsafeRune) && utf8.ValidString(s) {
		return s
	}
	s = strings.ToValidUTF8(s, "\uFFFD")
	return strings.Map(func(r rune) rune {
		if r == 0x2028 || r == 0x2029 {
			return '\n'
		}
		if unsafeRune(r) {
			return -1
		}
		return r
	}, s)
}

func unsafeRune(r rune) bool {
	switch {
	case r == 0x0a || r == 0x09:
		return false
	case r < 0x20, r >= 0x7f && r <= 0x9f:
		return true
	case r == 0x200e, r == 0x200f, r == 0xfeff, r == 0x2028, r == 0x2029:
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

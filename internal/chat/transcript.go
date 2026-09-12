package chat

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/store"
)

// Entry is one line of a chat transcript.
//
// The three types are what the conversation is made of; the rest are additions
// that cost a few bytes a line and answer questions the file otherwise cannot:
// which directory the conversation belongs to, which model wrote an answer, and
// what it cost. A reader that knows only about type, ts and text is unaffected
// by them.
type Entry struct {
	Type string    `json:"type"`
	TS   timestamp `json:"ts"`
	Text string    `json:"text"`
	// Tool names the tool a "tool" entry is the output of.
	Tool  string `json:"tool,omitempty"`
	Cwd   string `json:"cwd,omitempty"`
	Model string `json:"model,omitempty"`
	In    int    `json:"in,omitempty"`
	Out   int    `json:"out,omitempty"`
}

// timestamp is when an entry was written.
//
// It is written as RFC 3339, which is what the transcripts beside it use and
// what a person reading the file with their eyes can make sense of. It is read
// back from either that or a count of seconds, because a timestamp is the one
// field of this format two implementations could reasonably have read
// differently, and losing every entry in a file over it would be a poor trade.
type timestamp struct{ time.Time }

func (t timestamp) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.Time.Format(time.RFC3339Nano))
}

func (t *timestamp) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		if s == "" {
			return nil
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, time.DateTime} {
			if v, err := time.Parse(layout, s); err == nil {
				t.Time = v
				return nil
			}
		}
		return nil
	}
	var f float64
	if json.Unmarshal(b, &f) == nil {
		// Both seconds and milliseconds are plausible for a bare number; a
		// value that would otherwise land in the 1970s is the giveaway.
		if f > 1e11 {
			t.Time = time.UnixMilli(int64(f))
		} else {
			t.Time = time.Unix(int64(f), 0)
		}
	}
	return nil
}

// Dir is where flockdeck chat keeps its transcripts.
func Dir() (string, error) {
	base, err := store.Dir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "chats")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create the chat transcript directory: %w", err)
	}
	return dir, nil
}

// Path returns the transcript for a session, or "" when there is nothing to
// read.
//
// Emptiness counts as nothing, the same way it does for Claude Code: a pane
// that was opened and never prompted leaves a file behind with no conversation
// in it, and resuming that is not resuming anything.
func Path(session string) string {
	if session == "" {
		return ""
	}
	dir, err := Dir()
	if err != nil {
		return ""
	}
	path := filepath.Join(dir, session+".jsonl")
	if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
		return ""
	}
	return path
}

// Log appends to one session's transcript.
//
// It is written through on every entry rather than buffered, because the two
// things that read it -- a fan-out looking for a plan, the history overlay --
// read it while the pane is still running, and an answer still sitting in a
// buffer is an answer they cannot see.
type Log struct {
	mu   sync.Mutex
	f    *os.File
	cwd  string
	when func() time.Time
}

// OpenLog opens a session's transcript for appending, creating it if need be.
func OpenLog(dir, session, cwd string) (*Log, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create the chat transcript directory: %w", err)
	}
	path := filepath.Join(dir, session+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the chat transcript: %w", err)
	}
	return &Log{f: f, cwd: cwd, when: time.Now}, nil
}

// Append records one entry.
func (l *Log) Append(e Entry) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if e.TS.IsZero() {
		e.TS = timestamp{l.when()}
	}
	if e.Cwd == "" {
		e.Cwd = l.cwd
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err := l.f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

// Path is the file this log writes to.
func (l *Log) Path() string {
	if l == nil {
		return ""
	}
	return l.f.Name()
}

func (l *Log) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}

// maxEntry bounds one entry read back from a transcript. A longer line -- a
// paste of many megabytes, recorded whole -- is read past rather than kept.
//
// It used to end the read: the scanner gave up on the line and the error
// took every entry with it, so one enormous paste left a conversation that
// resume could not put back and the history could not list. A conversation
// missing one entry is still a conversation.
const maxEntry = 8 << 20

// nextEntryLine reads one line of a transcript without its ending, and reports
// whether it was longer than maxEntry, in which case none of it is kept.
func nextEntryLine(br *bufio.Reader) (line []byte, long bool, err error) {
	for {
		chunk, err := br.ReadSlice('\n')
		if !long {
			if len(line)+len(chunk) > maxEntry {
				line, long = nil, true
			} else {
				line = append(line, chunk...)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return bytes.TrimRight(line, "\r\n"), long, err
	}
}

// ReadEntries reads a whole transcript. A line that does not parse is skipped
// rather than failing the read: the last line of a file being written to is
// half-written as often as not.
func ReadEntries(path string) ([]Entry, error) { return readTail(path, maxInt64) }

// maxInt64 stands for "all of it" where a size is asked for.
const maxInt64 = int64(1) << 62

// replyTailBytes bounds how much of a transcript is read looking for what the
// model last said. A transcript grows without limit and the last turn is at the
// end of it, so only the tail is worth opening.
const replyTailBytes = 4 << 20

// readTail reads the last max bytes of a transcript.
//
// Reading only the tail lands mid-line, so the first line read back is a
// fragment and is dropped rather than parsed.
func readTail(path string, max int64) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	partial := false
	if fi, err := f.Stat(); err == nil && fi.Size() > max {
		// The byte before where reading starts says whether it starts a
		// line. Only when it does not is the first line read a fragment:
		// dropping it regardless threw away a whole entry whenever the cut
		// fell between two.
		if _, err := f.Seek(fi.Size()-max-1, io.SeekStart); err == nil {
			var before [1]byte
			_, err := io.ReadFull(f, before[:])
			partial = err != nil || before[0] != '\n'
		}
	}
	br := bufio.NewReaderSize(f, 64<<10)
	if partial {
		if _, _, err := nextEntryLine(br); err != nil {
			return nil, nil
		}
	}
	var out []Entry
	for {
		line, long, err := nextEntryLine(br)
		var e Entry
		if !long && len(line) > 0 && json.Unmarshal(line, &e) == nil && e.Type != "" {
			out = append(out, e)
		}
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
	}
}

// Messages turns a transcript back into a conversation to resume from.
//
// A tool's output comes back as part of what the user said rather than as the
// answer to a call, because the transcript records what was said and not the
// call ids the wires pair an answer with -- and an answer to a call that is not
// in the request is rejected outright by every one of them. Consecutive entries
// on the same side are joined for the same reason: an API that insists the two
// sides alternate should not be handed two user entries in a row.
func Messages(entries []Entry) []Message {
	var out []Message
	add := func(role Role, text string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Text += "\n\n" + text
			return
		}
		out = append(out, Message{Role: role, Text: text})
	}
	for _, e := range entries {
		switch e.Type {
		case entryClear:
			// Everything before the conversation was started over is history
			// the model was no longer being shown, and resuming should put back
			// what was on the screen rather than what the file remembers.
			out = nil
		case string(RoleUser):
			add(RoleUser, e.Text)
		case string(RoleAssistant):
			add(RoleAssistant, e.Text)
		case string(RoleTool):
			label := e.Tool
			if label == "" {
				label = "tool"
			}
			add(RoleUser, "["+label+"]\n"+e.Text)
		}
	}
	return out
}

// Replies returns what the model said in its last few turns, newest first and
// at most maxTurns of them.
//
// This is the shape session.RecentReplies has, and for the same reason: it is
// where a fan-out finds the plan it is about to split up. An API pane's plan is
// then no different from any other agent's.
func Replies(session string, maxTurns int) []string {
	path := Path(session)
	if path == "" || maxTurns <= 0 {
		return nil
	}
	entries, err := readTail(path, replyTailBytes)
	if err != nil {
		return nil
	}
	var turns, said []string
	end := func() {
		if len(said) > 0 {
			turns = append(turns, strings.Join(said, "\n\n"))
			said = nil
		}
	}
	for _, e := range entries {
		switch e.Type {
		case string(RoleAssistant):
			if text := strings.TrimSpace(e.Text); text != "" {
				said = append(said, text)
			}
		case string(RoleUser):
			// A turn is everything said between one thing the user typed and
			// the next.
			end()
		}
	}
	end()
	out := make([]string, 0, maxTurns)
	for i := len(turns) - 1; i >= 0 && len(out) < maxTurns; i-- {
		out = append(out, turns[i])
	}
	return out
}

// Conversation is a stored chat that can be resumed. It carries the same facts
// as a Claude Code conversation, so the history overlay can list the two side
// by side.
type Conversation struct {
	ID       string
	Cwd      string
	Summary  string
	Title    string
	Modified time.Time
	Messages int
	Size     int64
}

// summaryLimit is how much of an opening prompt is worth keeping for a list of
// conversations. It is a row in a list, not the prompt itself.
const summaryLimit = 200

// Conversations lists the stored chats belonging to a working directory, newest
// first. An empty cwd lists them all.
func Conversations(cwd string) ([]Conversation, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	names, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	var out []Conversation
	for _, path := range names {
		fi, err := os.Stat(path)
		if err != nil || fi.Size() == 0 {
			continue
		}
		entries, err := ReadEntries(path)
		if err != nil || len(entries) == 0 {
			continue
		}
		c := Conversation{
			ID:       strings.TrimSuffix(filepath.Base(path), ".jsonl"),
			Modified: fi.ModTime(),
			Size:     fi.Size(),
		}
		for _, e := range entries {
			if e.Cwd != "" && c.Cwd == "" {
				c.Cwd = e.Cwd
			}
			if e.Type == string(RoleUser) && c.Summary == "" {
				c.Summary = firstLine(e.Text, summaryLimit)
			}
			if e.Type == string(RoleUser) || e.Type == string(RoleAssistant) {
				c.Messages++
			}
		}
		if c.Summary == "" {
			c.Summary = NoPrompt
		}
		if cwd != "" && !sameDir(c.Cwd, cwd) {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}

// NoPrompt stands in for a conversation that says nothing about itself, the way
// the same label does for a Claude Code transcript.
const NoPrompt = "(no prompt recorded)"

// firstLine is the opening line of s, cut to at most n bytes on a rune
// boundary.
func firstLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strings.TrimSpace(s[:n])
}

// sameDir compares two directories the way the platform does.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// newSessionID makes an id for a conversation nobody named.
//
// It has the shape of a version 4 UUID because that is what every other
// conversation id in Flockdeck is, and a transcript is looked up by globbing for a
// file named after one. Sixteen random bytes are all it takes, so there is no
// call for a dependency.
func newSessionID(random func([]byte) error) (string, error) {
	b := make([]byte, 16)
	if err := random(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	s := hex.EncodeToString(b)
	return strings.Join([]string{s[0:8], s[8:12], s[12:16], s[16:20], s[20:32]}, "-"), nil
}

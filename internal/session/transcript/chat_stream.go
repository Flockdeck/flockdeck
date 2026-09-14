package transcript

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
)

// Stream returns a live view of a pane running Flockdeck's own chat client,
// tailing its transcript (internal/chat/transcript.go) the same way Claude's
// does its own. It answers ok=true whenever a session id is given at all --
// same reasoning as Claude's: a pane that has not been prompted yet has
// nothing to show, not an agent the phone should fall back to the terminal
// for.
//
// The session id names a file in the chats folder, so it must be one plain
// name. It is whatever the pane's own process last reported through the
// hook, and a misbehaving one reporting "../x" must not point this at a
// transcript outside that folder.
func (Chat) Stream(_ agent.Spec, sessionID string) (Stream, bool) {
	if !plainName(sessionID) {
		return nil, false
	}
	return newChatStream(sessionID), true
}

// plainName reports whether s is a single file name: not empty, not "." or
// "..", and with no directory separator of any platform in it.
func plainName(s string) bool {
	return s != "" && s != "." && s != ".." && s == filepath.Base(s) && !strings.Contains(s, "/")
}

// chatStream tails one chat transcript and keeps the Entry values it has
// translated the lines into.
//
// Unlike Claude's, a chat transcript line is always written once the thing
// it describes is already finished (see internal/chat/calls.go's answer --
// there is no separate call-started line), so an entry here is never
// updated in place the way a Claude tool_use is; each line becomes exactly
// one Entry, added once.
type chatStream struct {
	mu        sync.Mutex
	sessionID string

	path   string // resolved lazily, matching claudeStream
	offset int64
	carry  []byte

	entries []Entry
	index   map[string]int
	detail  map[string]Detail

	seq          int
	resetPending bool

	// light is whether this stream is in preview-only mode -- see
	// SetLight on the Stream interface. Zero value is false, so a freshly
	// built stream defaults to full, matching every caller that built one
	// before this field existed.
	light bool
}

func newChatStream(sessionID string) *chatStream {
	return &chatStream{
		sessionID: sessionID,
		index:     map[string]int{},
		detail:    map[string]Detail{},
	}
}

// Refresh implements Stream.
func (s *chatStream) Refresh() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.path == "" {
		dir, err := chatsDir()
		if err != nil {
			return nil
		}
		s.path = filepath.Join(dir, s.sessionID+".jsonl")
	}
	f, err := os.Open(s.path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if _, err := f.Seek(s.offset, io.SeekStart); err != nil {
		return nil
	}
	chunk, err := io.ReadAll(f)
	if err != nil || len(chunk) == 0 {
		return nil
	}
	data := append(s.carry, chunk...)
	s.offset += int64(len(chunk))

	parts := bytes.Split(data, []byte("\n"))
	complete, partial := parts[:len(parts)-1], parts[len(parts)-1]
	s.carry = append([]byte(nil), partial...)

	var changed []Entry
	for _, line := range complete {
		line = bytes.TrimRight(line, "\r")
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if e, ok := s.applyLine(line); ok {
			changed = append(changed, e)
		}
	}
	return changed
}

// Snapshot implements Stream.
func (s *chatStream) Snapshot() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entries
}

// Detail implements Stream.
func (s *chatStream) Detail(id string) (Detail, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.detail[id]
	return d, ok
}

// Reset implements Stream: true, once, the first time it is asked after a
// `/clear` line -- see the type doc on the Stream interface for why chat
// needs this and Claude does not.
func (s *chatStream) Reset() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.resetPending
	s.resetPending = false
	return v
}

// SetLight implements Stream.
func (s *chatStream) SetLight(light bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if light == s.light {
		return
	}
	s.light = light
	if light {
		s.entries = nil
		s.index = map[string]int{}
		s.detail = map[string]Detail{}
		return
	}
	// Coming back to full: light mode never kept the entries it tailed
	// through, so the only way to answer with the whole conversation again
	// is to read the file from the top, same as a stream built fresh.
	s.offset = 0
	s.carry = nil
	s.seq = 0
	s.resetPending = false
	s.entries = nil
	s.index = map[string]int{}
	s.detail = map[string]Detail{}
}

// EntryCount implements Stream.
func (s *chatStream) EntryCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// setDetail records an entry's trimmed-away full body, except in light mode,
// where nothing is worth keeping for a pane nobody is watching.
func (s *chatStream) setDetail(id string, d Detail) {
	if s.light {
		return
	}
	s.detail[id] = d
}

// ---------------------------------------------------------------------------
// Line translation
// ---------------------------------------------------------------------------

// chatStreamLine is the shape of one line of internal/chat/transcript.go's
// Entry, read back independently of that package (the same way the existing
// Chat Reader's chatLine in chat.go does) so this stays a small, self
// contained reader rather than a second consumer of internal/chat's full
// surface.
type chatStreamLine struct {
	Type string          `json:"type"`
	TS   json.RawMessage `json:"ts"`
	Text string          `json:"text"`
	Tool string          `json:"tool"`
	Call string          `json:"call"`
}

// Entry type values internal/chat/transcript.go ever writes -- see
// commands.go's entryClear, message.go's Role* and calls.go's answer/record
// call sites, every one of them read for this adapter.
const (
	chatTypeUser      = "user"
	chatTypeAssistant = "assistant"
	chatTypeTool      = "tool"
	chatTypeClear     = "clear"
)

// chatToolFailurePrefixes are how a tool entry's Text says it did not
// succeed -- there is no separate is_error flag the way Claude's tool_result
// has one, only the fixed set of strings internal/chat/calls.go's answer()
// is ever called with for anything other than a tool's own output. Read from
// every s.answer(c, ...) and s.decline(...) call site in calls.go.
var chatToolFailurePrefixes = []string{
	"the tool failed:",
	"not run:",
	"there is no tool called",
	"the user did not answer",
	"the user stopped the turn",
	"the user declined this",
}

func chatToolStatus(text string) string {
	for _, prefix := range chatToolFailurePrefixes {
		if strings.HasPrefix(text, prefix) {
			return StatusError
		}
	}
	return StatusOK
}

// applyLine translates one transcript line into zero or one Entry, per the
// design's §5: only prompt, reply and tool have anything in a chat
// transcript to build from (no thinking text, no error lines and no images
// are ever written to it -- see NOTES.md). ok is false for a line that
// produced nothing to show, including the clear marker itself, which resets
// the stream's state rather than becoming a row.
func (s *chatStream) applyLine(raw []byte) (Entry, bool) {
	var line chatStreamLine
	if json.Unmarshal(raw, &line) != nil {
		return Entry{}, false
	}
	switch line.Type {
	case chatTypeClear:
		s.entries = nil
		s.index = map[string]int{}
		s.detail = map[string]Detail{}
		s.resetPending = true
		return Entry{}, false
	case chatTypeUser:
		text := strings.TrimSpace(line.Text)
		if text == "" {
			return Entry{}, false
		}
		return s.add(Entry{ID: s.nextID(), Kind: KindPrompt, TS: chatTimestamp(line.TS), Text: text}), true
	case chatTypeAssistant:
		text := strings.TrimSpace(line.Text)
		if text == "" {
			return Entry{}, false
		}
		return s.add(Entry{ID: s.nextID(), Kind: KindReply, TS: chatTimestamp(line.TS), Markdown: text}), true
	case chatTypeTool:
		return s.add(s.toolEntry(line)), true
	default:
		return Entry{}, false
	}
}

// toolEntry builds a tool row from one already-finished "tool" line, per the
// design's §2: output over ToolOutputCap, or more than the one line a
// summary can hold, is a summary with hasDetail rather than sent inline.
// There is no diff -- see NOTES.md -- because the transcript never carries a
// call's raw before/after text, only its rendered one-liner (Call) and its
// result (Text).
func (s *chatStream) toolEntry(line chatStreamLine) Entry {
	e := Entry{
		ID: s.nextID(), Kind: KindTool, TS: chatTimestamp(line.TS),
		Name: line.Tool, Label: chatToolLabel(line.Tool, line.Call),
		Status: chatToolStatus(line.Text),
	}
	summary, hasDetail := summarizeToolOutput(line.Text)
	e.Summary = summary
	if hasDetail {
		e.HasDetail = true
		s.setDetail(e.ID, Detail{Text: line.Text})
	}
	return e
}

// chatToolLabel is the short, human label a collapsed tool row shows, built
// from the tool's name and describeCall's own rendering of its argument
// (calls.go's Call field, "<name> <what>") -- there is no raw argument to
// read a path or command out of separately, so what is stripped back off
// Call's own leading "<name> " instead.
func chatToolLabel(tool, call string) string {
	what := strings.TrimSpace(strings.TrimPrefix(call, tool))
	switch tool {
	case "read_file":
		return "Read " + what
	case "write_file":
		return "Wrote " + what
	case "edit_file":
		return "Edited " + what
	case "list_dir":
		if what == "" {
			return "Listed the directory"
		}
		return "Listed " + what
	case "glob", "grep":
		return "Searched for " + what
	case "run_command":
		return "Ran " + what
	default:
		if what == "" {
			return tool
		}
		return tool + " " + what
	}
}

func (s *chatStream) nextID() string {
	id := "c" + strconv.Itoa(s.seq)
	s.seq++
	return id
}

func (s *chatStream) add(e Entry) Entry {
	if s.light {
		// Still translated and returned -- Refresh's caller (the inbox
		// preview) needs the entry -- just never retained: a pane nobody
		// has open keeps no history in memory, only the latest reply,
		// which the caller reads straight off Refresh's return value.
		return e
	}
	s.index[e.ID] = len(s.entries)
	s.entries = append(s.entries, e)
	return e
}

// chatTimestamp normalises internal/chat/transcript.go's ts field -- RFC
// 3339(Nano) on every line it writes itself, but read back accepting a bare
// number too (chat.timestamp.UnmarshalJSON) for the same reason a fixture or
// another writer might use one -- into the RFC 3339 string the wire wants,
// matching how claudeStream hands a timestamp it already read as a string
// straight over. Unreadable or empty is "", the same as a Claude line with
// none.
func chatTimestamp(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		if f > 1e11 {
			return time.UnixMilli(int64(f)).UTC().Format(time.RFC3339Nano)
		}
		return time.Unix(int64(f), 0).UTC().Format(time.RFC3339Nano)
	}
	return ""
}

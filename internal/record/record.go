// Package record writes a pane's agent interaction to disk as a structured
// transcript: one JSON object per line, one file per recording session,
// under a folder per project in Flockdeck's own state directory.
//
// It is off for every pane until the user turns it on, and it never writes
// into a project's repository. What it is given is what Claude Code's hooks
// say (internal/hooks), not what the terminal shows, so it holds the events an
// agent reports and nothing the screen drew. Everything it writes has been
// through Redact and Clip first, and that is best effort: see Redact.
package record

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// Version is the schema version written on every line (the "v" field). It goes
// up when a field changes meaning, never for a field added.
const Version = 1

// Event types, the "type" field of a line.
const (
	TypeStarted    = "recording_started"
	TypeStopped    = "recording_stopped"
	TypeTruncated  = "recording_truncated"
	TypeSession    = "session"
	TypePrompt     = "user_prompt"
	TypeAssistant  = "assistant_message"
	TypeToolCall   = "tool_call"
	TypeToolResult = "tool_result"
	TypeTitle      = "conversation_title"
	TypeCompacted  = "conversation_compacted"
	TypePermission = "permission_prompt"
	TypeOutcome    = "permission_outcome"
	TypeStatus     = "status"
)

// Permission outcomes, the "outcome" field of a permission_outcome line.
const (
	OutcomeAllowed      = "allowed"
	OutcomeAutoApproved = "auto_approved"
	OutcomeDenied       = "denied"
	OutcomeAbandoned    = "abandoned"
)

// Limits. A transcript is a convenience, not an archive, so every one of these
// errs towards losing the end of something large rather than filling a disk.
const (
	// MaxFileBytes caps one session's file. At the cap the file ends with a
	// recording_truncated line and the recording stops.
	MaxFileBytes = 16 << 20
	// MaxFieldBytes caps any one string a line carries, a tool's output
	// included; the text cut is replaced with a marker saying how much.
	MaxFieldBytes = 8 << 10
	// MaxMessageBytes is the cap for a user's prompt and an assistant's message,
	// which are the transcript's point and so are given more room.
	MaxMessageBytes = 32 << 10

	// Retention, applied to a project's folder each time a session starts there.
	RetainDays  = 30
	RetainFiles = 100
	RetainBytes = 256 << 20

	folderMode     = 0o700
	fileMode       = 0o600
	recordingsDir  = "recordings"
	exportsDir     = "exports"
	sessionFileExt = ".jsonl"
)

// Meta says whose line it is. It is read when a line is written, so a pane
// renamed, or one that has changed agent, is recorded as it is at the time.
type Meta struct {
	Pane         string
	PaneName     string
	Project      string // the project's name, as the window shows it
	ProjectRoot  string // its directory, which names the folder
	Agent        string
	Conversation string
}

// Entry is one line of a transcript.
type Entry struct {
	V int `json:"v"`
	// Seq numbers the lines of one session file from 1, with no gaps.
	Seq  int64  `json:"seq"`
	Time string `json:"time"`
	// Session names the session file the line is in, without its extension.
	Session      string `json:"session"`
	Pane         string `json:"pane"`
	PaneName     string `json:"paneName,omitempty"`
	Project      string `json:"project,omitempty"`
	Agent        string `json:"agent,omitempty"`
	Model        string `json:"model,omitempty"`
	Conversation string `json:"conversation,omitempty"`
	// GitBranch, Cwd and AgentVersion are the branch, the working directory and
	// the agent's version the stored entry behind the line records.
	GitBranch    string `json:"gitBranch,omitempty"`
	Cwd          string `json:"cwd,omitempty"`
	AgentVersion string `json:"agentVersion,omitempty"`
	Type         string `json:"type"`
	// Subagent names the subagent an event came from; absent for the main agent.
	Subagent string `json:"subagent,omitempty"`
	Text     string `json:"text,omitempty"`
	// Usage and StopReason are on the first line of a model's reply only.
	Usage      *Usage `json:"usage,omitempty"`
	StopReason string `json:"stopReason,omitempty"`
	// Title is the conversation's title, on a conversation_title line.
	Title string `json:"title,omitempty"`
	// Trigger, TokensBefore and TokensAfter are on a conversation_compacted line.
	Trigger      string `json:"trigger,omitempty"`
	TokensBefore int    `json:"tokensBefore,omitempty"`
	TokensAfter  int    `json:"tokensAfter,omitempty"`
	Tool         string `json:"tool,omitempty"`
	ToolUseID    string `json:"toolUseId,omitempty"`
	Input        any    `json:"input,omitempty"`
	Output       string `json:"output,omitempty"`
	IsError      bool   `json:"isError,omitempty"`
	// Interrupted says a tool ended because the user stopped it.
	Interrupted bool   `json:"interrupted,omitempty"`
	Outcome     string `json:"outcome,omitempty"`
	// Inferred says Outcome was worked out from what came next, since Claude
	// Code has no event that reports the answer to a permission prompt.
	Inferred bool   `json:"inferred,omitempty"`
	Reason   string `json:"reason,omitempty"`
	// Source is how a session started, on a session line.
	Source   string `json:"source,omitempty"`
	Status   string `json:"status,omitempty"`
	Previous string `json:"previous,omitempty"`
	Detail   string `json:"detail,omitempty"`
	// Redacted says something in the line was replaced by Redact, or withheld
	// as the contents of a secret file.
	Redacted bool `json:"redacted,omitempty"`
	// Clipped names each field that was cut, with its length in bytes as the
	// recorder received it, before redaction: "text", "output", "detail",
	// "reason", "input", "title", "cwd" or "gitBranch". The field itself ends in a marker saying how much.
	Clipped map[string]int `json:"clipped,omitempty"`
}

// Usage is the tokens one model reply used. All four are always written, 0 for a
// kind the stored conversation does not give.
type Usage struct {
	InputTokens              int `json:"inputTokens"`
	OutputTokens             int `json:"outputTokens"`
	CacheCreationInputTokens int `json:"cacheCreationInputTokens"`
	CacheReadInputTokens     int `json:"cacheReadInputTokens"`
}

// Manager owns the open transcript files, one per recording pane.
type Manager struct {
	// Dir is the state directory recordings live under. It is resolved on use,
	// so a state directory that is not available at start-up is not fatal.
	Dir func() (string, error)

	mu    sync.Mutex
	now   func() time.Time
	max   int64
	panes map[string]*session
	// closed is set by Close, which is final: a Manager that has been closed
	// writes nothing more, whatever is still handed to it.
	closed bool

	// export is set on the Manager an export writes through (see Export). It
	// writes to the project's exports folder rather than beside the recordings,
	// and deletes nothing, since nobody asked for an old recording to go.
	export bool
	// target is the file an export writes to instead. It must not exist.
	target string
}

// session is one pane's open file and what is tracked while it is open.
type session struct {
	f    *os.File
	path string
	// final, when path is a file a finished transcript of this conversation is
	// being replaced through, is the file it replaces. See openLocked.
	final  string
	id     string
	seq    int64
	size   int64
	capped bool
	// last is the time of the last event written, which is the time of the
	// closing line.
	last time.Time
	// secret marks the tool calls that touched a secret file, by tool_use_id,
	// so their output is withheld; lastSecret does the same for a result that
	// does not say which call it answers.
	secret     map[string]bool
	lastSecret bool
}

// NewManager returns a Manager writing under the state directory dir names.
func NewManager(dir func() (string, error)) *Manager {
	return &Manager{Dir: dir, now: time.Now, max: MaxFileBytes, panes: map[string]*session{}}
}

var slugRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Folder is the name of a project's folder: its name, made safe, and a short
// hash of its directory, so two projects with one name do not share one.
func Folder(project, root string) string {
	name := strings.Trim(slugRe.ReplaceAllString(project, "-"), "-.")
	if name == "" {
		name = "project"
	}
	if len(name) > 40 {
		name = name[:40]
	}
	var h uint32 = 2166136261
	for _, c := range strings.ToLower(filepath.ToSlash(root)) {
		h = (h ^ uint32(c)) * 16777619
	}
	return fmt.Sprintf("%s-%08x", name, h)
}

// Active reports whether a transcript file is open for the pane.
func (m *Manager) Active(pane string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.panes[pane] != nil
}

// Path is the open transcript's file, empty if there is none.
func (m *Manager) Path(pane string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.panes[pane]; s != nil {
		if s.final != "" {
			return s.final
		}
		return s.path
	}
	return ""
}

// SetMaxFileBytes lowers the size a transcript file may reach from MaxFileBytes.
// It is for a caller that cannot make 16 MiB of conversation, and is set before
// anything is written.
func (m *Manager) SetMaxFileBytes(n int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.max = n
}

// The text of the lines that open and close a transcript. They say nothing
// about how it was made, so that a transcript recorded as a conversation went
// on and one exported afterwards are the same bytes.
const (
	startText = "start of the transcript"
	endText   = "end of the transcript"
)

// SessionID is the id a transcript made from a conversation has, and so the
// name of its file without the extension. It is the time of the
// conversation's first event and the start of the conversation's id, which
// are the same whenever and however the transcript is made.
func SessionID(first time.Time, meta Meta) string {
	return first.UTC().Format("20060102T150405Z") + "-" + shortConversation(meta)
}

// shortConversation is the part of a conversation's id its transcripts' names
// end with.
func shortConversation(meta Meta) string {
	conv := meta.Conversation
	if conv == "" {
		conv = meta.Pane
	}
	short := slugRe.ReplaceAllString(conv, "")
	if len(short) > 8 {
		short = short[:8]
	}
	return short
}

// Write adds one event of the pane's conversation to its transcript, opening
// the file first if the pane has none: a transcript begins with the
// conversation's first event, whatever the time is now. It reports false when
// the transcript has reached its size cap and ended (ErrFull), which the caller answers
// by switching the pane's recording off, and an error when it could not be
// opened.
func (m *Manager) Write(meta Meta, ev transcript.ExportEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	s := m.panes[meta.Pane]
	if s == nil {
		if err := m.openLocked(meta, ev.Time); err != nil {
			return err
		}
		s = m.panes[meta.Pane]
		if err := m.writeLocked(meta, Entry{Type: TypeStarted, Text: startText, Time: stamp(ev.Time)}); err != nil {
			m.dropLocked(meta.Pane)
			return err
		}
	}
	e := entryOf(ev)
	m.prepareLocked(s, &e)
	if err := m.writeLocked(meta, e); err != nil {
		return err
	}
	s.last = ev.Time
	if s.capped {
		return ErrFull
	}
	return nil
}

// ErrClosed is what Write returns after Close.
var ErrClosed = errors.New("the transcripts are closed")

// dropLocked closes a pane's file after a failure and removes what was made of a
// replacement, leaving a finished file it was to replace as it was.
func (m *Manager) dropLocked(pane string) {
	if s := m.panes[pane]; s != nil {
		_ = s.f.Close()
		if s.final != "" {
			_ = os.Remove(s.path)
		}
		delete(m.panes, pane)
	}
}

// ErrFull is what Write returns once the transcript has reached its size cap.
var ErrFull = errors.New("the transcript is full")

// Finish ends the pane's transcript with a closing line, at the time of the
// last event. It is harmless for a pane with no transcript open.
//
// A transcript that was written through a replacement of an earlier, finished
// one takes its place only if it is itself finished and has every line the
// earlier one had. If the closing line cannot be written the replacement is
// dropped and the earlier file stays; if the earlier file cannot be replaced (a
// program has it open) the finished replacement is left beside it, and the error
// says so.
func (m *Manager) Finish(meta Meta) error {
	m.mu.Lock()
	s := m.panes[meta.Pane]
	if s == nil {
		m.mu.Unlock()
		return nil
	}
	werr := m.writeLocked(meta, Entry{Type: TypeStopped, Text: endText, Time: stamp(s.last)})
	cerr := s.f.Close()
	delete(m.panes, meta.Pane)
	plain := m.target == "" && !m.export
	m.mu.Unlock()

	// Moving the file into place can take a moment, and nothing else waits for
	// it: the lock is not held.
	if werr == nil {
		werr = cerr
	}
	if werr != nil {
		if s.final != "" {
			_ = os.Remove(s.path)
		}
		return fmt.Errorf("its closing line could not be written: %w", werr)
	}
	if s.final != "" {
		if !finished(s.path) || !supersedes(s.final, s.path) {
			_ = os.Remove(s.path)
			return ErrEarlierKept
		}
		if err := renameRetry(s.path, s.final); err != nil {
			var link *os.LinkError
			if errors.As(err, &link) {
				err = link.Err
			}
			return fmt.Errorf("the new one is left beside the earlier one as %s, which could not be replaced: %v", filepath.Base(s.path), err)
		}
	}
	// A recording of a conversation is the one file of it; an export never
	// deletes another, as nobody asked for an earlier one to go.
	if plain {
		removeSuperseded(filepath.Dir(s.path), meta, filepath.Base(m.finalPath(s)))
	}
	return nil
}

// ErrEarlierKept is what Finish returns when the transcript it made was not put
// in place of an earlier, finished one of the same conversation, because that has
// lines this one does not (the stored conversation changed under it, or this one
// was cut short): the earlier one is kept as it was.
var ErrEarlierKept = errors.New("an earlier, finished transcript of this conversation has events the new one lacks (the stored conversation was cut or changed), so it was kept as it was; delete it to have a fresh one")

// renameRetry moves a file over another, trying again for a moment: on Windows
// a program with the old one open (a viewer, a scanner) makes it fail until it
// lets go. It does not wait after the last try.
func renameRetry(from, to string) error {
	var err error
	for i := 0; i < 6; i++ {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		if i < 5 {
			time.Sleep(time.Duration(i+1) * 50 * time.Millisecond)
		}
	}
	return err
}

// staleNewAfter is how old a replacement left beside a transcript has to be
// before it is taken to have been abandoned by a quit or a crash.
const staleNewAfter = 24 * time.Hour

// sweepStale removes the replacements in a folder that nothing is writing and
// that are old: a Flockdeck that quit mid-rewrite leaves one, and nothing else
// would ever list or count it.
func (m *Manager) sweepStale(folder string) {
	ents, err := os.ReadDir(folder)
	if err != nil {
		return
	}
	held := map[string]bool{}
	for _, s := range m.panes {
		held[s.path] = true
	}
	for _, e := range ents {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), sessionFileExt+".new") {
			continue
		}
		p := filepath.Join(folder, e.Name())
		if fi, err := e.Info(); err == nil && !held[p] && m.now().Sub(fi.ModTime()) > staleNewAfter {
			_ = os.Remove(p)
		}
	}
}

func (m *Manager) finalPath(s *session) string {
	if s.final != "" {
		return s.final
	}
	return s.path
}

// Close closes every transcript, for a Flockdeck that is quitting, for good.
// Nothing is written, and what was being written in place of a finished
// transcript is dropped: a recording left on is on again at the next start, and
// writes its file again from the conversation's beginning.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for id := range m.panes {
		m.dropLocked(id)
	}
}

// Abandon closes the pane's transcript with no closing line, for one about to
// be written again from the start. What it replaces is left as it is.
func (m *Manager) Abandon(meta Meta) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dropLocked(meta.Pane)
}

// Discard closes the pane's transcript and deletes its file, for an export
// that failed part way.
func (m *Manager) Discard(meta Meta) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.panes[meta.Pane]; s != nil {
		_ = s.f.Close()
		_ = os.Remove(s.path)
		delete(m.panes, meta.Pane)
	}
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// entryOf is the line an event of a conversation is.
func entryOf(ev transcript.ExportEvent) Entry {
	e := Entry{Time: stamp(ev.Time)}
	switch ev.Kind {
	case transcript.ExportPrompt:
		e.Type, e.Text = TypePrompt, ev.Text
	case transcript.ExportMessage:
		e.Type, e.Text, e.Model = TypeAssistant, ev.Text, ev.Model
		e.Usage, e.StopReason = usageOf(ev.Usage), ev.StopReason
	case transcript.ExportToolCall:
		e.Type, e.Tool, e.ToolUseID, e.Input, e.Model = TypeToolCall, ev.Tool, ev.ToolUseID, ev.Input, ev.Model
		e.Usage, e.StopReason = usageOf(ev.Usage), ev.StopReason
	case transcript.ExportToolResult:
		e.Type, e.Tool, e.ToolUseID, e.Output, e.IsError, e.Interrupted = TypeToolResult, ev.Tool, ev.ToolUseID, ev.Output, ev.IsError, ev.Interrupted
	case transcript.ExportTitle:
		e.Type, e.Title = TypeTitle, ev.Text
	case transcript.ExportCompact:
		e.Type, e.Trigger, e.TokensBefore, e.TokensAfter = TypeCompacted, ev.Trigger, ev.TokensBefore, ev.TokensAfter
	}
	e.GitBranch, e.Cwd, e.AgentVersion = ev.GitBranch, ev.Cwd, ev.AgentVersion
	return e
}

func usageOf(u *transcript.ExportUsage) *Usage {
	if u == nil {
		return nil
	}
	return &Usage{u.InputTokens, u.OutputTokens, u.CacheCreationInputTokens, u.CacheReadInputTokens}
}

// openLocked creates the pane's transcript file for a conversation whose first
// event was at first.
//
// A live recording's file and an export's default file are named by the
// session. The same conversation gives the same lines, so writing it again is
// how a recording left on across a restart catches up, and an export made twice
// is one file. A file that is there and has no closing line was never finished
// and is written over. One that has a closing line is not touched while the
// new one is written: that goes to a file beside it, which takes its place when
// it is finished and has everything the old one had (see Finish). An export to
// a path of its own never writes over a file.
func (m *Manager) openLocked(meta Meta, first time.Time) error {
	base, err := m.Dir()
	if err != nil {
		return err
	}
	id := SessionID(first, meta)
	folder := filepath.Join(base, recordingsDir, Folder(meta.Project, meta.ProjectRoot))
	if m.export {
		folder = filepath.Join(folder, exportsDir)
	}
	path, flags, final := filepath.Join(folder, id+sessionFileExt), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, ""
	if m.target != "" {
		folder, path, flags = filepath.Dir(m.target), m.target, os.O_WRONLY|os.O_CREATE|os.O_EXCL
	} else if finished(path) {
		final, path = path, path+".new"
	}
	if err := os.MkdirAll(folder, folderMode); err != nil {
		return fmt.Errorf("create the recordings folder: %w", err)
	}
	f, err := os.OpenFile(path, flags, fileMode)
	if err != nil {
		return fmt.Errorf("create the transcript: %w", err)
	}
	m.panes[meta.Pane] = &session{f: f, path: path, final: final, id: id, secret: map[string]bool{}}
	if m.target == "" {
		// Only Flockdeck's own folders are swept, never one beside a file the user
		// chose.
		m.sweepStale(folder)
	}
	if !m.export {
		prune(folder, path, m.now())
	}
	return nil
}

// finished reports whether the file at path is a transcript that ended with its
// closing line.
func finished(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	const tail = 64 << 10
	off := max(fi.Size()-tail, 0)
	buf := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return false
	}
	lines := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	var e Entry
	if json.Unmarshal([]byte(lines[len(lines)-1]), &e) != nil {
		return false
	}
	return e.Type == TypeStopped || e.Type == TypeTruncated
}

// supersedes reports whether the transcript at next has every line the finished
// one at old had but its closing line: whether it is the same transcript, grown.
// Lines are the same if they are the same event -- the same place in the file,
// the same type and the same time -- and not if they are the same bytes, so that
// a later version of Flockdeck that redacts or clips a little differently can
// still replace an earlier one, while a transcript that has lost an event cannot.
func supersedes(old, next string) bool {
	a, err := os.Open(old)
	if err != nil {
		return true // nothing there to lose
	}
	defer a.Close()
	b, err := os.Open(next)
	if err != nil {
		return false
	}
	defer b.Close()
	ra, rb := bufio.NewReaderSize(a, 64<<10), bufio.NewReaderSize(b, 64<<10)
	line := func(r *bufio.Reader) (Entry, bool) {
		var out []byte
		for {
			part, err := r.ReadSlice('\n')
			out = append(out, part...)
			if err == bufio.ErrBufferFull {
				continue
			}
			break
		}
		var e Entry
		if len(out) == 0 {
			return e, false
		}
		_ = json.Unmarshal(out, &e)
		return e, true
	}
	prev, ok := line(ra)
	for ok {
		cur, more := line(ra)
		if !more {
			return true // prev was the closing line
		}
		theirs, has := line(rb)
		if !has || prev.Seq != theirs.Seq || prev.Type != theirs.Type || prev.Time != theirs.Time {
			return false
		}
		prev = cur
	}
	return true
}

// removeSuperseded deletes the other transcripts of the same conversation in a
// folder, which an earlier first event gave another name: a conversation is one
// file. Only files whose first line says the same conversation are touched.
func removeSuperseded(folder string, meta Meta, keep string) {
	conv := meta.Conversation
	if conv == "" {
		conv = meta.Pane
	}
	ents, err := os.ReadDir(folder)
	if err != nil {
		return
	}
	suffix := "-" + shortConversation(meta) + sessionFileExt
	for _, e := range ents {
		if e.Name() == keep || !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), suffix) {
			continue
		}
		p := filepath.Join(folder, e.Name())
		var first Entry
		if b, err := readFirstLine(p); err == nil && json.Unmarshal(b, &first) == nil && first.Type == TypeStarted && first.Conversation == conv && finished(p) {
			_ = os.Remove(p)
		}
	}
}

// cutID is an id or a name cut to a length no real one reaches, for the fields
// of a line that are not clipped as text is.
func cutID(s string) string {
	const limit = 256
	if len(s) <= limit {
		return s
	}
	s = s[:limit]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// prepareLocked applies what the transcript tracks across lines to e: which
// tool calls touched a secret file.
func (m *Manager) prepareLocked(s *session, e *Entry) {
	switch e.Type {
	case TypeToolCall:
		secret := touchesSecretFile(e.Input)
		if e.ToolUseID != "" {
			s.secret[e.ToolUseID] = secret
		}
		s.lastSecret = secret
		if secret {
			e.Input, e.Redacted = withholdInput(e.Input), true
		}
	case TypeToolResult:
		secret := s.lastSecret
		if e.ToolUseID != "" {
			if v, ok := s.secret[e.ToolUseID]; ok {
				secret = v
				delete(s.secret, e.ToolUseID)
			}
		}
		if secret && e.Output != "" {
			e.Output, e.Redacted = Withheld, true
		}
	}
}

func (m *Manager) writeLocked(meta Meta, e Entry) error {
	s := m.panes[meta.Pane]
	if s == nil || s.capped {
		return nil
	}
	e.V = Version
	e.Seq = s.seq + 1
	e.Session = s.id
	e.Pane, e.PaneName, e.Project = cutID(meta.Pane), meta.PaneName, meta.Project
	e.Agent, e.Conversation = meta.Agent, cutID(meta.Conversation)
	e.Tool, e.ToolUseID = cutID(e.Tool), cutID(e.ToolUseID)
	e.AgentVersion, e.Trigger = cutID(e.AgentVersion), cutID(e.Trigger)
	sanitise(&e)
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if s.size+int64(len(line)) > m.max {
		s.capped = true
		end, _ := json.Marshal(Entry{V: Version, Seq: e.Seq, Session: s.id, Time: e.Time, Pane: e.Pane, PaneName: meta.PaneName, Project: meta.Project, Type: TypeTruncated,
			Text: fmt.Sprintf("the transcript reached its size cap of %d MiB and ended here", m.max>>20)})
		if _, err := s.f.Write(append(end, '\n')); err != nil {
			return err
		}
		return nil
	}
	n, err := s.f.Write(line)
	if err != nil {
		return err
	}
	s.size += int64(n)
	s.seq++
	return nil
}

// sanitise is the last step before a line is written, and runs for every one:
// secrets out, long text clipped. It is here and not at each call so that no
// path to the file can skip it.
func sanitise(e *Entry) {
	limit := MaxFieldBytes
	if e.Type == TypePrompt || e.Type == TypeAssistant {
		limit = MaxMessageBytes
	}
	clean := func(name string, v *string, limit int) {
		orig, was := receivedLen(*v)
		r := Redact(*v)
		if r != *v {
			e.Redacted = true
		}
		c := Clip(r, limit)
		if c != r {
			was = true
		}
		if was {
			if e.Clipped == nil {
				e.Clipped = map[string]int{}
			}
			e.Clipped[name] = orig
		}
		*v = c
	}
	clean("text", &e.Text, limit)
	clean("title", &e.Title, MaxFieldBytes)
	clean("cwd", &e.Cwd, MaxFieldBytes)
	clean("gitBranch", &e.GitBranch, MaxFieldBytes)
	clean("output", &e.Output, limit)
	clean("detail", &e.Detail, MaxFieldBytes)
	clean("reason", &e.Reason, MaxFieldBytes)
	if e.Input != nil {
		before, _ := json.Marshal(e.Input)
		redacted := RedactValue(e.Input)
		if after, _ := json.Marshal(redacted); string(after) != string(before) {
			e.Redacted = true
		}
		e.Input = ClipValue(redacted, MaxFieldBytes)
		clipped, _ := json.Marshal(e.Input)
		redactedJSON, _ := json.Marshal(redacted)
		if string(clipped) != string(redactedJSON) || clipMarkerRe.Match(before) {
			if e.Clipped == nil {
				e.Clipped = map[string]int{}
			}
			e.Clipped["input"] = len(before)
		}
	}
}

// clipMarkerRe finds the marker Clip leaves, including at the end of a string
// an earlier stage (the hook) already clipped.
var clipMarkerRe = regexp.MustCompile(`…\[clipped (\d+) bytes\]`)

// receivedLen is a string's length in bytes as it was before any stage cut it,
// and whether one did: a string that already ends in Clip's marker, from the
// hook, was cut by that many bytes more than it shows.
func receivedLen(s string) (int, bool) {
	loc := clipMarkerRe.FindStringSubmatchIndex(s)
	if loc == nil || loc[1] != len(s) {
		return len(s), false
	}
	n, _ := strconv.Atoi(s[loc[2]:loc[3]])
	return loc[0] + n, true
}

// touchesSecretFile reports whether a tool call's input names a secret file --
// a path argument, or a word of a shell command -- by the name check
// review.SecretPath makes.
func touchesSecretFile(in any) bool {
	obj, ok := in.(map[string]any)
	if !ok {
		return false
	}
	for k, v := range obj {
		s, ok := v.(string)
		if !ok {
			continue
		}
		switch k {
		case "file_path", "path", "notebook_path", "filePath":
			if secretFile(s) {
				return true
			}
		case "command":
			for _, w := range strings.Fields(s) {
				if secretFile(strings.Trim(w, `"';`)) {
					return true
				}
			}
		}
	}
	return false
}

// withholdInput keeps a secret file's path and drops everything else the call
// carried, which for a write is the secret itself.
func withholdInput(in any) any {
	obj, ok := in.(map[string]any)
	if !ok {
		return in
	}
	out := map[string]any{}
	for k, v := range obj {
		switch k {
		case "file_path", "path", "notebook_path", "filePath", "command":
			out[k] = v
		default:
			if _, isStr := v.(string); isStr {
				out[k] = Withheld
			} else {
				out[k] = v
			}
		}
	}
	return out
}

// prune applies the retention rule to one project's folder: files older than
// RetainDays go, then the oldest until what is left is within RetainFiles and
// RetainBytes. The file just opened is never removed.
func prune(folder, keep string, now time.Time) {
	cutoff := now.Add(-RetainDays * 24 * time.Hour)
	var kept []Info
	for _, in := range listFolder(folder) {
		if in.Path != keep && in.Modified.Before(cutoff) {
			_ = os.Remove(in.Path)
			continue
		}
		kept = append(kept, in)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].Modified.Before(kept[j].Modified) })
	var total int64
	for _, in := range kept {
		total += in.Size
	}
	count := len(kept)
	for _, in := range kept {
		if count <= RetainFiles && total <= RetainBytes {
			break
		}
		if in.Path == keep {
			continue
		}
		if os.Remove(in.Path) == nil {
			total -= in.Size
			count--
		}
	}
}

// Info describes one recording on disk.
type Info struct {
	Path     string    `json:"path"`
	Folder   string    `json:"folder"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	// Started, Project, PaneName, Pane, Agent and Model are read from the
	// file's first line, and are empty for a file whose first line is not the
	// start of a recording.
	Started  string `json:"started,omitempty"`
	Project  string `json:"project,omitempty"`
	PaneName string `json:"paneName,omitempty"`
	Pane     string `json:"pane,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Model    string `json:"model,omitempty"`
}

func listFolder(folder string) []Info {
	ents, err := os.ReadDir(folder)
	if err != nil {
		return nil
	}
	var out []Info
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), sessionFileExt) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Info{Path: filepath.Join(folder, e.Name()), Folder: filepath.Base(folder), Size: fi.Size(), Modified: fi.ModTime()})
	}
	return out
}

// EnsureRoot is Root, made if it is not there yet.
func EnsureRoot(dir func() (string, error)) (string, error) {
	root, err := Root(dir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, folderMode); err != nil {
		return "", fmt.Errorf("create the recordings folder: %w", err)
	}
	return root, nil
}

// Root is the folder every project's folder is under.
func Root(dir func() (string, error)) (string, error) {
	base, err := dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, recordingsDir), nil
}

// List returns every recording on disk, newest first.
func List(dir func() (string, error)) ([]Info, error) {
	root, err := Root(dir)
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Info
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		for _, in := range listFolder(filepath.Join(root, e.Name())) {
			var first Entry
			if b, err := readFirstLine(in.Path); err == nil && json.Unmarshal(b, &first) == nil && first.Type == TypeStarted {
				in.Started, in.Project, in.PaneName, in.Pane, in.Agent, in.Model = first.Time, first.Project, first.PaneName, first.Pane, first.Agent, first.Model
			}
			out = append(out, in)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}

func readFirstLine(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, 16<<10)
	n, _ := f.Read(buf)
	if i := strings.IndexByte(string(buf[:n]), '\n'); i >= 0 {
		n = i
	}
	return buf[:n], nil
}

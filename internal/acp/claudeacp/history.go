package claudeacp

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// session/load replays the conversation from claude's own stored session, a
// JSON Lines file at <config dir>/projects/<project>/<session id>.jsonl. claude
// documents no format for it, so everything here is best effort: a line that
// does not parse, a shape that is not known and a file that is missing or odd
// leave the replay shorter or empty, never an error. The session still opens.
const (
	// historyMaxEntries and historyMaxBytes bound what is replayed. When the
	// conversation is longer the oldest part is left out. The bridge has limits
	// of its own; these keep the adapter's memory and the pipe small.
	historyMaxEntries = 2000
	historyMaxBytes   = 4 << 20
	// historyMaxLine is the longest line read; a longer one (a pasted file, an
	// image) is skipped without being held in memory.
	historyMaxLine = 1 << 20

	// historyMaxWait is how long reading may take.
	historyMaxWait = 5 * time.Second
	// historyMaxText is the longest text of one message.
	historyMaxText = 64 << 10
	// historyMaxDirs is how many project directories are looked through for the
	// file when the cwd's own does not have it.
	historyMaxDirs = 5000
)

// historyMaxScan is how much of the end of the file is read. A variable so
// that a test need not write 64 MiB.
var historyMaxScan int64 = 64 << 20

// historyRoot is claude's projects directory: CLAUDE_CONFIG_DIR in the
// environment claude gets, else .claude in the home directory.
func (o *Options) historyRoot() string {
	if o.HistoryDir != "" {
		return o.HistoryDir
	}
	for i := len(o.Env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(o.Env[i], "CLAUDE_CONFIG_DIR="); ok && v != "" {
			return filepath.Join(v, "projects")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

var nonWord = regexp.MustCompile(`[^A-Za-z0-9]`)

// historyFile finds the stored session id (a canonical UUID, so it is nothing
// but hex digits and dashes): in the directory claude names after the cwd, and
// failing that in any project directory, since claude's spelling of the cwd
// may not be ours. It returns "" when there is no regular file.
func historyFile(root, cwd, id string) string {
	if root == "" {
		return ""
	}
	name := id + ".jsonl"
	try := func(dir string) string {
		p := filepath.Join(root, dir, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p
		}
		return ""
	}
	if p := try(nonWord.ReplaceAllString(cwd, "-")); p != "" {
		return p
	}
	d, err := os.Open(root)
	if err != nil {
		return ""
	}
	defer d.Close()
	names, _ := d.Readdirnames(historyMaxDirs)
	for _, n := range names {
		if p := try(n); p != "" {
			return p
		}
	}
	return ""
}

type histLine struct {
	Type              string          `json:"type"`
	IsMeta            bool            `json:"isMeta"`
	IsSidechain       bool            `json:"isSidechain"`
	IsCompactSummary  bool            `json:"isCompactSummary"`
	IsAPIErrorMessage bool            `json:"isApiErrorMessage"`
	Error             json.RawMessage `json:"error"`
	Message           struct {
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// histEntry is one update to replay, with the size it counts for.
type histEntry struct {
	u    map[string]any
	size int
	tool string // the tool call id, for a tool_call
}

// history collects the entries, newest last, dropping the oldest past the caps.
type history struct {
	cwd     string
	entries []*histEntry
	head    int
	bytes   int
	tools   map[string]*histEntry
}

func (h *history) add(u map[string]any, size int, tool string) *histEntry {
	e := &histEntry{u: u, size: size + 64, tool: tool}
	h.entries = append(h.entries, e)
	h.bytes += e.size
	if tool != "" {
		h.tools[tool] = e
	}
	for len(h.entries)-h.head > historyMaxEntries || (h.bytes > historyMaxBytes && len(h.entries)-h.head > 1) {
		old := h.entries[h.head]
		h.entries[h.head] = nil
		h.head++
		h.bytes -= old.size
		if old.tool != "" {
			delete(h.tools, old.tool)
		}
	}
	if h.head >= 1024 {
		h.entries = append([]*histEntry(nil), h.entries[h.head:]...)
		h.head = 0
	}
	return e
}

func chunk(kind, text string) map[string]any {
	return map[string]any{"sessionUpdate": kind, "content": textContent(text)}
}

var (
	cmdName = regexp.MustCompile(`(?s)<command-name>\s*(.*?)\s*</command-name>`)
	cmdArgs = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
)

// promptText is the person's words in a user line, or "" when the line is not
// a prompt: a tool result, a note claude wrote to itself, the output of a local
// command, an interruption marker.
func promptText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		var blocks []block
		if json.Unmarshal(raw, &blocks) != nil {
			return ""
		}
		var parts []string
		for _, b := range blocks {
			switch b.Type {
			case "text":
				parts = append(parts, b.Text)
			case "image":
				parts = append(parts, "[image]")
			case "tool_result":
				return ""
			}
		}
		s = strings.Join(parts, "\n")
	}
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return ""
	case strings.HasPrefix(s, "<command-name>"), strings.HasPrefix(s, "<command-message>"):
		m := cmdName.FindStringSubmatch(s)
		if m == nil {
			return ""
		}
		text := m[1]
		if a := cmdArgs.FindStringSubmatch(s); a != nil && strings.TrimSpace(a[1]) != "" {
			text += " " + strings.TrimSpace(a[1])
		}
		return text
	case strings.HasPrefix(s, "<local-command-"), strings.HasPrefix(s, "<system-reminder>"),
		strings.HasPrefix(s, "Caveat: The messages below"), strings.HasPrefix(s, "[Request interrupted"):
		return ""
	}
	return capString(s, historyMaxText)
}

func capString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// fold adds what one line says to the history.
func (h *history) fold(l histLine) {
	if l.IsSidechain || l.IsMeta || l.IsCompactSummary || l.IsAPIErrorMessage {
		return
	}
	switch l.Type {
	case "user":
		var blocks []block
		if json.Unmarshal(l.Message.Content, &blocks) == nil {
			for _, b := range blocks {
				if b.Type == "tool_result" {
					h.result(b)
				}
			}
		}
		if t := promptText(l.Message.Content); t != "" {
			h.add(chunk("user_message_chunk", t), len(t), "")
		}
	case "assistant":
		// A line with an error is claude's own note about a failed request, and a
		// synthetic message is text claude made up locally; neither is the
		// agent's answer.
		if (len(l.Error) > 0 && string(l.Error) != "null") || l.Message.Model == "<synthetic>" {
			return
		}
		var blocks []block
		if json.Unmarshal(l.Message.Content, &blocks) != nil {
			return
		}
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if b.Text != "" {
					t := capString(b.Text, historyMaxText)
					h.add(chunk("agent_message_chunk", t), len(t), "")
				}
			case "thinking":
				// claude stores thinking as an empty string with a signature
				// for most models; there is nothing to show then.
				if b.Thinking != "" {
					t := capString(b.Thinking, historyMaxText)
					h.add(chunk("agent_thought_chunk", t), len(t), "")
				}
			case "tool_use":
				h.tool(b)
			}
		}
	}
}

func (h *history) tool(b block) {
	if b.ID == "" || h.tools[b.ID] != nil {
		return
	}
	d := describeTool(b.Name, b.Input, h.cwd, "")
	if d.plan != nil {
		return // a plan is current state, not history
	}
	u := map[string]any{"sessionUpdate": "tool_call", "toolCallId": b.ID, "title": d.title, "kind": d.kind, "status": "pending"}
	size := len(d.title)
	if len(b.Input) > 0 && len(b.Input) <= historyMaxText {
		u["rawInput"] = b.Input
		size += len(b.Input)
	}
	if len(d.locations) > 0 {
		u["locations"] = d.locations
	}
	if len(d.content) > 0 {
		u["content"] = d.content
		if raw, err := json.Marshal(d.content); err == nil {
			size += len(raw)
		}
	}
	h.add(u, size, b.ID)
}

// result settles the tool call a result belongs to in place, so that a call
// and its outcome are one entry and one is never kept without the other. A
// result for a call that was left out, or never seen, is dropped.
func (h *history) result(b block) {
	e := h.tools[b.ToolUseID]
	if e == nil {
		return
	}
	status := "completed"
	if b.IsError {
		status = "failed"
	}
	e.u["status"] = status
	// An edit keeps the diff it was announced with, as in a live turn.
	if text := resultText(b.Content); text != "" && !(e.u["kind"] == "edit" && !b.IsError) {
		text = capString(text, historyMaxText)
		e.u["content"] = []any{map[string]any{"type": "content", "content": textContent(text)}}
		e.size += len(text)
		h.bytes += len(text)
	}
}

// readHistory reads the tail of the file at path into entries. It stops at
// historyMaxWait, and skips what it cannot read.
func readHistory(path, cwd string) []*histEntry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return nil
	}
	r := bufio.NewReaderSize(f, 64<<10)
	if fi.Size() > historyMaxScan {
		if _, err := f.Seek(fi.Size()-historyMaxScan, io.SeekStart); err != nil {
			return nil
		}
		r.Reset(f)
		skipLine(r) // the first line is cut
	}
	h := &history{cwd: cwd, tools: map[string]*histEntry{}}
	deadline := time.Now().Add(historyMaxWait)
	for n := 0; ; n++ {
		if n%256 == 0 && time.Now().After(deadline) {
			break
		}
		line, err := readLine(r)
		if len(line) > 0 {
			var l histLine
			if json.Unmarshal(line, &l) == nil {
				h.fold(l)
			}
		}
		if err != nil {
			break
		}
	}
	return h.entries[h.head:]
}

// readLine returns the next line, or nil for one longer than historyMaxLine
// (which is read past and not kept). The error is only io.EOF or a read error.
func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	long := false
	for {
		part, err := r.ReadSlice('\n')
		if !long {
			if len(line)+len(part) > historyMaxLine {
				long, line = true, nil
			} else {
				line = append(line, part...)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if long {
			return nil, err
		}
		return line, err
	}
}

func skipLine(r *bufio.Reader) {
	for {
		if _, err := r.ReadSlice('\n'); err != bufio.ErrBufferFull {
			return
		}
	}
}

// replayHistory sends the stored conversation of a session being loaded as
// replayed updates, before the answer to session/load. They carry sessionID as
// the client gave it, since the client marks updates for that id as replay.
func (s *server) replayHistory(sessionID, id, cwd string) {
	path := historyFile(s.opts.historyRoot(), cwd, id)
	if path == "" {
		return
	}
	for _, e := range readHistory(path, cwd) {
		s.update(sessionID, e.u)
	}
}

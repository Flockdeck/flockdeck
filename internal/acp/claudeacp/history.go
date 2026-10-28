package claudeacp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
	// historyMaxText is the longest text of one message.
	historyMaxText = 64 << 10
)

// These are variables so that a test need not write 16 MiB or wait 5 seconds.
var (
	// historyMaxScan is how much of the end of the file is read: the newest
	// part, which is what the entry and byte caps keep anyway. A line of the
	// file is about as large as its text, plus claude's own bookkeeping lines.
	historyMaxScan int64 = 16 << 20
	// historyMaxWait is how long reading may take.
	historyMaxWait = 5 * time.Second
	// historyMaxDirs is how many project directories the fallback search looks
	// through. A root with more is not searched at all.
	historyMaxDirs = 5000
	// historyMaxLinks is how many lines the branch search keeps a link for. A
	// file with more is replayed in file order.
	historyMaxLinks = 400000
	// historyHead is how much of the start of a line too long to read is kept,
	// to find the parent it names.
	historyHead = 512
	// historyTail is the same for the end, where the uuid is, behind the message.
	historyTail = 4096
)

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
// failing that in the project directories in name order, since claude's
// spelling of the cwd may not be ours (it also shortens a path over 200
// characters and adds a hash, which is not reproduced here). A symbolic link or
// junction, for the file or its directory, is not followed. It returns "" when
// there is no regular file, or the root has more than historyMaxDirs entries
// and the cwd's own directory does not have it.
func historyFile(root, cwd, id string) string {
	if root == "" {
		return ""
	}
	name := id + ".jsonl"
	try := func(dir string) string {
		d := filepath.Join(root, dir)
		if fi, err := os.Lstat(d); err != nil || !fi.IsDir() {
			return ""
		}
		p := filepath.Join(d, name)
		if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
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
	names, _ := d.Readdirnames(historyMaxDirs + 1)
	if len(names) > historyMaxDirs {
		return ""
	}
	sort.Strings(names)
	for _, n := range names {
		if p := try(n); p != "" {
			return p
		}
	}
	return ""
}

type histLine struct {
	UUID                      string          `json:"uuid"`
	Type                      string          `json:"type"`
	IsMeta                    bool            `json:"isMeta"`
	IsSidechain               bool            `json:"isSidechain"`
	IsCompactSummary          bool            `json:"isCompactSummary"`
	IsAPIErrorMessage         bool            `json:"isApiErrorMessage"`
	IsVisibleInTranscriptOnly bool            `json:"isVisibleInTranscriptOnly"`
	Error                     json.RawMessage `json:"error"`
	Origin                    *histOrigin     `json:"origin"`
	Message                   struct {
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	// Attachment is set for type "attachment". A prompt typed while a turn runs
	// is stored only as one of kind queued_command.
	Attachment struct {
		Type        string          `json:"type"`
		CommandMode string          `json:"commandMode"`
		IsMeta      bool            `json:"isMeta"`
		Origin      *histOrigin     `json:"origin"`
		Prompt      json.RawMessage `json:"prompt"`
	} `json:"attachment"`
}

// histOrigin says who a user line came from: "human" for the person, "peer"
// for a message from another session, "task-notification" for a finished
// background task. A line with no origin (older claude) is taken as the person's.
type histOrigin struct {
	Kind string `json:"kind"`
}

func (o *histOrigin) human() bool { return o == nil || o.Kind == "" || o.Kind == "human" }

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
	// evicted says entries were dropped for the caps; gap says a line was
	// skipped for its length.
	evicted, gap bool
}

func (h *history) add(u map[string]any, size int, tool string) *histEntry {
	e := &histEntry{u: u, size: size + 64, tool: tool}
	h.entries = append(h.entries, e)
	h.bytes += e.size
	if tool != "" {
		h.tools[tool] = e
	}
	h.evict()
	return e
}

// evict drops the oldest entries until the caps hold.
func (h *history) evict() {
	for len(h.entries)-h.head > historyMaxEntries || (h.bytes > historyMaxBytes && len(h.entries)-h.head > 1) {
		old := h.entries[h.head]
		h.entries[h.head] = nil
		h.head++
		h.bytes -= old.size
		h.evicted = true
		if old.tool != "" {
			delete(h.tools, old.tool)
		}
	}
	if h.head >= 1024 {
		h.entries = append([]*histEntry(nil), h.entries[h.head:]...)
		h.head = 0
	}
}

func chunk(kind, text string) map[string]any {
	return map[string]any{"sessionUpdate": kind, "content": textContent(text)}
}

// element is the start of a tag with a dash in its name, which is how claude
// marks what it wrote itself (command-name, local-command-stdout,
// task-notification, bash-input, system-reminder, user-prompt-submit-hook). A
// tag without one, such as the pasted_content around a paste, is the person's
// text.
var element = regexp.MustCompile(`^<([A-Za-z][A-Za-z0-9]*(?:-[A-Za-z0-9]+)+)(?:\s[^>]*)?>`)

// stripWrappers removes the claude-written elements from the start of s and
// returns what is left, and the slash command the elements named, if any. An
// element that is not closed runs to the end.
func stripWrappers(s string) (rest, command string) {
	for {
		s = strings.TrimSpace(s)
		m := element.FindStringSubmatch(s)
		if m == nil {
			return s, command
		}
		end := strings.Index(s, "</"+m[1]+">")
		body, next := s[len(m[0]):], ""
		if end >= 0 {
			body, next = s[len(m[0]):end], s[end+len(m[1])+3:]
		}
		if m[1] == "command-name" {
			command = strings.TrimSpace(body)
		}
		if m[1] == "command-args" && command != "" && strings.TrimSpace(body) != "" {
			command += " " + strings.TrimSpace(body)
		}
		s = next
	}
}

// promptText is the person's words in a user line's content, or "" when the
// line is not a prompt: a tool result, or something claude wrote.
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
	rest, command := stripWrappers(s)
	switch {
	case command != "":
		return capString(command, historyMaxText)
	case strings.HasPrefix(rest, "Caveat: The messages below"), strings.HasPrefix(rest, "[Request interrupted"):
		return ""
	}
	return capString(rest, historyMaxText)
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
	if l.IsSidechain || l.IsMeta || l.IsCompactSummary || l.IsAPIErrorMessage || l.IsVisibleInTranscriptOnly {
		return
	}
	switch l.Type {
	case "attachment":
		a := l.Attachment
		if a.Type != "queued_command" || a.CommandMode != "prompt" || a.IsMeta || !a.Origin.human() {
			return
		}
		if t := promptText(a.Prompt); t != "" {
			h.add(chunk("user_message_chunk", t), len(t), "")
		}
	case "user":
		var blocks []block
		if json.Unmarshal(l.Message.Content, &blocks) == nil {
			for _, b := range blocks {
				if b.Type == "tool_result" {
					h.result(b)
				}
			}
		}
		if !l.Origin.human() {
			return
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
		h.evict()
	}
}

// historyResult is what readHistory found.
type historyResult struct {
	entries []*histEntry
	// earlier says the oldest part of the conversation is not in entries (the
	// file is longer than the part read, or the caps dropped entries); later
	// says reading stopped at the time limit before the newest part; gap says
	// a line in between was too long to read.
	earlier, later, gap bool
}

// histLink is what the branch search needs of a line.
type histLink struct {
	UUID       string `json:"uuid"`
	ParentUUID string `json:"parentUuid"`
	Type       string `json:"type"`
	Sidechain  bool   `json:"isSidechain"`
}

// activeBranch returns the uuids of the lines on the branch that ends at the
// tip, and whether the chain was followed to its start. claude does not delete
// what a rewind drops: a session cut back with --resume-session-at gets its next
// message appended to the same file, with the kept message as its parent, so the
// dropped turns stay in the file after it and file order is not the
// conversation. Lines name their parent (parentUuid), so the conversation is
// the chain from the newest line back. The start is a line with no parent (the
// first line, or the one after a compaction); a parent that is not among the
// links, or a loop, is not a start.
func activeBranch(links map[string]string, tip string) (active map[string]bool, whole bool) {
	active = map[string]bool{}
	for id := tip; !active[id]; {
		active[id] = true
		parent, ok := links[id]
		if !ok {
			return active, false
		}
		if parent == "" {
			return active, true
		}
		id = parent
	}
	return active, false
}

// headLink reads the uuid and parent of a line that was too long to keep from
// the two ends of it: claude writes parentUuid first and uuid after the
// message. The text of the message is JSON-escaped, so a quote in it is never
// one of these. whole is false when an end lacks its member, and the line is
// then a gap in the chain.
var (
	linkUUID   = regexp.MustCompile(`"uuid":"([0-9A-Za-z-]{1,64})"`)
	linkParent = regexp.MustCompile(`"parentUuid":(?:null|"([0-9A-Za-z-]{1,64})")`)
)

func headLink(head, tail []byte) (uuid, parent string, whole bool) {
	p := linkParent.FindSubmatch(head)
	us := linkUUID.FindAllSubmatch(tail, -1)
	if p == nil || len(us) == 0 {
		return "", "", false
	}
	return string(us[len(us)-1][1]), string(p[1]), true
}

// readHistory reads the tail of the file at path into entries. It stops at
// historyMaxWait, and skips what it cannot read.
func readHistory(path, cwd string) historyResult {
	var res historyResult
	f, err := os.Open(path)
	if err != nil {
		return res
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return res
	}
	// pass calls fn for each line of the part of the file that is read, and says
	// whether it stopped at the time limit, which each pass has to itself. fn gets
	// the two ends of a line that was too long to keep in head and tail.
	pass := func(fn func(line, head, tail []byte, skipped bool)) (timedOut bool) {
		deadline := time.Now().Add(historyMaxWait)
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return false
		}
		r := bufio.NewReaderSize(f, 64<<10)
		if fi.Size() > historyMaxScan {
			if _, err := f.Seek(fi.Size()-historyMaxScan, io.SeekStart); err != nil {
				return false
			}
			r.Reset(f)
			skipLine(r) // the first line is cut
			res.earlier = true
		}
		for n := 0; ; n++ {
			if n%256 == 0 && !time.Now().Before(deadline) {
				return true
			}
			line, head, tail, skipped, err := readLine(r)
			fn(line, head, tail, skipped)
			if err != nil {
				return false
			}
		}
	}
	// The first pass finds the branch the newest line is on. A link is kept for
	// every line with a uuid; only a conversation line can be the newest.
	links := map[string]string{}
	var tip string
	over, lost := false, false
	timedOut := pass(func(line, head, tail []byte, skipped bool) {
		if over {
			return
		}
		var l histLink
		switch {
		case skipped:
			u, p, whole := headLink(head, tail)
			if !whole {
				lost = true
				return
			}
			l.UUID, l.ParentUUID = u, p
		case len(line) == 0 || !bytes.Contains(line, []byte(`"uuid"`)):
			return
		case json.Unmarshal(line, &l) != nil || l.UUID == "":
			return
		}
		if len(links) >= historyMaxLinks {
			over = true
			return
		}
		links[l.UUID] = l.ParentUUID
		switch l.Type {
		case "user", "assistant", "attachment", "system":
			if !l.Sidechain {
				tip = l.UUID
			}
		}
	})
	// With no branch to follow, a chain that does not reach its start (a line
	// in it was lost, or the file has been changed) and a first pass that did not
	// finish, the lines are replayed in file order. A chain that stops at the
	// start of the part read is the expected end of one, since the part is the
	// newest of the file.
	var active map[string]bool
	if tip != "" && !timedOut && !over && !lost {
		var whole bool
		active, whole = activeBranch(links, tip)
		if !whole && !res.earlier {
			active = nil
		}
	}
	links = nil
	h := &history{cwd: cwd, tools: map[string]*histEntry{}}
	later := pass(func(line, _, _ []byte, skipped bool) {
		if skipped {
			h.gap = true
		}
		// Most lines are bookkeeping; these words are in every line that
		// matters, so the others are not parsed.
		if len(line) > 0 && (bytes.Contains(line, []byte(`"user"`)) || bytes.Contains(line, []byte(`"assistant"`)) || bytes.Contains(line, []byte("queued_command"))) {
			var l histLine
			if json.Unmarshal(line, &l) == nil && (active == nil || l.UUID == "" || active[l.UUID]) {
				h.fold(l)
			}
		}
	})
	res.later = timedOut || later
	res.entries = h.entries[h.head:]
	res.earlier = res.earlier || h.evicted
	res.gap = h.gap
	return res
}

// readLine returns the next line. A line longer than historyMaxLine is read
// past and not kept: line is nil, skipped is true, and head and tail are the
// start (at most historyHead bytes) and the end (at most historyTail bytes) of
// it. The error is only io.EOF or a read error.
func readLine(r *bufio.Reader) (line, head, tail []byte, skipped bool, err error) {
	for {
		part, err := r.ReadSlice('\n')
		if skipped {
			tail = lastBytes(tail, part, historyTail)
		} else if len(line)+len(part) > historyMaxLine {
			skipped = true
			all := append(line, part...)
			head = append([]byte(nil), all[:min(len(all), historyHead)]...)
			tail = lastBytes(nil, all, historyTail)
			line = nil
		} else {
			line = append(line, part...)
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return line, head, tail, skipped, err
	}
}

// lastBytes is the last n bytes of old followed by more.
func lastBytes(old, more []byte, n int) []byte {
	all := append(append([]byte(nil), old...), more...)
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all
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
// Notices come first when part of the conversation is not shown.
func (s *server) replayHistory(sessionID, id, cwd string) {
	path := historyFile(s.opts.historyRoot(), cwd, id)
	if path == "" {
		return
	}
	res := readHistory(path, cwd)
	for _, c := range []struct {
		on   bool
		code string
	}{{res.earlier, "history_cut_earlier"}, {res.later, "history_cut_later"}, {res.gap, "history_cut_gap"}} {
		if c.on {
			s.update(sessionID, map[string]any{"sessionUpdate": "flockdeck_notice", "code": c.code})
		}
	}
	for _, e := range res.entries {
		s.update(sessionID, e.u)
	}
}

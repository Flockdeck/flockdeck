package codexacp

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
)

// item is a thread item as codex describes it. The members whose shape is not
// certain stay raw, so that one odd item does not make the whole notification
// unreadable.
type item struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Text       string          `json:"text"`
	Summary    json.RawMessage `json:"summary"`
	Content    json.RawMessage `json:"content"`
	Command    string          `json:"command"`
	Cwd        string          `json:"cwd"`
	Status     string          `json:"status"`
	Actions    []commandAction `json:"commandActions"`
	Output     *string         `json:"aggregatedOutput"`
	ExitCode   *int            `json:"exitCode"`
	DurationMs *int64          `json:"durationMs"`
	Changes    json.RawMessage `json:"changes"`
	Server     string          `json:"server"`
	Tool       string          `json:"tool"`
	Arguments  json.RawMessage `json:"arguments"`
	Result     json.RawMessage `json:"result"`
	Error      json.RawMessage `json:"error"`
	Query      string          `json:"query"`
	Review     string          `json:"review"`
	Path       string          `json:"path"`
}

type commandAction struct {
	Type string `json:"type"`
}

// itemState is what the adapter remembers about an item while its turn runs.
type itemState struct {
	streamed  bool      // deltas of the text were sent
	summary   bool      // deltas of a reasoning summary were sent
	announced bool      // the tool_call was sent
	desc      *toolDesc // what it was announced with
	out       []byte
	outSent   time.Time
}

const (
	// maxItems bounds the items remembered for one turn.
	maxItems = 4096
	// maxText is the most of a tool's output, or of a file in a diff, sent.
	maxText = 64 << 10
	// maxDiffText is the same for one file of a file change.
	maxDiffText = 256 << 10
	// maxChanges is the most files of one file change shown.
	maxChanges = 50
	// outputEvery spaces the updates of a command's output while it runs.
	outputEvery = 200 * time.Millisecond
)

// state returns the state of an item, making it. It must be called with ss.mu
// held. It returns nil when the turn has too many items to follow.
func (ss *session) state(id string) *itemState {
	st := ss.items[id]
	if st == nil {
		if id == "" || len(ss.items) >= maxItems {
			return nil
		}
		st = &itemState{}
		ss.items[id] = st
	}
	return st
}

func (ss *session) mark(id string, f func(*itemState)) {
	ss.mu.Lock()
	if st := ss.state(id); st != nil {
		f(st)
	}
	ss.mu.Unlock()
}

// rawReasoning reports whether raw reasoning text may be shown for the item:
// it may when no summary was.
func (ss *session) rawReasoning(id string) bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	st := ss.state(id)
	return st == nil || !st.summary
}

func isTool(typ string) bool {
	switch typ {
	case "commandExecution", "fileChange", "mcpToolCall", "dynamicToolCall", "webSearch",
		"imageView", "imageGeneration", "collabAgentToolCall", "subAgentActivity":
		return true
	}
	return false
}

func toolStatus(s, def string) string {
	switch s {
	case "inProgress", "in_progress":
		return "in_progress"
	case "completed":
		return "completed"
	case "failed", "declined":
		return "failed"
	}
	return def
}

type toolDesc struct {
	truncated bool // what is shown is not all of what codex will do
	title     string
	kind      string
	rawInput  any
	locations []map[string]any
	content   []any
}

func firstLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i] + " ..."
	}
	if r := []rune(s); len(r) > n {
		s = string(r[:n]) + "..."
	}
	return s
}

// capText cuts s to n bytes, at a character boundary.
func capText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "\n[output cut]"
}

func textBlock(s string) []any {
	return []any{map[string]any{"type": "content", "content": textContent(s)}}
}

func (ss *session) abs(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(ss.cwd, p)
}

func (ss *session) describe(it *item) toolDesc {
	d := toolDesc{kind: "other", title: it.Type}
	switch it.Type {
	case "commandExecution":
		d.kind = "execute"
		d.title = firstLine(it.Command, 120)
		if d.title == "" {
			d.title = "Run a command"
		}
		d.rawInput = map[string]any{"command": it.Command, "cwd": it.Cwd}
		if len(it.Actions) > 0 {
			read, search := true, true
			for _, a := range it.Actions {
				read = read && a.Type == "read"
				search = search && (a.Type == "listFiles" || a.Type == "search")
			}
			switch {
			case read:
				d.kind = "read"
			case search:
				d.kind = "search"
			}
		}
	case "fileChange":
		d.kind, d.title = "edit", "Edit files"
		d.content, d.locations, d.truncated = ss.fileChanges(it.Changes)
		if len(d.locations) == 1 {
			d.title = "Edit " + d.locations[0]["path"].(string)
		} else if len(d.locations) > 1 {
			d.title = "Edit " + itoa(len(d.locations)) + " files"
		}
	case "mcpToolCall":
		d.title = strings.Trim(it.Server+": "+it.Tool, ": ")
		if len(it.Arguments) > 0 {
			d.rawInput = it.Arguments
		}
	case "dynamicToolCall":
		d.title = it.Tool
		if d.title == "" {
			d.title = "Tool call"
		}
	case "webSearch":
		d.kind, d.title = "fetch", "Search the web: "+firstLine(it.Query, 100)
	case "imageView":
		d.kind, d.title = "read", "View "+it.Path
		d.locations = loc(ss.abs(it.Path))
	case "imageGeneration":
		d.title = "Generate an image"
	case "collabAgentToolCall", "subAgentActivity":
		d.kind, d.title = "think", "Sub-agent"
	}
	if strings.TrimSpace(d.title) == "" {
		d.title = it.Type
	}
	return d
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

func loc(p string) []map[string]any {
	if p == "" {
		return nil
	}
	return []map[string]any{{"path": p}}
}

// kindName reads the kind of a file change, which is a string or an object
// with a type member.
func kindName(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &o)
	return o.Type
}

// fileChanges turns the changes of a file change item into ACP content and
// locations. Codex gives the contents of a file it adds or deletes as the
// diff, and a unified diff for an update; an update is shown as that text.
func (ss *session) fileChanges(raw json.RawMessage) ([]any, []map[string]any, bool) {
	var changes []struct {
		Path string          `json:"path"`
		Kind json.RawMessage `json:"kind"`
		Diff string          `json:"diff"`
	}
	if json.Unmarshal(raw, &changes) != nil {
		return nil, nil, false
	}
	var content []any
	var locs []map[string]any
	truncated := false
	for _, c := range changes {
		if len(locs) >= maxChanges {
			break
		}
		path := ss.abs(c.Path)
		locs = append(locs, map[string]any{"path": path})
		text := capText(c.Diff, maxDiffText)
		truncated = truncated || len(c.Diff) > maxDiffText
		switch kindName(c.Kind) {
		case "add":
			content = append(content, map[string]any{"type": "diff", "path": path, "oldText": nil, "newText": text})
		case "delete":
			content = append(content, map[string]any{"type": "diff", "path": path, "oldText": text, "newText": ""})
		default:
			content = append(content, map[string]any{"type": "content", "content": textContent(path + "\n" + text)})
		}
	}
	if more := len(changes) - len(locs); more > 0 {
		truncated = true
		content = append(content, map[string]any{"type": "content", "content": textContent(itoa(more) + " more files are not shown")})
	}
	return content, locs, truncated
}

func strList(raw json.RawMessage) []string {
	var out []string
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

// resultText is the text of an MCP tool result or error.
func resultText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return capText(s, maxText)
	}
	var r struct {
		Message string `json:"message"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return ""
	}
	var parts []string
	if r.Message != "" {
		parts = append(parts, r.Message)
	}
	for _, c := range r.Content {
		switch c.Type {
		case "text":
			parts = append(parts, c.Text)
		case "image":
			parts = append(parts, "[image]")
		}
	}
	return capText(strings.Join(parts, "\n"), maxText)
}

func (ss *session) itemStarted(it *item) {
	if !isTool(it.Type) {
		ss.mark(it.ID, func(*itemState) {})
		return
	}
	d := ss.describe(it)
	ss.mu.Lock()
	st := ss.state(it.ID)
	already := st != nil && st.announced
	if st != nil {
		st.announced = true
		st.desc = &d
	}
	ss.mu.Unlock()
	if already {
		// An approval request got there first. Fill in what it did not know.
		u := map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": it.ID, "title": d.title, "kind": d.kind,
			"status": toolStatus(it.Status, "in_progress")}
		if len(d.content) > 0 {
			u["content"] = d.content
		}
		if len(d.locations) > 0 {
			u["locations"] = d.locations
		}
		ss.emit(u)
		return
	}
	ss.emit(ss.toolCall(it.ID, d, toolStatus(it.Status, "in_progress")))
}

func (ss *session) toolCall(id string, d toolDesc, status string) map[string]any {
	u := map[string]any{"sessionUpdate": "tool_call", "toolCallId": id, "title": d.title, "kind": d.kind, "status": status}
	if d.rawInput != nil {
		u["rawInput"] = d.rawInput
	}
	if len(d.locations) > 0 {
		u["locations"] = d.locations
	}
	if len(d.content) > 0 {
		u["content"] = d.content
	}
	return u
}

func (ss *session) itemCompleted(it *item) {
	ss.mu.Lock()
	var st itemState
	if p := ss.items[it.ID]; p != nil {
		st = *p
		delete(ss.items, it.ID)
	}
	ss.mu.Unlock()
	switch it.Type {
	case "agentMessage", "plan":
		if !st.streamed && strings.TrimSpace(it.Text) != "" {
			ss.emit(map[string]any{"sessionUpdate": "agent_message_chunk", "content": textContent(it.Text)})
		}
	case "exitedReviewMode":
		if strings.TrimSpace(it.Review) != "" {
			ss.emit(map[string]any{"sessionUpdate": "agent_message_chunk", "content": textContent(it.Review)})
		}
	case "reasoning":
		if !st.streamed {
			parts := strList(it.Summary)
			if len(parts) == 0 {
				parts = strList(it.Content)
			}
			if text := strings.Join(parts, "\n\n"); strings.TrimSpace(text) != "" {
				ss.emit(map[string]any{"sessionUpdate": "agent_thought_chunk", "content": textContent(text)})
			}
		}
	default:
		if !isTool(it.Type) {
			return
		}
		status := toolStatus(it.Status, "completed")
		if !st.announced {
			d := ss.describe(it)
			ss.emit(ss.toolCall(it.ID, d, status))
		}
		u := map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": it.ID, "status": status}
		switch it.Type {
		case "commandExecution":
			out := string(st.out)
			if it.Output != nil {
				out = *it.Output
			}
			if out != "" {
				u["content"] = textBlock(capText(out, maxText))
			}
			raw := map[string]any{}
			if it.ExitCode != nil {
				raw["exitCode"] = *it.ExitCode
			}
			if it.DurationMs != nil {
				raw["durationMs"] = *it.DurationMs
			}
			if len(raw) > 0 {
				u["rawOutput"] = raw
			}
		case "fileChange":
			if content, _, _ := ss.fileChanges(it.Changes); len(content) > 0 {
				u["content"] = content
			}
		default:
			text := resultText(it.Result)
			if text == "" {
				text = resultText(it.Error)
			}
			if text != "" {
				u["content"] = textBlock(text)
			}
		}
		ss.emit(u)
	}
}

// commandOutput adds a piece of a command's output and, at most every 200 ms,
// sends what there is so far. The full output follows with the completed item.
func (ss *session) commandOutput(id, delta string) {
	if delta == "" {
		return
	}
	ss.mu.Lock()
	st := ss.state(id)
	if st == nil || !st.announced {
		ss.mu.Unlock()
		return
	}
	if room := maxText - len(st.out); room > 0 {
		if len(delta) > room {
			delta = delta[:room]
		}
		st.out = append(st.out, delta...)
	}
	send := time.Since(st.outSent) >= outputEvery
	var text string
	if send {
		st.outSent = time.Now()
		text = strings.ToValidUTF8(string(st.out), "")
	}
	ss.mu.Unlock()
	if send {
		ss.emit(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": id, "status": "in_progress", "content": textBlock(text)})
	}
}

func (ss *session) patchUpdated(id string, changes json.RawMessage) {
	ss.mu.Lock()
	st := ss.items[id]
	ok := st != nil && st.announced
	ss.mu.Unlock()
	if !ok {
		return
	}
	if content, locs, _ := ss.fileChanges(changes); len(content) > 0 {
		ss.emit(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": id, "content": content, "locations": locs})
	}
}

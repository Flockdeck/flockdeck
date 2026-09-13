package transcript

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jmwri/flockdeck/internal/agent"
)

// Stream returns a live view of a Claude Code pane's conversation, tailing
// its transcript as the design's §1 describes. It answers ok=true whenever
// the pane's session id and Claude's own state directory are known, whether
// or not the transcript exists yet -- a pane that has not been prompted has
// nothing to show, not an agent the phone should fall back to the terminal
// for.
func (Claude) Stream(spec agent.Spec, sessionID string) (Stream, bool) {
	if sessionID == "" {
		return nil, false
	}
	home := ClaudeHomeFor(spec)
	if home == "" {
		return nil, false
	}
	return newClaudeStream(home, sessionID), true
}

// claudeStream tails one Claude Code transcript and keeps the Entry values it
// has translated the lines into.
type claudeStream struct {
	mu        sync.Mutex
	home      string
	sessionID string

	path   string // resolved lazily: the file may not exist at construction
	offset int64
	carry  []byte

	entries []Entry
	index   map[string]int
	detail  map[string]Detail

	autoID int
}

func newClaudeStream(home, sessionID string) *claudeStream {
	return &claudeStream{
		home:      home,
		sessionID: sessionID,
		index:     map[string]int{},
		detail:    map[string]Detail{},
	}
}

// Refresh implements Stream.
func (s *claudeStream) Refresh() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.path == "" {
		s.path = claudePath(s.home, s.sessionID)
		if s.path == "" {
			return nil
		}
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
		changed = append(changed, s.applyLine(line)...)
	}
	return changed
}

// Snapshot implements Stream.
func (s *claudeStream) Snapshot() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entries
}

// Detail implements Stream.
func (s *claudeStream) Detail(id string) (Detail, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.index[id]
	if !ok {
		return Detail{}, false
	}
	if s.entries[i].Kind == KindSubagent {
		entries, ok := s.subagentEntries(id)
		if !ok {
			return Detail{}, false
		}
		return Detail{Entries: entries}, true
	}
	d, ok := s.detail[id]
	return d, ok
}

// subagentEntries reads the mini-transcript of a subagent joined to toolUseID
// by its meta.json, per the design's §1: a separate agent-<hash>.jsonl beside
// the parent's own file, under <projectFolder>/<sessionId>/subagents/.
func (s *claudeStream) subagentEntries(toolUseID string) ([]Entry, bool) {
	if s.path == "" {
		return nil, false
	}
	dir := filepath.Join(filepath.Dir(s.path), s.sessionID, "subagents")
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, false
	}
	var jsonlPath string
	for _, f := range files {
		name := f.Name()
		if !strings.HasSuffix(name, ".meta.json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var meta struct {
			ToolUseID string `json:"toolUseId"`
		}
		if json.Unmarshal(data, &meta) != nil || meta.ToolUseID != toolUseID {
			continue
		}
		jsonlPath = filepath.Join(dir, strings.TrimSuffix(name, ".meta.json")+".jsonl")
		break
	}
	if jsonlPath == "" {
		return nil, false
	}
	data, err := os.ReadFile(jsonlPath)
	if err != nil {
		return nil, false
	}
	sub := newClaudeStream(s.home, s.sessionID)
	sub.path = jsonlPath
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimRight(line, "\r")
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		sub.applyLine(line)
	}
	return sub.entries, true
}

// ---------------------------------------------------------------------------
// Line translation
// ---------------------------------------------------------------------------

// streamLine is the part of a transcript entry the translator reads from
// every line, whatever family it belongs to.
type streamLine struct {
	Type        string `json:"type"`
	Subtype     string `json:"subtype"`
	UUID        string `json:"uuid"`
	IsSidechain bool   `json:"isSidechain"`
	Timestamp   string `json:"timestamp"`
	Message     struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	WireToolInputs map[string]json.RawMessage `json:"wireToolInputs"`
}

// rawBlock is one content block, whichever kind it turns out to be.
type rawBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
	// tool_use
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	// tool_result
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	// image, the same shape as an Anthropic Messages API image block. Only a
	// base64 source can be read here; a file reference (source.type "file")
	// names bytes on Anthropic's own servers, not on this machine, and is
	// dropped the same way a media type this build does not draw is.
	Source *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	} `json:"source,omitempty"`
}

// applyLine translates one transcript line into zero or more Entry values,
// recording them against the stream's own state (a new tool_use starts an
// entry; its tool_result later updates the same one in place) and returning
// whichever entries are new or changed as a result of this line, in order.
func (s *claudeStream) applyLine(raw []byte) []Entry {
	var line streamLine
	if json.Unmarshal(raw, &line) != nil {
		return nil
	}
	switch line.Type {
	case "system":
		if line.Subtype == "compact_boundary" {
			return []Entry{s.add(Entry{ID: s.lineID(line.UUID), Kind: KindCompaction, TS: line.Timestamp})}
		}
		return nil
	case "user":
		return s.applyUser(line)
	case "assistant":
		return s.applyAssistant(line)
	default:
		// Bookkeeping with nothing to render: mode, permission-mode,
		// atis-latch, bridge-session, file-history-snapshot, last-prompt,
		// ai-title, custom-title, and anything this build has never seen.
		return nil
	}
}

func (s *claudeStream) applyUser(line streamLine) []Entry {
	str, blocks, isStr := parseContent(line.Message.Content)
	if isStr {
		return s.promptOrNotice(line.UUID, line.Timestamp, str)
	}
	var changed []Entry
	var texts []string
	for i, b := range blocks {
		switch b.Type {
		case "tool_result":
			changed = append(changed, s.applyToolResult(b, line.Timestamp)...)
		case "text":
			if b.Text != "" {
				texts = append(texts, b.Text)
			}
		case "image":
			// A pasted screenshot: an image block sitting directly in the
			// user message, not inside a tool_result.
			if e, ok := s.addImage(fmt.Sprintf("%s:%d", s.lineID(line.UUID), i), line.Timestamp, b); ok {
				changed = append(changed, e)
			}
		}
	}
	if len(texts) > 0 {
		changed = append(changed, s.promptOrNotice(line.UUID, line.Timestamp, strings.Join(texts, "\n\n"))...)
	}
	return changed
}

// noticePrefixes mark scaffolding worth showing as a notice -- a slash
// command's own output -- rather than dropped outright like an injected
// reminder. See syntheticPromptPrefixes in claude.go, which this is a subset
// of.
var noticePrefixes = []string{"<command-name>", "<command-message>", "<local-command"}

func (s *claudeStream) promptOrNotice(id, ts, text string) []Entry {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if isSyntheticPrompt(text) {
		for _, p := range noticePrefixes {
			if strings.HasPrefix(text, p) {
				return []Entry{s.add(Entry{ID: s.lineID(id), Kind: KindNotice, TS: ts, Text: text})}
			}
		}
		return nil
	}
	return []Entry{s.add(Entry{ID: s.lineID(id), Kind: KindPrompt, TS: ts, Text: text})}
}

func (s *claudeStream) applyAssistant(line streamLine) []Entry {
	str, blocks, isStr := parseContent(line.Message.Content)
	if isStr {
		if t := strings.TrimSpace(str); t != "" {
			return []Entry{s.add(Entry{ID: s.lineID(line.UUID), Kind: KindReply, TS: line.Timestamp, Markdown: t})}
		}
		return nil
	}
	var changed []Entry
	for i, b := range blocks {
		id := fmt.Sprintf("%s:%d", s.lineID(line.UUID), i)
		switch b.Type {
		case "text":
			if t := strings.TrimSpace(b.Text); t != "" {
				changed = append(changed, s.add(Entry{ID: id, Kind: KindReply, TS: line.Timestamp, Markdown: t}))
			}
		case "thinking":
			changed = append(changed, s.add(Entry{ID: id, Kind: KindThinking, TS: line.Timestamp, Chars: len([]rune(b.Text))}))
			if b.Text != "" {
				s.detail[id] = Detail{Text: b.Text}
			}
		case "tool_use":
			input := b.Input
			if wi, ok := line.WireToolInputs[b.ID]; ok {
				input = wi
			}
			changed = append(changed, s.startTool(b.ID, b.Name, input, line.Timestamp))
		}
	}
	return changed
}

// lineID names an entry after the transcript line it came from, or a
// generated id when the line carried none -- which every line on this
// machine's transcripts does, but a fixture or another Claude Code build
// might not.
func (s *claudeStream) lineID(uuid string) string {
	if uuid != "" {
		return uuid
	}
	s.autoID++
	return fmt.Sprintf("gen-%d", s.autoID)
}

func (s *claudeStream) add(e Entry) Entry {
	s.index[e.ID] = len(s.entries)
	s.entries = append(s.entries, e)
	return e
}

// update mutates an existing entry in place, by id, and reports whether one
// was found. This is how a tool_use's entry goes from running to ok or error
// once its tool_result arrives, rather than becoming a second entry.
func (s *claudeStream) update(id string, mutate func(*Entry)) (Entry, bool) {
	i, ok := s.index[id]
	if !ok {
		return Entry{}, false
	}
	mutate(&s.entries[i])
	return s.entries[i], true
}

// ---------------------------------------------------------------------------
// Tool calls
// ---------------------------------------------------------------------------

// toolInput is every field any built-in tool's input might carry. Reading
// them all into one struct, rather than one per tool, keeps the translator
// from having to know a tool's exact shape before it can be labelled.
type toolInput struct {
	FilePath  string `json:"file_path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
	Content   string `json:"content"`
	Edits     []struct {
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	} `json:"edits"`
	Command     string `json:"command"`
	Pattern     string `json:"pattern"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
}

// subagentTools are the tool_use names that launch a subagent rather than
// running a tool directly. Claude Code has called this "Task" in older
// builds and "Agent" in this one; both are accepted.
var subagentTools = map[string]bool{"Agent": true, "Task": true}

func (s *claudeStream) startTool(id, name string, rawInput json.RawMessage, ts string) Entry {
	var in toolInput
	_ = json.Unmarshal(rawInput, &in)

	if subagentTools[name] {
		desc := in.Description
		if desc == "" {
			desc = ellipsize(in.Prompt, subagentDescCap)
		}
		return s.add(Entry{
			ID: id, Kind: KindSubagent, TS: ts,
			Description: desc, Status: StatusRunning, HasDetail: true,
		})
	}

	e := Entry{ID: id, Kind: KindTool, TS: ts, Name: name, Label: toolLabel(name, in), Status: StatusRunning}
	if diff, ok := toolDiff(name, in); ok {
		if len(diff) <= DiffCap {
			e.Diff = diff
		} else {
			e.HasDetail = true
			e.Summary = diffSummary(diff)
			s.detail[id] = Detail{Diff: diff}
		}
	}
	return s.add(e)
}

// applyToolResult folds a tool_result block into the tool call it answers,
// and, per the design's §1, into a standalone image entry for each image the
// result itself carries -- the Read tool on a picture file, above all.
func (s *claudeStream) applyToolResult(b rawBlock, ts string) []Entry {
	status := StatusOK
	if b.IsError {
		status = StatusError
	}
	text := blockText(b.Content)
	var changed []Entry
	if e, ok := s.update(b.ToolUseID, func(e *Entry) {
		e.Status = status
		switch e.Kind {
		case KindSubagent:
			e.Report = ellipsize(strings.TrimSpace(text), subagentReportCap)
		case KindTool:
			if isEditFamily(e.Name) {
				// The confirmation text Claude Code returns for an edit is
				// not worth a row of its own -- the diff already sent says
				// what changed -- except when the edit failed, where it is
				// the only thing that says why.
				if status == StatusError {
					e.Summary = oneLine(text, summaryLineCap)
				}
				return
			}
			summary, hasDetail := summarizeToolOutput(text)
			e.Summary = summary
			if hasDetail {
				e.HasDetail = true
				s.detail[e.ID] = Detail{Text: text}
			}
		}
	}); ok {
		changed = append(changed, e)
	}
	for i, img := range imageBlocksIn(b.Content) {
		id := fmt.Sprintf("%s:image:%d", b.ToolUseID, i)
		if e, ok := s.addImage(id, ts, img); ok {
			changed = append(changed, e)
		}
	}
	return changed
}

// addImage turns one image content block into an image Entry, its bytes kept
// apart in detail so the page carrying it stays small (§2's "keeping it
// small"). ok is false for anything prepareImage refuses, or a block that is
// not a readable base64 image at all -- a file reference above all, which
// names bytes on Anthropic's own servers that this machine cannot read.
func (s *claudeStream) addImage(id, ts string, b rawBlock) (Entry, bool) {
	if b.Source == nil || b.Source.Type != "base64" || b.Source.Data == "" {
		return Entry{}, false
	}
	raw, err := base64.StdEncoding.DecodeString(b.Source.Data)
	if err != nil {
		return Entry{}, false
	}
	mediaType, data, width, height, ok := prepareImage(b.Source.MediaType, raw)
	if !ok {
		return Entry{}, false
	}
	e := Entry{ID: id, Kind: KindImage, TS: ts, MediaType: mediaType, Bytes: len(data), Width: width, Height: height, HasDetail: true}
	s.detail[id] = Detail{Data: base64.StdEncoding.EncodeToString(data)}
	return s.add(e), true
}

// imageBlocksIn reads a tool_result's own content for any image blocks it
// carries alongside its text, per the design's §1: content is either a plain
// string or an array of typed blocks, the same shape parseContent reads for a
// message.
func imageBlocksIn(raw json.RawMessage) []rawBlock {
	if len(raw) == 0 {
		return nil
	}
	var blocks []rawBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var out []rawBlock
	for _, b := range blocks {
		if b.Type == "image" {
			out = append(out, b)
		}
	}
	return out
}

func isEditFamily(name string) bool {
	return name == "Edit" || name == "MultiEdit" || name == "Write"
}

const (
	subagentDescCap   = 200
	subagentReportCap = 4000
	summaryLineCap    = 120
)

// toolLabel is the short, human label a collapsed tool row shows, per the
// design's §3 examples: "Edited push.go", "Ran go test ./...".
func toolLabel(name string, in toolInput) string {
	switch name {
	case "Edit", "MultiEdit":
		return "Edited " + baseName(in.FilePath)
	case "Write":
		return "Wrote " + baseName(in.FilePath)
	case "Read":
		return "Read " + baseName(in.FilePath)
	case "Bash":
		return "Ran " + ellipsize(firstLine(in.Command), 60)
	case "Grep":
		return "Searched for " + ellipsize(in.Pattern, 40)
	case "Glob":
		return "Searched for " + ellipsize(in.Pattern, 40)
	default:
		return name
	}
}

// toolDiff builds the diff an Edit, MultiEdit or Write call carries, straight
// from its own input -- no need to wait for the result, since what is being
// written is known the moment the call is made.
func toolDiff(name string, in toolInput) (string, bool) {
	switch name {
	case "Edit":
		return unifiedDiff(in.FilePath, in.OldString, in.NewString), true
	case "MultiEdit":
		var b strings.Builder
		for i, edit := range in.Edits {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(unifiedDiff(in.FilePath, edit.OldString, edit.NewString))
		}
		return b.String(), true
	case "Write":
		return writeDiff(in.FilePath, in.Content), true
	}
	return "", false
}

// diffSummary stands in for a diff too large to send inline.
func diffSummary(diff string) string {
	add, del := 0, 0
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
		case strings.HasPrefix(line, "+"):
			add++
		case strings.HasPrefix(line, "-"):
			del++
		}
	}
	return fmt.Sprintf("+%d -%d lines changed", add, del)
}

// summarizeToolOutput turns a tool's raw output into a one-line summary, and
// says whether the full text is worth fetching on demand: over ToolOutputCap,
// per the protocol contract, or simply more than the one line a summary can
// hold.
func summarizeToolOutput(text string) (summary string, hasDetail bool) {
	trimmed := strings.TrimRight(text, "\n")
	if trimmed == "" {
		return "", false
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) == 1 && len(trimmed) <= ToolOutputCap {
		return trimmed, false
	}
	first := ellipsize(lines[0], 80)
	if len(lines) > 1 {
		return fmt.Sprintf("%s (%d lines)", first, len(lines)), true
	}
	return first, true
}

func oneLine(text string, max int) string {
	line := strings.SplitN(strings.TrimSpace(text), "\n", 2)[0]
	return ellipsize(line, max)
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

// parseContent reads a message's content, which is either a plain string or
// an array of typed blocks.
func parseContent(raw json.RawMessage) (str string, blocks []rawBlock, isString bool) {
	if len(raw) == 0 {
		return "", nil, true
	}
	if json.Unmarshal(raw, &str) == nil {
		return str, nil, true
	}
	if json.Unmarshal(raw, &blocks) == nil {
		return "", blocks, false
	}
	return "", nil, true
}

// blockText reads a tool_result block's content, which is either a plain
// string or an array of {type,text} blocks, keeping the text whole rather
// than trimmed for a list row.
func blockText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func baseName(p string) string {
	if p == "" {
		return ""
	}
	p = strings.TrimRight(p, `/\`)
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// ellipsize cuts s to at most n runes, marking the cut with an ellipsis.
func ellipsize(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

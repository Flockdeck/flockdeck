package transcript

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
)

// Follow returns a Follower over a Claude Code conversation, read from the file
// Claude Code keeps it in. Sidechain entries (a subagent's own work) are left
// out, as are the entries Claude Code writes for itself: a slash command and its
// output, an injected reminder, the note a conversation continued from a summary
// opens with.
func (Claude) Follow(spec agent.Spec, sessionID string) Follower {
	return &claudeFollower{home: ClaudeHomeFor(spec), id: sessionID, names: map[string]string{}}
}

// Cwd is the working directory a stored Claude Code conversation records.
func (Claude) Cwd(spec agent.Spec, sessionID string) string {
	path := claudePath(ClaudeHomeFor(spec), sessionID)
	if path == "" {
		return ""
	}
	return transcriptCwd(path)
}

// claudeFollower reads a Claude Code conversation a piece at a time. Only
// whole lines are read, so a line being written when the file is read is read
// the next time, and what it gives is the same however the file was cut up by
// the reads: the lines are all that matter.
type claudeFollower struct {
	home, id string
	path     string
	offset   int64
	// carry is the start of a line whose end has not been written yet, and
	// discarding says the line it is part of is too large to hold and is being
	// stepped over.
	carry      []byte
	discarding bool

	names map[string]string
	last  time.Time
	buf   []byte
}

// pollChunk is how much of the file is read at a time.
const pollChunk = 256 << 10

func (f *claudeFollower) Poll(yield func(ExportEvent) error) (ExportStats, error) {
	var stats ExportStats
	if f.path == "" {
		f.path = claudePath(f.home, f.id)
		if f.path == "" {
			return stats, ErrNoTranscript
		}
		if f.offset > 0 || !f.last.IsZero() {
			// Found again after it had been read: it may be another copy.
			f.start()
			return stats, ErrReplaced
		}
	}
	file, err := os.Open(f.path)
	if err != nil {
		// Gone, or moved to another folder: look for it again next time.
		f.path = ""
		return stats, ErrNoTranscript
	}
	defer file.Close()
	if fi, err := file.Stat(); err == nil && fi.Size() < f.offset {
		f.start()
		return stats, ErrReplaced
	}
	if f.buf == nil {
		f.buf = make([]byte, pollChunk)
	}
	buf := f.buf
	for {
		n, rerr := file.ReadAt(buf, f.offset)
		f.offset += int64(n)
		data := buf[:n]
		for len(data) > 0 {
			i := bytes.IndexByte(data, '\n')
			if i < 0 {
				f.hold(data, &stats)
				break
			}
			line := data[:i]
			data = data[i+1:]
			if f.discarding {
				f.discarding = false
				stats.Skipped++
				continue
			}
			if len(f.carry)+len(line) > maxTranscriptEntry {
				f.carry = nil
				stats.Skipped++
				continue
			}
			if len(f.carry) > 0 {
				line = append(f.carry, line...)
				f.carry = nil
			}
			if err := f.line(line, yield, &stats); err != nil {
				return stats, err
			}
		}
		if rerr != nil {
			break
		}
	}
	// What is left is the start of a line, or a whole one that has not had its
	// line break yet. A whole one is read now: the break will be an empty line.
	if len(f.carry) > 0 && json.Valid(f.carry) {
		line := f.carry
		f.carry = nil
		if err := f.line(line, yield, &stats); err != nil {
			return stats, err
		}
	}
	return stats, nil
}

// start forgets everything read, to read the file again from its beginning.
func (f *claudeFollower) start() {
	f.offset, f.carry, f.discarding = 0, nil, false
	f.names, f.last = map[string]string{}, time.Time{}
}

// hold keeps the part of a line that has no end yet.
func (f *claudeFollower) hold(part []byte, stats *ExportStats) {
	if f.discarding {
		return
	}
	f.carry = append(f.carry, part...)
	if len(f.carry) > maxTranscriptEntry {
		f.carry, f.discarding = nil, true
	}
}

// line reads one line of the file, giving its events to yield.
func (f *claudeFollower) line(raw []byte, yield func(ExportEvent) error, stats *ExportStats) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var line exportLine
	if json.Unmarshal(raw, &line) != nil {
		stats.Skipped++
		return nil
	}
	if line.IsSidechain || (line.Type != "user" && line.Type != "assistant") {
		return nil
	}
	// An entry with no usable time is given the previous one's, and one that
	// runs backwards is held at it: a transcript's lines are not always in the
	// order they happened, and the export's are.
	ts, err := time.Parse(time.RFC3339Nano, line.Timestamp)
	if err != nil || ts.Before(f.last) {
		ts = f.last
	}
	if ts.IsZero() {
		stats.Skipped++
		return nil
	}
	f.last = ts

	var events []ExportEvent
	switch line.Type {
	case "user":
		if text, ok := humanPrompt(line); ok {
			events = []ExportEvent{{Time: ts, Kind: ExportPrompt, Text: text}}
		} else {
			events = toolResults(line, ts, f.names)
		}
	case "assistant":
		events = assistantEvents(line, ts, f.names)
	}
	for _, ev := range events {
		if err := yield(ev); err != nil {
			return err
		}
	}
	return nil
}

// exportLine is the part of a transcript entry the export reads.
type exportLine struct {
	Type        string `json:"type"`
	Timestamp   string `json:"timestamp"`
	IsSidechain bool   `json:"isSidechain"`
	IsMeta      bool   `json:"isMeta"`
	Origin      struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	IsCompactSummary          bool            `json:"isCompactSummary"`
	IsVisibleInTranscriptOnly bool            `json:"isVisibleInTranscriptOnly"`
	ToolUseResult             json.RawMessage `json:"toolUseResult"`
	Message                   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// interruptedPrompt is the entry Claude Code writes when the user stops a turn.
const interruptedPrompt = "[Request interrupted by user"

// humanPrompt returns what the person typed in a user entry, and false for an
// entry that is not that: a tool's result, or something Claude Code wrote.
func humanPrompt(line exportLine) (string, bool) {
	if line.IsMeta || line.IsCompactSummary || line.IsVisibleInTranscriptOnly {
		return "", false
	}
	var text string
	var s string
	if json.Unmarshal(line.Message.Content, &s) == nil {
		text = s
	} else {
		var blocks []rawBlock
		if json.Unmarshal(line.Message.Content, &blocks) != nil {
			return "", false
		}
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				parts = append(parts, b.Text)
			}
		}
		text = strings.Join(parts, "\n")
	}
	text = stripSystemReminders(text)
	if text == "" || strings.HasPrefix(text, interruptedPrompt) {
		return "", false
	}
	if line.Origin.Kind != "human" && isSyntheticPrompt(text) {
		return "", false
	}
	return text, true
}

// assistantEvents is the tool calls and the words in an assistant entry, in the
// order it has them. Thinking is not either.
func assistantEvents(line exportLine, ts time.Time, names map[string]string) []ExportEvent {
	var blocks []rawBlock
	if json.Unmarshal(line.Message.Content, &blocks) != nil {
		var s string
		if json.Unmarshal(line.Message.Content, &s) == nil && strings.TrimSpace(s) != "" {
			return []ExportEvent{{Time: ts, Kind: ExportMessage, Text: strings.TrimSpace(s)}}
		}
		return nil
	}
	var out []ExportEvent
	var said []string
	say := func() {
		if len(said) > 0 {
			out = append(out, ExportEvent{Time: ts, Kind: ExportMessage, Text: strings.Join(said, "\n\n")})
			said = nil
		}
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				said = append(said, strings.TrimSpace(b.Text))
			}
		case "tool_use":
			say()
			names[b.ID] = b.Name
			var in any
			if json.Unmarshal(b.Input, &in) != nil {
				in = string(b.Input)
			}
			out = append(out, ExportEvent{Time: ts, Kind: ExportToolCall, Tool: b.Name, ToolUseID: b.ID, Input: in})
		}
	}
	say()
	return out
}

// toolResults is the results a user entry carries. Claude Code writes the
// tool's own structured result beside the entry, which is the fuller of the
// two; the content blocks are the text the agent was shown, and are used where
// there is no structured result, the result is an error, or the entry holds
// several results and the one structured result could be any of them.
func toolResults(line exportLine, ts time.Time, names map[string]string) []ExportEvent {
	var blocks []rawBlock
	if json.Unmarshal(line.Message.Content, &blocks) != nil {
		return nil
	}
	n := 0
	for _, b := range blocks {
		if b.Type == "tool_result" {
			n++
		}
	}
	var out []ExportEvent
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		var text string
		if n == 1 && !b.IsError {
			text = structuredResult(line.ToolUseResult)
		}
		if text == "" {
			text = flattenContent(b.Content)
		}
		ev := ExportEvent{Time: ts, Kind: ExportToolResult, Tool: names[b.ToolUseID], ToolUseID: b.ToolUseID, Output: text, IsError: b.IsError}
		if b.IsError && strings.Contains(strings.ToLower(text), "interrupted by user") {
			ev.Interrupted = true
		}
		out = append(out, ev)
	}
	return out
}

// structuredResult is a tool's own result as compact JSON, or as it is when it
// is only text.
func structuredResult(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	var v any
	if json.Unmarshal(raw, &v) != nil || enc.Encode(v) != nil {
		return ""
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// flattenContent is the text of a tool_result's content, which is a string or
// a list of blocks.
func flattenContent(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []rawBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

package transcript

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
)

// Follow returns a Follower over a Claude Code conversation, read from the file
// Claude Code keeps it in. Sidechain entries (a subagent's own work) are left
// out, as are the entries Claude Code writes for itself: a slash command and its
// output, an injected reminder, the note a conversation continued from a summary
// opens with.
func (Claude) Follow(spec agent.Spec, sessionID string) Follower {
	return &claudeFollower{home: ClaudeHomeFor(spec), id: sessionID, names: map[string]string{}, replies: map[string]replyDone{}}
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
	// replies is which replies (by message id) have had their usage and stop
	// reason given already, and title the last conversation title given.
	replies map[string]replyDone
	title   string
	last    time.Time
	// ctx is the branch, directory and agent version of the entry last read, which
	// a title, having none of its own, is given as it is given that entry's time.
	ctx struct{ gitBranch, cwd, version string }
	buf []byte
	// pending is what the line last read gave that has not been handed over.
	pending []ExportEvent
}

// replyDone says what of one model reply has been written. Claude Code stores
// a reply as one entry per content block, all with the reply's id, usage and
// stop reason, so each is written once, on the first line the reply gives.
//
// It also keeps the usage and stop reason of the first entry seen for the reply,
// whether or not that entry wrote a line, to notice when a later entry gives
// different ones; see ReplyDisagreements.
type replyDone struct {
	usage, stop bool
	firstUsage  *ExportUsage
	firstStop   string
}

// replyDisagreements counts the assistant entries whose usage, or whose stop
// reason where they have one, differ from those of the first entry seen for the
// same reply (which may be a thinking entry that writes no line), not from what
// was written. Entries with no message id are not compared.
var replyDisagreements atomic.Int64

// ReplyDisagreements is how many assistant entries read so far (by every
// follower in this process) gave a usage or a stop reason different from the
// first entry seen for the same reply (which may be a thinking entry that writes
// no line), not from the value written. Entries with no message id are not
// compared. The export writes each once, from the
// first line the reply produces, which is exact only while a reply's entries
// all repeat the same numbers (docs/recording-format.md, "Token usage and stop
// reason"). Anything above zero means Claude Code stopped doing that. Counting
// changes nothing that is written.
func ReplyDisagreements() int64 { return replyDisagreements.Load() }

// openTranscript opens a stored conversation. A variable so that a test can make
// it fail the way a locked file does.
var openTranscript = os.Open

// pollChunk is how much of the file is read at a time.
const pollChunk = 256 << 10

func (f *claudeFollower) Poll(yield func(ExportEvent) error) (ExportStats, error) {
	var stats ExportStats
	// Events a look before this one read but could not hand over go first.
	if err := f.drain(yield); err != nil {
		return stats, err
	}
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
	file, err := openTranscript(f.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Gone, or moved to another folder: look for it again next time.
			f.path = ""
			return stats, ErrNoTranscript
		}
		// There, but not to be opened for now: another program has it, or there
		// are no more handles. Nothing is lost by trying again.
		return stats, fmt.Errorf("%w: %v", ErrRead, err)
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
		data := buf[:n]
		pos := 0
		for pos < len(data) {
			i := bytes.IndexByte(data[pos:], '\n')
			if i < 0 {
				f.hold(data[pos:])
				break
			}
			line := data[pos : pos+i]
			pos += i + 1
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
				// What was read of this chunk past the line that could not be
				// handed over is read again at the next look, and what of the line
				// itself was not handed over is kept to go first.
				f.offset += int64(pos)
				f.carry = nil
				return stats, err
			}
		}
		f.offset += int64(n)
		if rerr != nil {
			if rerr != io.EOF {
				return stats, fmt.Errorf("%w: %v", ErrRead, rerr)
			}
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

// drain hands over the events kept from a look that could not.
func (f *claudeFollower) drain(yield func(ExportEvent) error) error {
	for len(f.pending) > 0 {
		if err := yield(f.pending[0]); err != nil {
			return err
		}
		f.pending = f.pending[1:]
	}
	f.pending = nil
	return nil
}

// hold keeps the part of a line that has no end yet.
func (f *claudeFollower) hold(part []byte) {
	if f.discarding {
		return
	}
	f.carry = append(f.carry, part...)
	if len(f.carry) > maxTranscriptEntry {
		f.carry, f.discarding = nil, true
	}
}

// line reads one line of the file, giving its events to yield. An event yield
// does not take, and the ones after it in the line, are kept for the next look.
func (f *claudeFollower) line(raw []byte, yield func(ExportEvent) error, stats *ExportStats) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var line exportLine
	if json.Unmarshal(raw, &line) != nil {
		stats.Skipped++
		return nil
	}
	if line.IsSidechain || (line.Type != "user" && line.Type != "assistant" && line.Type != "ai-title" && !(line.Type == "system" && line.Subtype == "compact_boundary")) {
		return nil
	}
	if line.Type == "ai-title" {
		// A title has no time of its own: it is given the previous entry's, and a
		// title before anything else is left for the next time the file repeats
		// it, which it does at every turn.
		title := strings.TrimSpace(line.AITitle)
		if title == "" || title == f.title || f.last.IsZero() {
			return nil
		}
		f.title = title
		f.pending = []ExportEvent{{Time: f.last, Kind: ExportTitle, Text: title, GitBranch: f.ctx.gitBranch, Cwd: f.ctx.cwd, AgentVersion: f.ctx.version}}
		return f.drain(yield)
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
	f.ctx.gitBranch, f.ctx.cwd, f.ctx.version = line.GitBranch, line.Cwd, line.Version

	switch line.Type {
	case "user":
		if text, ok := humanPrompt(line); ok {
			f.pending = []ExportEvent{{Time: ts, Kind: ExportPrompt, Text: text}}
		} else {
			f.pending = toolResults(line, ts, f.names)
		}
	case "assistant":
		f.pending = assistantEvents(line, ts, f.names)
		if model := turnModel(line.Message.Model); model != "" {
			for i := range f.pending {
				f.pending[i].Model = model
			}
		}
		f.replyDetails(line)
	case "system":
		cm := line.CompactMetadata
		f.pending = []ExportEvent{{Time: ts, Kind: ExportCompact, Trigger: strings.TrimSpace(cm.Trigger), TokensBefore: int(max(cm.PreTokens, 0)), TokensAfter: int(max(cm.PostTokens, 0))}}
	}
	for i := range f.pending {
		f.pending[i].GitBranch, f.pending[i].Cwd, f.pending[i].AgentVersion = line.GitBranch, line.Cwd, line.Version
	}
	return f.drain(yield)
}

// replyDetails puts the usage and stop reason an assistant entry stores on the
// first event the reply it is part of gives, and on no other. The first entry
// of a reply may give no event at all (a thinking block), so it is the first
// event that carries them, not the first entry. An entry that names no reply is
// taken to be one of its own.
func (f *claudeFollower) replyDetails(line exportLine) {
	id := line.Message.ID
	done := f.replies[id]
	if id != "" {
		var u *ExportUsage
		if m := line.Message.Usage; m != nil {
			u = &ExportUsage{m.InputTokens, m.OutputTokens, m.CacheCreationInputTokens, m.CacheReadInputTokens}
		}
		stop := strings.TrimSpace(line.Message.StopReason)
		differs := false
		if u != nil {
			if done.firstUsage == nil {
				done.firstUsage = u
			} else if *done.firstUsage != *u {
				differs = true
			}
		}
		if stop != "" {
			if done.firstStop == "" {
				done.firstStop = stop
			} else if done.firstStop != stop {
				differs = true
			}
		}
		if differs {
			replyDisagreements.Add(1)
		}
		f.replies[id] = done
	}
	if len(f.pending) == 0 {
		return
	}
	first := &f.pending[0]
	if u := line.Message.Usage; u != nil && (id == "" || !done.usage) {
		first.Usage = &ExportUsage{u.InputTokens, u.OutputTokens, u.CacheCreationInputTokens, u.CacheReadInputTokens}
		done.usage = true
	}
	if r := strings.TrimSpace(line.Message.StopReason); r != "" && (id == "" || !done.stop) {
		first.StopReason = r
		done.stop = true
	}
	if id != "" {
		f.replies[id] = done
	}
}

// start forgets everything read, to read the file again from its beginning.
func (f *claudeFollower) start() {
	f.offset, f.carry, f.discarding, f.pending = 0, nil, false, nil
	f.names, f.last = map[string]string{}, time.Time{}
	f.ctx.gitBranch, f.ctx.cwd, f.ctx.version = "", "", ""
	f.replies, f.title = map[string]replyDone{}, ""
}

// exportLine is the part of a transcript entry the export reads.
type exportLine struct {
	Type            string `json:"type"`
	Timestamp       string `json:"timestamp"`
	IsSidechain     bool   `json:"isSidechain"`
	Subtype         string `json:"subtype"`
	AITitle         string `json:"aiTitle"`
	GitBranch       string `json:"gitBranch"`
	Cwd             string `json:"cwd"`
	Version         string `json:"version"`
	CompactMetadata struct {
		Trigger    string  `json:"trigger"`
		PreTokens  float64 `json:"preTokens"`
		PostTokens float64 `json:"postTokens"`
	} `json:"compactMetadata"`
	IsMeta bool `json:"isMeta"`
	Origin struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	IsCompactSummary          bool `json:"isCompactSummary"`
	IsVisibleInTranscriptOnly bool `json:"isVisibleInTranscriptOnly"`
	Message                   struct {
		ID         string `json:"id"`
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Usage      *struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
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

// turnModel is the model an assistant entry names, or "" if it names none that
// produced the turn: Claude Code stamps entries it wrote itself (an error
// notice, a stopped turn) with the placeholder "<synthetic>", which is no model.
func turnModel(m string) string {
	m = strings.TrimSpace(m)
	if m == "" || (strings.HasPrefix(m, "<") && strings.HasSuffix(m, ">")) {
		return ""
	}
	return m
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

// toolResults is the results a user entry carries: the text the agent was shown.
// Claude Code writes the tool's own structured result beside the entry, but it
// can hold what the agent was never shown and a transcript has no use for -- a
// whole file an edit was made to, an image as base64 -- so it is not used.
func toolResults(line exportLine, ts time.Time, names map[string]string) []ExportEvent {
	var blocks []rawBlock
	if json.Unmarshal(line.Message.Content, &blocks) != nil {
		return nil
	}
	var out []ExportEvent
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		text := flattenContent(b.Content)
		ev := ExportEvent{Time: ts, Kind: ExportToolResult, Tool: names[b.ToolUseID], ToolUseID: b.ToolUseID, Output: text, IsError: b.IsError}
		if b.IsError && strings.Contains(strings.ToLower(text), "interrupted by user") {
			ev.Interrupted = true
		}
		out = append(out, ev)
	}
	return out
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

package transcript

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// replyTailBytes bounds how much of a transcript is read looking for what the
// agent last said. Transcripts grow without limit and a reply is at the end, so
// only the tail is worth opening.
const replyTailBytes = 4 << 20

// replyLine is the part of a transcript entry that carries what was said.
type replyLine struct {
	Type string `json:"type"`
	// IsSidechain marks an entry belonging to a subagent rather than to the
	// conversation the user is having.
	IsSidechain bool `json:"isSidechain"`
	// IsMeta marks an entry Claude Code injected rather than one belonging to
	// the exchange between the user and the agent.
	IsMeta  bool `json:"isMeta"`
	Message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// claudeReplies returns what an agent said in its last few turns, newest first
// and at most maxTurns of them.
//
// This is the text the agent actually wrote, read from Claude Code's own
// transcript. Reading it off the terminal instead means reading a redrawn
// interface: bullets wrapped to the pane's width and so cut mid-sentence,
// spinner frames and token counters sitting exactly where a list item would be,
// and thinking the agent was only musing with. A plan is markdown, and the
// transcript is where the markdown is.
func claudeReplies(sessionID string, maxTurns int) []string {
	if sessionID == "" || maxTurns <= 0 {
		return nil
	}
	path := claudePath(sessionID)
	if path == "" {
		return nil
	}
	sc, f, err := tailLines(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var t turns
	for sc.Scan() {
		var line replyLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		// A subagent's answer is not what this pane's agent said, and an
		// injected entry is not part of the exchange at all.
		if line.IsSidechain || line.IsMeta {
			continue
		}
		switch line.Type {
		case "assistant":
			t.say(saidText(line.Message.Content))
		case "user":
			// Tool results are recorded as user entries too, so only something
			// a person typed ends the turn it answers.
			if isPromptContent(line.Message.Content) {
				t.end()
			}
		}
	}
	return t.newest(maxTurns)
}

// tailLines opens a transcript and returns a scanner over its last
// replyTailBytes, which is where what an agent last said is. Reading only the
// tail lands mid-line, so the first line read back is a fragment and is
// dropped rather than parsed. The caller closes the file.
func tailLines(path string) (*bufio.Scanner, *os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	partial := false
	if fi, err := f.Stat(); err == nil && fi.Size() > replyTailBytes {
		if _, err := f.Seek(fi.Size()-replyTailBytes, io.SeekStart); err == nil {
			partial = true
		}
	}
	sc := newTranscriptScanner(f)
	if partial {
		sc.Scan()
	}
	return sc, f, nil
}

// turns gathers what an agent said into turns. A turn is everything said in
// answer to one prompt, so the tool calls in the middle of a long piece of
// work do not cut it into pieces: a fan-out reads a plan out of this, and half
// a plan is not one.
type turns struct {
	all, said []string
}

// say adds to the turn under way.
func (t *turns) say(text string) {
	if text != "" {
		t.said = append(t.said, text)
	}
}

// end closes the turn under way, if anything was said in it.
func (t *turns) end() {
	if len(t.said) > 0 {
		t.all = append(t.all, strings.Join(t.said, "\n\n"))
		t.said = nil
	}
}

// newest returns at most n turns, most recent first.
func (t *turns) newest(n int) []string {
	t.end()
	var out []string
	for i := len(t.all) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, t.all[i])
	}
	return out
}

// saidText joins the text an assistant entry contains.
//
// Only text blocks count. Thinking is the agent working something out, not what
// it decided, and a plan drawn from it reads like overheard muttering; tool
// calls are not words at all.
func saidText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
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
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, strings.TrimSpace(b.Text))
		}
	}
	return strings.Join(parts, "\n\n")
}

// isPromptContent reports whether a user entry is something a person typed
// rather than the result of a tool the agent ran.
//
// Claude Code writes user entries of its own too -- a slash command, the
// output of one, an injected reminder -- and they look exactly like a typed
// prompt. Treating one as the start of a new turn cuts the agent's answer in
// half, so the fan-out dialog shows the tail of a plan and not the plan.
func isPromptContent(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s) != "" && !isSyntheticPrompt(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" && !isSyntheticPrompt(b.Text) {
			return true
		}
	}
	return false
}

// claudePath returns the file Claude Code keeps a session's conversation in,
// or "" when there is none.
//
// Transcripts live under a per-directory folder whose name is derived from the
// working directory, but that mangling is Claude's business and could change.
// Session ids are UUIDs, so searching every project folder for the file is both
// simpler and more robust than reproducing the naming.
//
// The folders are listed rather than globbed. A glob reads the whole path as a
// pattern, so a home directory with a bracket in its name -- "C:\Users\[dev]",
// or a CLAUDE_CONFIG_DIR pointed anywhere at all -- matched nothing, and every
// restored pane started an empty conversation instead of resuming its own.
func claudePath(sessionID string) string {
	if sessionID == "" || strings.ContainsAny(sessionID, `/\`) {
		return ""
	}
	home := claudeHome()
	if home == "" {
		return ""
	}
	projects := filepath.Join(home, "projects")
	folders, err := os.ReadDir(projects)
	if err != nil {
		return ""
	}
	// Resuming a conversation from a different working directory files it
	// under that directory's folder and leaves the earlier copy behind, so an
	// id can match in more than one place. The most recently written copy is
	// the live one; taking whichever sorts first would read a transcript that
	// stopped growing turns ago.
	//
	// Unless it is empty. A session interrupted before it recorded anything
	// leaves an empty file, and the newest copy being one of those is not the
	// conversation ending: preferring it told a restored pane there was
	// nothing to resume while an older copy held all of it.
	newest, newestMod, newestEmpty := "", time.Time{}, true
	for _, folder := range folders {
		m := filepath.Join(projects, folder.Name(), sessionID+".jsonl")
		fi, err := os.Stat(m)
		if err != nil || fi.IsDir() {
			continue
		}
		empty := fi.Size() == 0
		if newest == "" || (newestEmpty && !empty) || (empty == newestEmpty && fi.ModTime().After(newestMod)) {
			newest, newestMod, newestEmpty = m, fi.ModTime(), empty
		}
	}
	return newest
}

package transcript

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jmwri/perch/internal/agent"
	"github.com/jmwri/perch/internal/store"
)

// Chat reads the record Perch's own chat client keeps: one JSONL file per
// session under the state directory, one object per entry.
//
// It is the same reader for every API agent, because they are all the same
// program talking to different endpoints, and it is a great deal simpler than
// Claude Code's because Perch writes it: the entries are small, they are all
// in one folder, and there is no derived folder name to reproduce.
type Chat struct{}

// chatLine is a chat transcript entry.
//
// Cwd is what makes the flat folder of chats sortable into projects. Every
// other reader here finds a conversation by where it was stored; these are all
// stored together, so the directory a chat ran in is written into it, and a
// chat that records none is not offered anywhere -- offering it in every
// project would be worse than offering it in none.
type chatLine struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Cwd  string `json:"cwd"`
}

func (Chat) Path(_ agent.Spec, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	dir, err := chatsDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// Replies returns what the model said in its last few turns, newest first.
//
// A turn is everything said in answer to one prompt, so the tool calls in the
// middle of a long piece of work do not cut it into pieces -- which is the
// same rule the Claude reader follows, and for the same reason: a fan-out
// reads a plan out of this, and half a plan is not one.
func (c Chat) Replies(spec agent.Spec, sessionID string, n int) []string {
	if n <= 0 {
		return nil
	}
	path := c.Path(spec, sessionID)
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	// Reading only the tail lands mid-line, so the first line read back is a
	// fragment and is dropped rather than parsed.
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

	var turns, said []string
	endTurn := func() {
		if len(said) > 0 {
			turns = append(turns, strings.Join(said, "\n\n"))
			said = nil
		}
	}
	for sc.Scan() {
		var line chatLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		switch line.Type {
		case "assistant":
			if text := strings.TrimSpace(line.Text); text != "" {
				said = append(said, text)
			}
		case "user":
			endTurn()
		}
	}
	endTurn()

	out := make([]string, 0, n)
	for i := len(turns) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, turns[i])
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Conversations lists the chats that ran in a working directory, most recently
// used first.
func (Chat) Conversations(spec agent.Spec, cwd string) ([]Conversation, error) {
	dir, err := chatsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Never having run an API agent is not a failure; being unable to read
		// what is there is.
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read chats in %s: %w", dir, err)
	}

	var out []Conversation
	for _, e := range entries {
		id := transcriptID(e.Name())
		if e.IsDir() || id == "" {
			continue
		}
		info, err := e.Info()
		// An empty file is a session that was opened and never said anything.
		// There is nothing to resume and nothing to put in the row but a date.
		if err != nil || info.Size() == 0 {
			continue
		}
		summary, recorded, entries := describeChat(filepath.Join(dir, e.Name()))
		if recorded == "" || !sameDir(recorded, cwd) {
			continue
		}
		if summary == "" {
			summary = NoPrompt
		}
		out = append(out, Conversation{
			ID:       id,
			Agent:    spec.ID,
			Cwd:      cwd,
			Summary:  summary,
			Modified: info.ModTime(),
			Messages: entries,
			Size:     info.Size(),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if !out[i].Modified.Equal(out[j].Modified) {
			return out[i].Modified.After(out[j].Modified)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// describeChat reads what a chat says about itself: the opening prompt, the
// directory it ran in, and how many entries it holds.
//
// The first two are in the opening entries and the third is the whole file, so
// they are found the way Claude's are -- parse the head, count the line breaks
// of the rest -- except that there is no cache behind it. A chat is Perch's
// own writing rather than a conversation with every tool result pasted into
// it, so the folder is small enough to read on each listing.
func describeChat(path string) (summary, cwd string, entries int) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", 0
	}
	defer f.Close()

	counted := &countingReader{r: f, last: '\n'}
	lines := newTranscriptReader(counted)
	for i := 0; i < summaryScanLimit && (summary == "" || cwd == ""); i++ {
		raw, ok := lines.next()
		if !ok {
			break
		}
		var line chatLine
		if json.Unmarshal(raw, &line) != nil {
			continue
		}
		if cwd == "" {
			cwd = line.Cwd
		}
		if summary == "" && line.Type == "user" {
			summary = firstPrompt(line.Text)
		}
	}
	lines.release()
	drain(counted)

	entries = counted.newlines
	if counted.last != '\n' {
		// A chat being written to right now usually ends mid-entry, and what
		// is on that last line is still an entry.
		entries++
	}
	return summary, cwd, entries
}

// chatsDir returns the folder Perch's chat client keeps its transcripts in.
func chatsDir() (string, error) {
	dir, err := store.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "chats"), nil
}

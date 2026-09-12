package transcript

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/store"
)

// Chat reads the record Flockdeck's own chat client keeps: one JSONL file per
// session under the state directory, one object per entry.
//
// It is the same reader for every API agent, because they are all the same
// program talking to different endpoints, and it is a great deal simpler than
// Claude Code's because Flockdeck writes it: the entries are small, they are all
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
	// Model is the model an answer came from, and Agent the catalog entry the
	// chat was started as where the chat client records it. They are what puts
	// a chat back under the agent that held it, since every API agent reads
	// the same folder.
	Model string `json:"model"`
	Agent string `json:"agent"`
}

// chatRow is a chat as the folder describes it, with what it recorded about
// the agent that held it.
type chatRow struct {
	Conversation
	model, agent string
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

// Replies returns what the model said in its last few turns, newest first,
// turn by turn the way the Claude reader counts them.
func (c Chat) Replies(spec agent.Spec, sessionID string, n int) []string {
	if n <= 0 {
		return nil
	}
	path := c.Path(spec, sessionID)
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
		var line chatLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		switch line.Type {
		case "assistant":
			t.say(strings.TrimSpace(line.Text))
		case "user":
			t.end()
		}
	}
	return t.newest(n)
}

// Conversations lists the chats that ran in a working directory, most recently
// used first, each labelled as spec's. All asks once for every API agent and
// works out which of them held each chat instead.
func (Chat) Conversations(spec agent.Spec, cwd string) ([]Conversation, error) {
	rows, err := chatRows(cwd)
	if len(rows) == 0 {
		return nil, err
	}
	out := make([]Conversation, len(rows))
	for i, r := range rows {
		out[i] = r.Conversation
		out[i].Agent = spec.ID
	}
	return out, err
}

// chatRows describes the chats that ran in a working directory, most recently
// used first.
func chatRows(cwd string) ([]chatRow, error) {
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

	var out []chatRow
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
		f := describeChat(filepath.Join(dir, e.Name()))
		if f.cwd == "" || !sameDir(f.cwd, cwd) {
			continue
		}
		if f.summary == "" {
			f.summary = NoPrompt
		}
		out = append(out, chatRow{
			Conversation: Conversation{
				ID:       id,
				Cwd:      cwd,
				Summary:  f.summary,
				Modified: info.ModTime(),
				Messages: f.entries,
				Size:     info.Size(),
			},
			model: f.model,
			agent: f.agent,
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

// chatFacts is what a chat says about itself.
type chatFacts struct {
	summary, cwd, model, agent string
	entries                    int
}

// describeChat reads what a chat says about itself: the opening prompt, the
// directory it ran in, the model that answered and the agent it was started
// as where it says, and how many entries it holds.
//
// All but the count are in the opening entries and the count is the whole
// file, so they are found the way Claude's are -- parse the head, count the
// line breaks of the rest -- except that there is no cache behind it. A chat is
// Flockdeck's own writing rather than a conversation with every tool result
// pasted into it, so the folder is small enough to read on each listing.
func describeChat(path string) chatFacts {
	var c chatFacts
	f, err := os.Open(path)
	if err != nil {
		return c
	}
	defer f.Close()

	counted := &countingReader{r: f, last: '\n'}
	lines := newTranscriptReader(counted)
	// The agent is not waited for: a chat that names one names it from the
	// start, and one that does not would otherwise be read to the scan limit.
	for i := 0; i < summaryScanLimit && (c.summary == "" || c.cwd == "" || c.model == ""); i++ {
		raw, ok := lines.next()
		if !ok {
			break
		}
		var line chatLine
		if json.Unmarshal(raw, &line) != nil {
			continue
		}
		if c.cwd == "" {
			c.cwd = line.Cwd
		}
		if c.model == "" {
			c.model = line.Model
		}
		if c.agent == "" {
			c.agent = line.Agent
		}
		if c.summary == "" && line.Type == "user" {
			c.summary = firstPrompt(line.Text)
		}
	}
	lines.release()
	drain(counted)

	c.entries = counted.newlines
	if counted.last != '\n' {
		// A chat being written to right now usually ends mid-entry, and what
		// is on that last line is still an entry.
		c.entries++
	}
	return c
}

// chatsDir returns the folder Flockdeck's chat client keeps its transcripts in.
func chatsDir() (string, error) {
	dir, err := store.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "chats"), nil
}

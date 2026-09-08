package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Conversation is a stored Claude Code conversation that can be resumed.
type Conversation struct {
	// ID is the session id, which is also the transcript's file name and what
	// `claude --resume` takes.
	ID       string
	Cwd      string
	Summary  string
	Modified time.Time
	Messages int
	Size     int64
}

// summaryScanLimit bounds how much of a transcript is read looking for the
// opening prompt. Transcripts can be very large and the first user message is
// near the top.
const summaryScanLimit = 200

// transcriptLine is the part of a transcript entry that identifies the first
// real prompt.
type transcriptLine struct {
	Type    string `json:"type"`
	Cwd     string `json:"cwd"`
	Message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// Conversations lists the stored conversations for a working directory, most
// recently used first.
//
// Claude Code keeps one transcript per session under a per-directory folder.
// The folder name is derived from the path, but that mangling is Claude's
// business, so the derived name is only a fast path: if it does not exist the
// folders are searched for one whose transcripts record this directory.
func Conversations(cwd string) ([]Conversation, error) {
	home := claudeHome()
	if home == "" {
		return nil, nil
	}
	projects := filepath.Join(home, "projects")

	dir := filepath.Join(projects, projectSlug(cwd))
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		found, err := findProjectDir(projects, cwd)
		if err != nil || found == "" {
			return nil, err
		}
		dir = found
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}

	var out []Conversation
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		c := Conversation{
			ID:       strings.TrimSuffix(e.Name(), ".jsonl"),
			Cwd:      cwd,
			Modified: info.ModTime(),
			Size:     info.Size(),
		}
		c.Summary, c.Messages = describeTranscript(filepath.Join(dir, e.Name()))
		if c.Summary == "" {
			c.Summary = "(no prompt recorded)"
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}

// projectSlug reproduces the folder name Claude Code derives from a path.
func projectSlug(path string) string {
	var b strings.Builder
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// findProjectDir looks for the folder whose transcripts belong to cwd.
func findProjectDir(projects, cwd string) (string, error) {
	entries, err := os.ReadDir(projects)
	if err != nil {
		return "", nil
	}
	want := strings.ToLower(filepath.Clean(cwd))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(projects, e.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			if got := transcriptCwd(filepath.Join(dir, f.Name())); got != "" {
				if strings.ToLower(filepath.Clean(got)) == want {
					return dir, nil
				}
			}
			break // one transcript is enough to identify the folder
		}
	}
	return "", nil
}

// transcriptCwd reads the working directory a transcript records.
func transcriptCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := newTranscriptScanner(f)
	for i := 0; sc.Scan() && i < 10; i++ {
		var line transcriptLine
		if json.Unmarshal(sc.Bytes(), &line) == nil && line.Cwd != "" {
			return line.Cwd
		}
	}
	return ""
}

// describeTranscript returns the opening prompt and how many entries the
// transcript holds.
func describeTranscript(path string) (string, int) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0
	}
	defer f.Close()

	sc := newTranscriptScanner(f)
	summary := ""
	count := 0
	for sc.Scan() {
		count++
		// Every line is counted, but only the first few are parsed: the
		// opening prompt is near the top, and stopping the count there would
		// report a long conversation as a short one.
		if summary != "" || count > summaryScanLimit {
			continue
		}
		var line transcriptLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if line.Type != "user" || line.Message.Role != "user" {
			continue
		}
		if text := contentText(line.Message.Content); text != "" {
			summary = text
		}
	}
	return summary, count
}

// contentText pulls readable text out of a message's content, which is either
// a plain string or a list of typed blocks.
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return firstPrompt(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	for _, b := range blocks {
		if b.Type == "text" {
			if t := firstPrompt(b.Text); t != "" {
				return t
			}
		}
	}
	return ""
}

// firstPrompt tidies a prompt for display and skips the synthetic entries
// Claude Code records alongside real ones.
func firstPrompt(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Slash commands, hook output and system reminders are not what someone
	// scanning the list is looking for.
	for _, skip := range []string{"<command-name>", "<local-command", "<system-reminder>", "Caveat:"} {
		if strings.HasPrefix(s, skip) {
			return ""
		}
	}
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > 160 {
		s = string([]rune(s)[:160]) + "…"
	}
	return s
}

// newTranscriptScanner returns a scanner able to cope with the long lines a
// transcript contains.
func newTranscriptScanner(f *os.File) *bufio.Scanner {
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	return sc
}

package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

	// The derived name is only a guess, and a lossy one: every character that
	// is not a letter or a digit becomes a dash, so "my-app" and "my_app"
	// derive the same folder. Take it only when its transcripts agree they
	// were recorded here, or say nothing either way.
	dir := filepath.Join(projects, projectSlug(cwd))
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() || !folderCouldBe(dir, cwd) {
		found, err := findProjectDir(projects, cwd)
		if err != nil || found == "" {
			return nil, err
		}
		dir = found
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		// Never having used Claude Code here is not a failure; being unable to
		// read what is there is, and reporting it as an empty list leaves
		// somebody looking for conversations they know they had.
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read conversations in %s: %w", dir, err)
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
		// A session interrupted before it recorded anything leaves an empty
		// file behind. Offering it as a conversation is offering a resume
		// that Claude Code refuses, on a row that can say nothing about
		// itself but its date.
		if info.Size() == 0 {
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
	// Most recently used first, with the id breaking a tie so that two
	// conversations started together do not swap places between refreshes.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Modified.Equal(out[j].Modified) {
			return out[i].Modified.After(out[j].Modified)
		}
		return out[i].ID < out[j].ID
	})
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

// cwdProbeLimit bounds how many transcripts in one folder are opened looking
// for the directory it belongs to, so an unrelated folder full of empty
// transcripts cannot make listing history slow.
const cwdProbeLimit = 5

// findProjectDir looks for the folder whose transcripts belong to cwd.
func findProjectDir(projects, cwd string) (string, error) {
	entries, err := os.ReadDir(projects)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", projects, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(projects, e.Name())
		if got := folderCwd(dir); got != "" && sameDir(got, cwd) {
			return dir, nil
		}
	}
	return "", nil
}

// folderCwd returns the working directory a project folder's transcripts
// record, or "" when none of the ones it looks at says.
//
// One transcript that names a directory identifies the whole folder, but the
// first file need not be that transcript: a session that was opened and
// abandoned leaves one with nothing in it. Look past those, up to a handful,
// rather than writing the folder off.
func folderCwd(dir string) string {
	files, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	tried := 0
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
			continue
		}
		if got := transcriptCwd(filepath.Join(dir, f.Name())); got != "" {
			return got
		}
		if tried++; tried >= cwdProbeLimit {
			break
		}
	}
	return ""
}

// folderCouldBe reports whether a project folder may hold cwd's conversations:
// either its transcripts say so, or they say nothing at all, which is what a
// folder holding only abandoned sessions looks like.
func folderCouldBe(dir, cwd string) bool {
	got := folderCwd(dir)
	return got == "" || sameDir(got, cwd)
}

// sameDir compares two recorded working directories.
func sameDir(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
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
//
// The two are found separately because they cost very different things. The
// prompt is near the top, so parsing stops after the opening entries. The
// count has to reach the end of the file, but counting line breaks is a scan
// rather than a parse and stays quick on the tens of megabytes a long
// conversation runs to -- and, unlike a parse, is not stopped by a single
// entry too large to hold in memory.
func describeTranscript(path string) (string, int) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0
	}
	defer f.Close()

	summary := openingPrompt(f)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return summary, 0
	}
	return summary, countEntries(f)
}

// openingPrompt reads the first thing the user asked, looking only at the
// opening entries of the transcript.
func openingPrompt(r io.Reader) string {
	sc := newTranscriptScanner(r)
	for i := 0; i < summaryScanLimit && sc.Scan(); i++ {
		var line transcriptLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if line.Type != "user" || line.Message.Role != "user" {
			continue
		}
		if text := contentText(line.Message.Content); text != "" {
			return text
		}
	}
	return ""
}

// countEntries counts the entries in a transcript, which is one per line.
func countEntries(r io.Reader) int {
	buf := make([]byte, 256<<10)
	n := 0
	// A file that ends without a line break still has an entry on that last
	// line: a transcript being written to at this moment usually does.
	last := byte('\n')
	for {
		read, err := r.Read(buf)
		if read > 0 {
			n += bytes.Count(buf[:read], []byte{'\n'})
			last = buf[read-1]
		}
		if err != nil {
			break
		}
	}
	if last != '\n' {
		n++
	}
	return n
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

// syntheticPromptPrefixes open the user entries Claude Code writes itself: a
// slash command and its output, an injected reminder, the note that opens a
// resumed conversation. They are recorded exactly like something a person
// typed, and telling them apart is only possible by how they start.
var syntheticPromptPrefixes = []string{
	"<command-name>",
	"<command-message>",
	"<local-command",
	"<system-reminder>",
	"<user-prompt-submit-hook>",
	"Caveat:",
}

// isSyntheticPrompt reports whether a user entry was written by Claude Code
// rather than typed by the user.
func isSyntheticPrompt(s string) bool {
	s = strings.TrimSpace(s)
	for _, skip := range syntheticPromptPrefixes {
		if strings.HasPrefix(s, skip) {
			return true
		}
	}
	return false
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
	if isSyntheticPrompt(s) {
		return ""
	}
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > 160 {
		s = string([]rune(s)[:160]) + "…"
	}
	return s
}

// newTranscriptScanner returns a scanner able to cope with the long lines a
// transcript contains.
func newTranscriptScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	return sc
}

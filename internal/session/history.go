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
	"sync"
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

// transcriptFacts is what one read of a transcript found, together with what
// the file looked like at the time, so a later listing can tell whether the
// answer still holds.
type transcriptFacts struct {
	modTime time.Time
	size    int64
	summary string
	// cwd is the working directory the transcript records, which says whose
	// conversation it is when a folder is shared between two directories.
	cwd string
	// newlines is the raw count of line breaks, kept apart from the entry
	// count so that an appended tail can be added to it.
	newlines int
	// partial records that the file ended mid-entry, which is what a
	// transcript being written to right now looks like.
	partial bool
}

// entries is how many entries the transcript holds.
func (f transcriptFacts) entries() int {
	if f.partial {
		return f.newlines + 1
	}
	return f.newlines
}

// transcriptCache remembers what each transcript in a project folder said.
//
// Listing history is dominated by reading the files: every refresh of the
// panel would otherwise parse the opening entries of every transcript and
// then read all of each one to the end, and a project in daily use
// accumulates hundreds of them running to tens of megabytes. Almost none of
// that changes between one refresh and the next.
//
// The cache is per folder and replaced whole on every listing, so transcripts
// deleted from a folder drop out of it rather than accumulating.
var transcriptCache = struct {
	sync.Mutex
	dirs map[string]map[string]transcriptFacts
}{dirs: make(map[string]map[string]transcriptFacts)}

// cachedFolderLimit bounds how many project folders are remembered at once.
// Someone who opens a great many projects in one sitting should not grow the
// cache without end; starting over costs one slow listing.
const cachedFolderLimit = 64

// cachedFacts returns what the last listing of a folder found. The map is
// never written to once published, so reading it needs no lock of its own.
func cachedFacts(dir string) map[string]transcriptFacts {
	transcriptCache.Lock()
	defer transcriptCache.Unlock()
	return transcriptCache.dirs[dir]
}

// rememberFacts publishes what this listing of a folder found.
func rememberFacts(dir string, facts map[string]transcriptFacts) {
	transcriptCache.Lock()
	defer transcriptCache.Unlock()
	if len(transcriptCache.dirs) >= cachedFolderLimit {
		transcriptCache.dirs = make(map[string]map[string]transcriptFacts, 1)
	}
	transcriptCache.dirs[dir] = facts
}

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
// Claude Code keeps one transcript per session under a per-directory folder
// whose name it derives from the path, and the derivation is reproduced here,
// so that folder is where this directory's transcripts are. It is a lossy
// derivation -- every character that is not a letter or a digit becomes a
// dash, so "my-app" and "my_app" land in the same folder -- so which
// transcripts in it are this directory's is decided per transcript, by what
// each one records. If nothing in there is ours the folders are searched for
// one whose transcripts record this directory, which is where a folder Claude
// named differently turns up.
func Conversations(cwd string) ([]Conversation, error) {
	home := claudeHome()
	if home == "" {
		return nil, nil
	}
	projects := filepath.Join(home, "projects")

	dir := filepath.Join(projects, projectSlug(cwd))
	entries, derr := os.ReadDir(dir)
	out := conversationsIn(dir, entries, cwd)
	if len(out) == 0 {
		found, err := findProjectDir(projects, cwd)
		if err != nil {
			return nil, err
		}
		if found == "" {
			// Never having used Claude Code here is not a failure; being
			// unable to read what is there is, and reporting that as an empty
			// list leaves somebody looking for conversations they know they
			// had.
			if derr != nil && !os.IsNotExist(derr) {
				return nil, fmt.Errorf("read conversations in %s: %w", dir, derr)
			}
			return nil, nil
		}
		entries, err := os.ReadDir(found)
		if err != nil {
			return nil, fmt.Errorf("read conversations in %s: %w", found, err)
		}
		out = conversationsIn(found, entries, cwd)
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

// describeReaders bounds how many transcripts are read at once. Reading a
// folder of them is spent waiting on the disk far more than working, so
// several at a time finish sooner than one after another; many more than
// this only queue up on the same disk.
const describeReaders = 8

// conversationsIn describes the transcripts in one project folder that belong
// to cwd, in whatever order the folder was read.
func conversationsIn(dir string, entries []os.DirEntry, cwd string) []Conversation {
	files := make([]os.FileInfo, 0, len(entries))
	names := make([]string, 0, len(entries))
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
		names = append(names, e.Name())
		files = append(files, info)
	}

	cached := cachedFacts(dir)
	facts := make([]transcriptFacts, len(names))
	readTranscripts(func(i int) {
		facts[i] = describeTranscript(filepath.Join(dir, names[i]), files[i], cached[names[i]])
	}, len(names))

	fresh := make(map[string]transcriptFacts, len(names))
	out := make([]Conversation, 0, len(names))
	for i, name := range names {
		fresh[name] = facts[i]

		// A transcript that names a different directory is a neighbour's,
		// sharing this folder because the two paths derive the same name.
		// Offering it here would resume somebody else's work in this project;
		// one that names nowhere is an abandoned session and belongs to
		// whoever asks.
		if facts[i].cwd != "" && !sameDir(facts[i].cwd, cwd) {
			continue
		}

		c := Conversation{
			ID:       strings.TrimSuffix(name, ".jsonl"),
			Cwd:      cwd,
			Modified: files[i].ModTime(),
			Size:     files[i].Size(),
			Summary:  facts[i].summary,
			Messages: facts[i].entries(),
		}
		if c.Summary == "" {
			c.Summary = "(no prompt recorded)"
		}
		out = append(out, c)
	}
	rememberFacts(dir, fresh)
	if len(out) == 0 {
		return nil
	}
	return out
}

// readTranscripts runs read over each of n transcripts, a few at a time.
func readTranscripts(read func(i int), n int) {
	if n <= 1 {
		for i := 0; i < n; i++ {
			read(i)
		}
		return
	}
	next := make(chan int, n)
	for i := 0; i < n; i++ {
		next <- i
	}
	close(next)

	readers := describeReaders
	if readers > n {
		readers = n
	}
	var wg sync.WaitGroup
	wg.Add(readers)
	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			for i := range next {
				read(i)
			}
		}()
	}
	wg.Wait()
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
		if folderMentions(dir, cwd) {
			return dir, nil
		}
	}
	return "", nil
}

// folderMentions reports whether any of the first few transcripts in a
// project folder records cwd as the directory it ran in.
//
// One transcript that names the directory identifies the folder, but the
// first file need not be that transcript: a session that was opened and
// abandoned leaves one with nothing in it, and a folder can be shared with a
// neighbouring directory whose path derives the same name. Look past those,
// up to a handful, rather than writing the folder off on the first answer.
func folderMentions(dir, cwd string) bool {
	files, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	tried := 0
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
			continue
		}
		if got := transcriptCwd(filepath.Join(dir, f.Name())); got != "" && sameDir(got, cwd) {
			return true
		}
		if tried++; tried >= cwdProbeLimit {
			break
		}
	}
	return false
}

// sameDir compares two recorded working directories.
func sameDir(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// cwdScanLimit bounds how far into a transcript the working directory it
// records is looked for.
//
// A transcript does not open with the conversation. It opens with Claude
// Code's own bookkeeping -- the mode, the permission mode, a bridge record, a
// compaction summary -- and none of those entries names a directory. Across
// the transcripts on this machine the first entry that does was as far down
// as the twenty-sixth, so the ten this used to look at wrote off one
// transcript in twenty as saying nothing about where it ran.
const cwdScanLimit = 60

// transcriptCwd reads the working directory a transcript records.
func transcriptCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	lines := newTranscriptReader(f)
	for i := 0; i < cwdScanLimit; i++ {
		raw, ok := lines.next()
		if !ok {
			break
		}
		var line transcriptLine
		if json.Unmarshal(raw, &line) == nil && line.Cwd != "" {
			return line.Cwd
		}
	}
	return ""
}

// describeTranscript returns what a transcript holds: the opening prompt, and
// how many entries there are.
//
// The two are found separately because they cost very different things. The
// prompt is near the top, so parsing stops after the opening entries. The
// count has to reach the end of the file, but counting line breaks is a scan
// rather than a parse and stays quick on the tens of megabytes a long
// conversation runs to -- and, unlike a parse, is not stopped by a single
// entry too large to hold in memory.
//
// prev is what the last listing found, and lets most of even that be skipped.
// An untouched file is not opened at all. A transcript is only ever appended
// to, so one that has merely grown still opens with the same prompt and still
// holds the line breaks already counted: only the new tail is read. Anything
// else -- a first look, a file that shrank, one replaced at the same size --
// is read whole.
func describeTranscript(path string, info os.FileInfo, prev transcriptFacts) transcriptFacts {
	now := transcriptFacts{modTime: info.ModTime(), size: info.Size()}
	if prev.size == now.size && prev.modTime.Equal(now.modTime) {
		return prev
	}

	f, err := os.Open(path)
	if err != nil {
		return now
	}
	defer f.Close()

	if grown := prev.size > 0 && now.size > prev.size; grown {
		if _, err := f.Seek(prev.size, io.SeekStart); err != nil {
			return now
		}
		now.summary, now.cwd, now.newlines = prev.summary, prev.cwd, prev.newlines
	} else {
		now.summary, now.cwd = openingPrompt(f)
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return now
		}
	}

	n, partial := countNewlines(f)
	now.newlines += n
	now.partial = partial
	return now
}

// openingPrompt reads the first thing the user asked and the working
// directory the transcript records, looking only at the opening entries.
//
// The two come back together because they are found in the same walk: every
// entry carries the directory, so the one holding the prompt almost always
// carries it too, and reading it costs nothing extra.
func openingPrompt(r io.Reader) (prompt, cwd string) {
	lines := newTranscriptReader(r)
	for i := 0; i < summaryScanLimit; i++ {
		raw, ok := lines.next()
		if !ok {
			break
		}
		var line transcriptLine
		if json.Unmarshal(raw, &line) != nil {
			continue
		}
		if cwd == "" {
			cwd = line.Cwd
		}
		if line.Type != "user" || line.Message.Role != "user" {
			continue
		}
		if text := contentText(line.Message.Content); text != "" {
			return text, cwd
		}
	}
	return "", cwd
}

// countNewlines counts the line breaks in what is left of a reader, and
// reports whether the last byte it read was not one. A transcript has one
// entry per line, so a file that ends without a line break still has an entry
// on that last line: a transcript being written to at this moment usually
// does.
func countNewlines(r io.Reader) (int, bool) {
	buf := make([]byte, 256<<10)
	n, partial := 0, false
	for {
		read, err := r.Read(buf)
		if read > 0 {
			n += bytes.Count(buf[:read], []byte{'\n'})
			partial = buf[read-1] != '\n'
		}
		if err != nil {
			break
		}
	}
	return n, partial
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
// transcript contains. It is what the reply reader walks a transcript with,
// which reads a transcript it knows the shape of rather than one it is
// hunting through.
func newTranscriptScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	return sc
}

// maxTranscriptEntry bounds how large a single entry may be before it is
// stepped over rather than held in memory. Listing history must not be able
// to allocate a transcript's worth of memory per file.
const maxTranscriptEntry = 8 << 20

// transcriptReader walks a transcript entry by entry.
//
// A transcript has one entry per line and the lines are long: a tool result
// or a pasted screenshot arrives as a single line of many megabytes. An entry
// too large to hold has to be stepped over rather than end the walk, because
// what is being looked for is often the entry after it -- a conversation that
// opens by pasting an image still has a prompt underneath, and a scanner that
// stops at the paste reports it as having none.
type transcriptReader struct {
	br  *bufio.Reader
	buf []byte
}

func newTranscriptReader(r io.Reader) *transcriptReader {
	return &transcriptReader{br: bufio.NewReaderSize(r, 64<<10)}
}

// next returns the next entry, or ok=false once there are no more. An entry
// too large to hold comes back as no bytes at all: there was something here,
// but it is not being kept. The bytes are only valid until the next call.
func (t *transcriptReader) next() ([]byte, bool) {
	t.buf = t.buf[:0]
	oversized := false
	for {
		chunk, err := t.br.ReadSlice('\n')
		if len(t.buf)+len(chunk) > maxTranscriptEntry {
			// Keep reading to the end of the entry, but stop holding on to it.
			oversized = true
			t.buf = t.buf[:0]
		}
		if !oversized {
			t.buf = append(t.buf, chunk...)
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			// The end of the file, or a file that cannot be read any further.
			// A last line with no break after it is still an entry.
			if !oversized && len(t.buf) == 0 {
				return nil, false
			}
		}
		break
	}
	if oversized {
		return nil, true
	}
	return t.buf, true
}

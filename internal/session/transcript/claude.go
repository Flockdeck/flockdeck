package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
)

// Claude reads the transcripts Claude Code writes: one JSONL file per session,
// under a folder per working directory, below Claude Code's own state
// directory. None of that comes from the Spec, because it is Claude Code's
// arrangement rather than Flockdeck's, and Flockdeck only reproduces enough of it to
// find the files.
type Claude struct{}

func (Claude) Path(_ agent.Spec, sessionID string) string {
	return claudePath(sessionID)
}

func (Claude) Replies(_ agent.Spec, sessionID string, n int) []string {
	return claudeReplies(sessionID, n)
}

func (Claude) Conversations(spec agent.Spec, cwd string) ([]Conversation, error) {
	found, err := claudeConversations(cwd)
	return label(spec.ID, found), err
}

// claudeHome returns the directory Claude Code keeps its state in.
func claudeHome() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
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
	// title is the name Claude Code gave the conversation.
	title string
	// prompted records that the summary is something a person typed rather
	// than a name Claude Code gave the conversation or nothing at all.
	prompted bool
	// headRead records that the opening entries were read as far as they are
	// ever read, so whatever they did not say they never will.
	headRead bool
	// newlines is the raw count of line breaks, kept apart from the entry
	// count so that an appended tail can be added to it.
	newlines int
	// partial records that the file ended mid-entry, which is what a
	// transcript being written to right now looks like.
	partial bool
}

// settled reports whether the opening entries have given up everything they
// have: what a person typed, and the name Claude Code gave the conversation.
// Until both are there they may still be coming. A pane opened a moment ago
// has neither, and the name follows the prompt by a few entries, because
// Claude Code cannot name a conversation before there is one.
//
// Or until there is nowhere left for them to come from. Only the opening
// entries are ever read, and they do not change once they are written, so a
// transcript already read that far has said all it is going to -- a
// conversation Claude Code never named, or one whose prompt is buried deeper
// than anybody is going to look. Without that, a transcript missing either
// would be read from the top and counted from the top on every refresh for
// as long as it kept growing, which for the agents that are working is every
// refresh there is.
func (f transcriptFacts) settled() bool {
	return f.headRead || (f.prompted && f.title != "")
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
	clock uint64
	dirs  map[string]cachedFolder
}{dirs: make(map[string]cachedFolder)}

// cachedFolder is one folder's transcripts and when it was last listed.
type cachedFolder struct {
	used  uint64
	files map[string]transcriptFacts
}

// cachedFolderLimit bounds how many project folders are remembered at once,
// so that someone who opens a great many projects in one sitting does not
// grow the cache without end.
const cachedFolderLimit = 64

// cachedFacts returns what the last listing of a folder found. The map is
// never written to once published, so reading it needs no lock of its own.
func cachedFacts(dir string) map[string]transcriptFacts {
	transcriptCache.Lock()
	defer transcriptCache.Unlock()
	return transcriptCache.dirs[dir].files
}

// rememberFacts publishes what this listing of a folder found, forgetting the
// folder nobody has looked at for longest when there is no room.
//
// Only the least useful one goes. A listing draws on several folders at once
// -- a project's own and one for every worktree of it -- so a couple of
// projects can fill the cache between them, and emptying the whole of it at
// that point would put every project back to reading every transcript it has.
func rememberFacts(dir string, facts map[string]transcriptFacts) {
	transcriptCache.Lock()
	defer transcriptCache.Unlock()

	if _, held := transcriptCache.dirs[dir]; !held {
		for len(transcriptCache.dirs) >= cachedFolderLimit {
			oldest, oldestUsed := "", uint64(0)
			for name, folder := range transcriptCache.dirs {
				if oldest == "" || folder.used < oldestUsed {
					oldest, oldestUsed = name, folder.used
				}
			}
			delete(transcriptCache.dirs, oldest)
		}
	}

	transcriptCache.clock++
	transcriptCache.dirs[dir] = cachedFolder{used: transcriptCache.clock, files: facts}
}

// transcriptLine is the part of a transcript entry that identifies the first
// real prompt.
type transcriptLine struct {
	Type    string `json:"type"`
	Cwd     string `json:"cwd"`
	AiTitle string `json:"aiTitle"`
	Message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// claudeConversations lists the stored conversations for a working directory,
// most recently used first.
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
func claudeConversations(cwd string) ([]Conversation, error) {
	// A directory named with a separator on the end is the same directory,
	// but it derives a folder name with a dash on the end, and the folders of
	// the worktrees inside it no longer start with that name.
	cwd = filepath.Clean(cwd)
	home := claudeHome()
	if home == "" {
		return nil, nil
	}
	projects := filepath.Join(home, "projects")

	dir := filepath.Join(projects, projectSlug(cwd))
	entries, derr := os.ReadDir(dir)
	out := conversationsIn(dir, entries, cwd, func(recorded string) bool {
		return ours(dir, recorded, cwd)
	})
	out = append(out, conversationsUnder(projects, cwd)...)
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
		out = conversationsIn(found, entries, cwd, func(recorded string) bool {
			return ours(found, recorded, cwd)
		})
	}

	out = newestOfEach(out)
	nameTheAlike(out)

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

// conversationsUnder lists the conversations that ran in cwd but are stored
// under a folder belonging to a directory inside it.
//
// That is what asking an agent to work in a worktree does. The session starts
// in the project and Claude Code follows it into the worktree, so the
// transcript is filed under the worktree while its entries record the project
// it belongs to -- and the project's own folder never hears about it. Of the
// 28 directories with history on this machine two have conversations stored
// that way, and one of them is a megabyte of work that the project it was
// done for could not offer. Delete the worktree afterwards, as one does, and
// nothing can offer it at all.
//
// A directory inside cwd derives a folder whose name begins with cwd's own,
// which is what these are found by. That also matches a sibling whose name
// merely starts the same way, so each candidate is asked what its transcripts
// record before it is read; the answer is the one the folder search already
// keeps, so a folder that is nothing to do with us costs a couple of reads
// once.
func conversationsUnder(projects, cwd string) []Conversation {
	entries, err := os.ReadDir(projects)
	if err != nil {
		return nil
	}
	prefix := projectSlug(cwd) + "-"

	var out []Conversation
	for _, e := range entries {
		if !e.IsDir() || !hasFolderPrefix(e.Name(), prefix) {
			continue
		}
		dir := filepath.Join(projects, e.Name())
		records, files := folderRecords(dir)
		mentions := false
		for _, got := range records {
			if sameDir(got, cwd) {
				mentions = true
				break
			}
		}
		if !mentions {
			continue
		}
		// Only what ran here. A transcript of the worktree's own work is the
		// worktree's, and one that records nowhere at all was abandoned
		// there rather than here.
		out = append(out, conversationsIn(dir, files, cwd, func(recorded string) bool {
			return recorded != "" && sameDir(recorded, cwd)
		})...)
	}
	return out
}

// hasFolderPrefix reports whether a project folder's name begins with the one
// derived from a directory, the way a folder for something inside it does.
func hasFolderPrefix(name, prefix string) bool {
	if len(name) < len(prefix) {
		return false
	}
	if pathsIgnoreCase {
		return strings.EqualFold(name[:len(prefix)], prefix)
	}
	return name[:len(prefix)] == prefix
}

// newestOfEach keeps one row per conversation.
//
// Resuming a conversation somewhere else copies its transcript under that
// directory's folder and leaves the first one behind, so the same id can be
// stored in more than one of the folders a listing draws from. The copy that
// was written last is the one that has the conversation in it.
func newestOfEach(all []Conversation) []Conversation {
	seen := make(map[string]int, len(all))
	out := all[:0]
	for _, c := range all {
		if i, ok := seen[c.ID]; ok {
			if c.Modified.After(out[i].Modified) {
				out[i] = c
			}
			continue
		}
		seen[c.ID] = len(out)
		out = append(out, c)
	}
	return out
}

// nameTheAlike replaces the summary of conversations that were opened with
// the same words by the name Claude Code gave each of them.
//
// Send the same prompt to ten agents at once -- which is what this
// application is for -- and the history panel lists ten rows reading "You are
// one of 10 agents working in parallel...", identical down to the last
// character, and nothing in them says which is which. Claude Code names a
// conversation after what it turned out to be about, and those names are all
// different: "layout tree bugs", "store layer fixes", "server bugs and
// usability". Where the prompt has stopped telling one row from another, the
// name is what is left that does.
//
// A conversation whose prompt is its own keeps it: what somebody typed is
// what they will look for.
func nameTheAlike(all []Conversation) {
	alike := make(map[string]int, len(all))
	for _, c := range all {
		alike[c.Summary]++
	}
	for i, c := range all {
		if c.Title != "" && alike[c.Summary] > 1 {
			all[i].Summary = c.Title
		}
	}
}

// describeReaders bounds how many transcripts are read at once. Reading a
// folder of them is spent waiting on the disk far more than working, so
// several at a time finish sooner than one after another; many more than
// this only queue up on the same disk.
const describeReaders = 8

// conversationsIn describes the transcripts in one project folder that belong
// to cwd, in whatever order the folder was read. Which of them do is up to
// the caller: it depends on how the folder was arrived at.
func conversationsIn(dir string, entries []os.DirEntry, cwd string, belongs func(recorded string) bool) []Conversation {
	files := make([]os.FileInfo, 0, len(entries))
	names := make([]string, 0, len(entries))
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		id := transcriptID(e.Name())
		if e.IsDir() || id == "" {
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
		ids = append(ids, id)
		files = append(files, info)
	}

	cached := cachedFacts(dir)
	facts := make([]transcriptFacts, len(names))
	read := make([]bool, len(names))
	readTranscripts(func(i int) {
		facts[i], read[i] = describeTranscript(filepath.Join(dir, names[i]), files[i], cached[names[i]])
	}, len(names))

	fresh := make(map[string]transcriptFacts, len(names))
	out := make([]Conversation, 0, len(names))
	for i, name := range names {
		// A transcript that could not be opened is remembered as nothing at
		// all, so the next listing tries again rather than showing an empty
		// description of it until the file happens to change. It is still
		// listed: it was in the folder a moment ago, and a row that fails to
		// resume is a smaller wrong than a conversation gone missing.
		if read[i] {
			fresh[name] = facts[i]
		}

		if !belongs(facts[i].cwd) {
			continue
		}

		c := Conversation{
			ID:       ids[i],
			Cwd:      cwd,
			Modified: files[i].ModTime(),
			Size:     files[i].Size(),
			Summary:  facts[i].summary,
			Title:    facts[i].title,
			Messages: facts[i].entries(),
		}
		if c.Summary == "" {
			c.Summary = NoPrompt
		}
		out = append(out, c)
	}
	rememberFacts(dir, fresh)
	if len(out) == 0 {
		return nil
	}
	return out
}

// ours reports whether a transcript in a project folder is this directory's
// to offer, given the directory the transcript records.
//
// Everything in a folder is resumable from the path that folder is derived
// from: that folder is where Claude Code looks for a conversation to resume.
// So a transcript recording some other directory is still ours when that
// directory would derive a folder of its own. It is a conversation that
// started there and moved here -- which is what a session that begins in a
// project and then goes to work in a worktree of it looks like, and it is
// stored here and nowhere else, so here is the only place it can be offered.
//
// What is not ours is a transcript recording a directory that would derive
// this very folder and is not this one. "my-app" and "my_app" mangle to the
// same name and their conversations pile up together; offering one the
// other's is offering to resume somebody else's work in the wrong tree.
//
// A transcript that records nowhere is an abandoned session and belongs to
// whoever asks.
func ours(dir, recorded, cwd string) bool {
	if recorded == "" || sameDir(recorded, cwd) {
		return true
	}
	return projectSlug(recorded) != filepath.Base(dir)
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

// transcriptID returns the session id a transcript's file name holds, or ""
// when the file is not a transcript that can be resumed.
//
// A transcript is named after the session id it holds, and that id is what
// `claude --resume` takes and what a pane restored from a saved layout is
// given. Claude Code also leaves files in these folders that are not that:
// one it has decided is orphaned is renamed with a timestamp and a hash
// appended -- "<id>.orphaned-1788557454976-69dd3a08.jsonl" -- and three of
// them are sitting in the folders on this machine. Offering one is offering
// a conversation whose id names nothing, and resuming it fails; the id
// buried in the name is no way back to it either, because whatever it
// belonged to is stored somewhere else or nowhere.
func transcriptID(name string) string {
	id, ok := strings.CutSuffix(name, ".jsonl")
	if !ok || id == "" || strings.Contains(id, ".") {
		return ""
	}
	return id
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
		records, _ := folderRecords(dir)
		for _, got := range records {
			if sameDir(got, cwd) {
				return dir, nil
			}
		}
	}
	return "", nil
}

// probedFolders remembers which working directories each project folder's
// transcripts named.
//
// This is the answer to "where has this project been before", and it is asked
// of every folder in turn, opening transcripts as it goes. It is asked most
// often by the projects that will not find an answer: a new worktree, or a
// project that has never been used here, has no folder of its own, so every
// refresh of its empty history panel walks the lot. With ten agents in ten
// worktrees that is the common case, not the rare one.
//
// What a folder's transcripts recorded cannot change while the folder holds
// the transcripts it held, so the answer is kept against how many that was --
// including the answer that a folder said nothing at all, which is the one
// the empty projects keep asking for.
//
// How many, rather than when the folder was last written: Windows keeps a
// second copy of a directory's timestamps in its parent, and that copy is
// what a listing of the parent hands back, and it is not updated when a file
// appears in the directory. A project whose first conversation had just been
// recorded would go on being told it has no history.
var probedFolders = struct {
	sync.Mutex
	dirs map[string]folderProbe
}{dirs: make(map[string]folderProbe)}

// folderProbe is what the transcripts a folder was opened at recorded, and
// how many transcripts the folder held at the time.
type folderProbe struct {
	transcripts int
	cwds        []string
}

// folderRecords returns the working directories the first few transcripts in
// a project folder record, opening them only when the folder has gained or
// lost a transcript since it was last looked at.
//
// The folder has to be read either way, to see whether what was remembered
// about it still holds, so what was read comes back with the answer: the
// caller that goes on to list the folder would otherwise read it again.
func folderRecords(dir string) ([]string, []os.DirEntry) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	transcripts := 0
	for _, f := range files {
		if !f.IsDir() && transcriptID(f.Name()) != "" {
			transcripts++
		}
	}

	probedFolders.Lock()
	probe, ok := probedFolders.dirs[dir]
	probedFolders.Unlock()
	if ok && probe.transcripts == transcripts {
		return probe.cwds, files
	}

	probe = folderProbe{transcripts: transcripts, cwds: probeFolder(dir, files)}
	probedFolders.Lock()
	probedFolders.dirs[dir] = probe
	probedFolders.Unlock()
	return probe.cwds, files
}

// probeFolder opens the first few transcripts in a project folder and returns
// the working directories they record.
//
// One transcript that names a directory identifies the folder, but the first
// file need not be that transcript: a session that was opened and abandoned
// leaves one with nothing in it, and a folder can be shared with a
// neighbouring directory whose path derives the same name. Look past those,
// up to a handful, rather than writing the folder off on the first answer.
func probeFolder(dir string, files []os.DirEntry) []string {
	var cwds []string
	tried := 0
	for _, f := range files {
		if f.IsDir() || transcriptID(f.Name()) == "" {
			continue
		}
		if got := transcriptCwd(filepath.Join(dir, f.Name())); got != "" {
			cwds = append(cwds, got)
		}
		if tried++; tried >= cwdProbeLimit {
			break
		}
	}
	return cwds
}

// pathsIgnoreCase is whether two paths differing only in case are expected to
// be the same directory. They are on Windows and on a Mac as it comes; they
// are not on Linux, where "~/src/App" and "~/src/app" are two projects that
// happen to look alike.
var pathsIgnoreCase = runtime.GOOS == "windows" || runtime.GOOS == "darwin"

// sameDir compares two recorded working directories.
//
// Whether case matters is the filesystem's business rather than ours. Where
// it does not, Claude Code recorded whichever spelling the session was
// started with and this has to see through that; where it does, treating two
// directories as one shows a project its neighbour's conversations and offers
// to resume one of them here.
func sameDir(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if pathsIgnoreCase {
		return strings.EqualFold(a, b)
	}
	return a == b
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
	defer lines.release()
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
// The two cost very different things. The prompt is near the top, so parsing
// stops after the opening entries. The count has to reach the end of the
// file, but counting line breaks is a scan rather than a parse and stays
// quick on the tens of megabytes a long conversation runs to -- and, unlike a
// parse, is not stopped by a single entry too large to hold in memory. They
// are found in one pass all the same: the entries the prompt is parsed out of
// are counted as they go past, and the rest of the file is then scanned from
// where that stopped. Reading the opening entries can be a good fraction of
// reading the file -- across the transcripts on this machine, an eighth --
// and reading them twice bought nothing.
//
// prev is what the last listing found, and lets most of even that be skipped.
// An untouched file is not opened at all. A transcript is only ever appended
// to, so one that has merely grown still opens with the same prompt and still
// holds the line breaks already counted: only the new tail is read. Anything
// else -- a first look, a file that shrank, one replaced at the same size,
// one that had not been prompted yet when it was last read -- is read whole.
//
// What is read is the file as the directory listing described it, not as it
// is by the time it is opened. A transcript belonging to an agent that is
// working right now grows between the two, and counting to the end of it
// would count entries the recorded size does not cover -- which the next
// refresh, resuming from that size, would then count again.
func describeTranscript(path string, info os.FileInfo, prev transcriptFacts) (transcriptFacts, bool) {
	now := transcriptFacts{modTime: info.ModTime(), size: info.Size()}
	if prev.size == now.size && prev.modTime.Equal(now.modTime) {
		return prev, true
	}

	f, err := os.Open(path)
	if err != nil {
		// Deleted since the folder was read, or locked by something else.
		// Either way nothing has been learned about it.
		return now, false
	}
	defer f.Close()

	tail := now.size
	// A transcript that has not said everything about itself yet is one a
	// pane was opened for a moment ago, and the panel is full of those when
	// several agents have just been spawned. Its opening entries are read
	// again as it grows, so that the row picks up what was typed and what
	// Claude Code called it, instead of standing at "no prompt recorded" --
	// or at a prompt ten agents share -- for as long as the application runs.
	grown := prev.settled() && prev.size > 0 && now.size > prev.size
	if grown {
		if _, err := f.Seek(prev.size, io.SeekStart); err != nil {
			return now, false
		}
		now.summary, now.cwd, now.newlines = prev.summary, prev.cwd, prev.newlines
		now.title, now.prompted, now.headRead = prev.title, prev.prompted, prev.headRead
		tail = now.size - prev.size
	}

	counted := &countingReader{r: io.LimitReader(f, tail), last: '\n'}
	if !grown {
		now.summary, now.title, now.cwd, now.headRead = openingPrompt(counted)
		now.prompted = now.summary != ""
		if now.summary == "" {
			// Nothing was typed here that a person would recognise the
			// conversation by, but Claude Code may have named it.
			now.summary = now.title
		}
	}
	drain(counted)

	now.newlines += counted.newlines
	now.partial = counted.last != '\n'
	return now, true
}

// aiTitleMark is what an entry carrying the name Claude Code gave a
// conversation has in it.
var aiTitleMark = []byte(`"ai-title"`)

// openingPrompt reads the first thing the user asked, the name Claude Code
// gave the conversation, and the working directory the transcript records,
// looking only at the opening entries.
//
// All three come back together because they are found in the same walk: every
// entry carries the directory, so the one holding the prompt almost always
// carries it too, and the name is written within the first entries as well --
// though after the prompt, since Claude Code cannot name a conversation until
// there is one. Walking past the prompt to find it is what makes ten agents
// sent the same instruction tell apart in the panel.
//
// The walk stops as soon as all three are in hand, which on the transcripts
// on this machine is around the twenty-fifth entry. read says whether it got
// to the end of what it will ever look at, either way, so that a transcript
// which is never going to say more is not read again and again. Until then, entries that
// cannot carry what is still missing are not parsed at all: the ones in the
// way are the megabyte-long ones -- a tool result, a pasted file -- and
// looking for a mark in the bytes costs a fraction of parsing them as JSON.
func openingPrompt(r io.Reader) (prompt, title, cwd string, read bool) {
	lines := newTranscriptReader(r)
	defer lines.release()
	for i := 0; ; i++ {
		if prompt != "" && title != "" && cwd != "" {
			return prompt, title, cwd, true
		}
		if i >= summaryScanLimit {
			// As far as the opening entries are ever read. Whatever they have
			// not said by here, they are not going to.
			return prompt, title, cwd, true
		}
		raw, ok := lines.next()
		if !ok {
			// The end of the file, which a transcript being written to now
			// will pass: the entries after it may still say more.
			break
		}
		named := bytes.Contains(raw, aiTitleMark)
		if !named && prompt != "" && cwd != "" {
			continue
		}
		var line transcriptLine
		if json.Unmarshal(raw, &line) != nil {
			continue
		}
		if cwd == "" {
			cwd = line.Cwd
		}
		if line.Type == "ai-title" {
			// Claude Code writes the name out again every turn or so, the
			// same name each time, so the first one is the name.
			if title == "" {
				title = firstPrompt(line.AiTitle)
			}
			continue
		}
		if prompt != "" || line.Type != "user" || line.Message.Role != "user" {
			continue
		}
		prompt = contentText(line.Message.Content)
	}
	return prompt, title, cwd, false
}

// countingReader counts the line breaks in everything read through it, and
// keeps the last byte that went past. A transcript has one entry per line, so
// a file that ends without a line break still has an entry on that last line:
// a transcript being written to at this moment usually does. last starts as a
// line break so that a reader nothing is read from is not one entry.
type countingReader struct {
	r        io.Reader
	newlines int
	last     byte
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.newlines += bytes.Count(p[:n], []byte{'\n'})
		c.last = p[n-1]
	}
	return n, err
}

// scratch hands out the buffers a transcript is read through.
//
// A folder is read a few transcripts at a time, so a handful of buffers serve
// the whole of it, but a buffer for each left tens of megabytes of rubbish
// behind on every listing that read the files -- and this runs in an
// application whose other windows are pushing terminal output around, where
// the collector's time is somebody's typing not appearing.
var scratch = sync.Pool{New: func() any {
	buf := make([]byte, 256<<10)
	return &buf
}}

// drain reads the rest of a reader and throws it away.
func drain(r io.Reader) {
	buf := scratch.Get().(*[]byte)
	defer scratch.Put(buf)
	for {
		if _, err := r.Read(*buf); err != nil {
			return
		}
	}
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

// transcriptReaders keeps readers between transcripts, for the same reason
// the buffers they read through are kept: one per file is one per file's
// worth of rubbish.
var transcriptReaders = sync.Pool{New: func() any {
	return &transcriptReader{br: bufio.NewReaderSize(nil, 64<<10)}
}}

func newTranscriptReader(r io.Reader) *transcriptReader {
	t := transcriptReaders.Get().(*transcriptReader)
	t.br.Reset(r)
	t.buf = t.buf[:0]
	return t
}

// keptEntrySize bounds how large a reader's own buffer may be to be worth
// keeping. One transcript with a pasted image in it grows a reader to the
// size of the paste, and holding that for the rest of the run to save an
// allocation is the wrong way round.
const keptEntrySize = 1 << 20

// release hands a reader back once a transcript has been walked.
func (t *transcriptReader) release() {
	t.br.Reset(nil)
	if cap(t.buf) > keptEntrySize {
		t.buf = nil
	}
	transcriptReaders.Put(t)
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

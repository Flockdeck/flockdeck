package gitx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// FileChange is one modified file in a working tree.
type FileChange struct {
	Path string
	// Status is the two-letter porcelain code, e.g. " M", "A ", "??".
	Status string
	// Label is that code rendered for people: "modified", "new", …
	Label     string
	Staged    bool
	Unstaged  bool
	Untracked bool
	Added     int
	Removed   int
}

// Changes lists the files that differ from HEAD, with line counts.
func Changes(dir string) ([]FileChange, error) { return changes(dir, maxCounted) }

// changes is Changes with the counting limit given, so a test can reach it
// without writing a thousand files.
func changes(dir string, limit int) ([]FileChange, error) {
	// Neither call needs an answer from the other, and each one is a process:
	// run concurrently they cost one wait rather than two, which is what the
	// panel notices on a checkout with a lot of changes in it.
	//
	// The line counts are cancellable because they are the expensive call. git
	// has to read and diff every changed file to produce them, which took 20s
	// over a 10,000-file diff against 139ms for the status call that lists the
	// same files. Once status has come back and said there are more files than
	// anyone is going to read a "+12" beside, the count is killed where it
	// stands rather than left to finish work that is about to be thrown away.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var (
		counts map[string]lineCount
		wg     sync.WaitGroup
	)
	wg.Add(1)
	go func() { defer wg.Done(); counts = lineCounts(ctx, dir) }()
	// -z is what makes the names trustworthy: without it git quotes anything
	// with a space, a quote or a non-ASCII character and escapes the bytes, so
	// "café.txt" arrives as "caf\303\251.txt" and no longer names a real file.
	// It also puts a rename's old name in its own record rather than writing
	// "old -> new", which a file genuinely called "a -> b" was mistaken for.
	out, err := run(dir, "status", "--porcelain", "--untracked-files=all", "-z")
	// One record per entry, plus one more for each rename's old name, so this
	// runs a little ahead of the true count -- which is the safe direction for
	// deciding that there are too many to bother counting.
	tooMany := err == nil && strings.Count(out, "\x00") > limit
	if tooMany {
		cancel()
	}
	wg.Wait()
	if err != nil {
		return nil, err
	}
	if tooMany {
		// A count small enough to have finished before the cancel reached it
		// is thrown away all the same, so being over the limit means one thing
		// however that race came out.
		counts = nil
	}

	files := parseStatus(out)
	for i := range files {
		if n, ok := counts[files[i].Path]; ok {
			files[i].Added, files[i].Removed = n.added, n.removed
		}
	}
	countUntracked(dir, files, limit)
	return files, nil
}

// parseStatus reads the records of `git status --porcelain -z` into the files
// a commit would record, without their line counts.
func parseStatus(out string) []FileChange {
	var files []FileChange
	for _, rec := range statusRecords(out) {
		path, ok := rec.shown()
		if !ok {
			continue
		}
		code := rec.code
		fc := FileChange{
			Path:      path,
			Status:    code,
			Label:     statusLabel(code),
			Staged:    code[0] != ' ' && code[0] != '?',
			Unstaged:  code[1] != ' ' && code[1] != '?',
			Untracked: code == "??",
		}
		files = append(files, fc)
	}
	return files
}

// countUntracked fills in the line counts of the new files.
//
// git does not report those, so every untracked entry is a file to open and
// read to the end. A checkout that has picked up a build directory or an
// unignored node_modules has tens of thousands of them, and doing it one file
// at a time took 27 seconds over 20,000 of them -- almost all of it waiting on
// the disk with nothing else in flight, while the git call that found them
// took 262ms. Reading several at once is what shortens that wait.
func countUntracked(dir string, files []FileChange, limit int) {
	const readers = 16
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < readers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// One buffer a worker, not one a file: a buffer a file was 64KB of
			// garbage for every new file counted, 64MB a refresh at the limit.
			buf := make([]byte, 64<<10)
			// Each worker writes to its own elements, which no one else reads
			// until every one of them has finished.
			for i := range jobs {
				files[i].Added = countLines(filepath.Join(dir, files[i].Path), buf)
			}
		}()
	}
	left := limit
	for i := range files {
		if !files[i].Untracked {
			continue
		}
		if left == 0 {
			break
		}
		left--
		jobs <- i
	}
	close(jobs)
	wg.Wait()
}

// maxCounted bounds how many files have their lines counted, whether the
// number comes from git or from reading a new file here.
//
// The count is a nicety -- a "+40" beside the name -- and it is not cheap:
// every untracked file is one to read to the end, and every tracked one is a
// diff for git to compute. Reading 20,000 new files at once brought them down
// from 27s to 6s, which is still seconds of the panel not appearing, for
// numbers on rows nobody scrolls to. Past this many a file is still listed,
// without a count, which is how a file with nothing added in it looks anyway.
const maxCounted = 1000

type lineCount struct{ added, removed int }

// lineCounts reads how many lines each changed file adds and removes against
// the last commit: the comparison the panel's diff shows, and the one the
// commit button takes.
//
// It was the staged and the unstaged diff counted apart and added up, which
// is a different sum. A line staged and then taken out again counted +1 -1
// beside a file the commit would not touch, and a staged line edited again
// was counted twice. Before the first commit there is no HEAD to compare with,
// so there the two are still added up.
func lineCounts(ctx context.Context, dir string) map[string]lineCount {
	counts, err := numstat(ctx, dir, "HEAD")
	if err == nil || ctx.Err() != nil {
		return counts
	}
	staged, _ := numstat(ctx, dir, "--cached")
	unstaged, _ := numstat(ctx, dir)
	sum := map[string]lineCount{}
	for _, part := range []map[string]lineCount{staged, unstaged} {
		for path, n := range part {
			s := sum[path]
			sum[path] = lineCount{added: s.added + n.added, removed: s.removed + n.removed}
		}
	}
	return sum
}

// numstat reads per-file line counts for one diff, named by against.
//
// The keys have to match the names Changes reports, so this reads -z as well:
// otherwise a quoted name never matches, and a rename is keyed under
// "old => new", which matches nothing at all and left it counted as 0/0.
func numstat(ctx context.Context, dir string, against ...string) (map[string]lineCount, error) {
	// --find-renames pins what is paired to renames, whatever diff.renames
	// says: with "copies" set there, a copied file was counted against the
	// file it came from, 0/0, though the commit adds all of it.
	args := append([]string{"diff", "--numstat", "-z", "--find-renames"}, against...)
	out, err := runUntil(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	counts := map[string]lineCount{}
	records := strings.Split(out, "\x00")
	for i := 0; i < len(records); i++ {
		fields := strings.SplitN(records[i], "\t", 3)
		if len(fields) < 3 {
			continue
		}
		// Binary files report "-" instead of a count.
		a, _ := strconv.Atoi(fields[0])
		r, _ := strconv.Atoi(fields[1])
		path := fields[2]
		if path == "" {
			// A rename leaves the path field empty and sends the old and new
			// names as the next two records; the new one is what is shown.
			if i+2 >= len(records) {
				break
			}
			path = records[i+2]
			i += 2
		}
		counts[path] = lineCount{added: a, removed: r}
	}
	return counts, nil
}

// unmerged holds the porcelain codes git uses for a conflicted file. Several
// of them contain no "U" at all -- "AA" is both sides adding, "DD" both
// deleting -- so they have to be recognised as a set before the letters are
// read individually, or a conflict is labelled "added" or "deleted".
var unmerged = map[string]bool{
	"DD": true, "AU": true, "UD": true, "UA": true,
	"DU": true, "AA": true, "UU": true,
}

// statusLabel turns a porcelain code into a word.
func statusLabel(code string) string {
	switch {
	case code == "??":
		return "new"
	case unmerged[code]:
		return "conflict"
	case strings.ContainsAny(code, "D"):
		return "deleted"
	case strings.ContainsAny(code, "R"):
		return "renamed"
	case strings.ContainsAny(code, "AC"):
		// A copy -- reported only with status.renames set to "copies" -- is a
		// new file, and was labelled "modified".
		return "added"
	case strings.ContainsAny(code, "U"):
		return "conflict"
	default:
		return "modified"
	}
}

// looksBinary applies git's own test: a NUL byte near the start of the file.
func looksBinary(data []byte) bool {
	const sniff = 8000
	if len(data) > sniff {
		data = data[:sniff]
	}
	return bytes.IndexByte(data, 0) >= 0
}

// countLines counts the lines in a file, used to size an untracked addition.
//
// It reads in chunks rather than whole: this runs for every untracked file on
// every refresh of the panel, and one of them can be a multi-gigabyte log or
// model checkpoint that nobody wants held in memory to be counted. buf is the
// chunk, the caller's to reuse from one file to the next.
func countLines(path string, buf []byte) int {
	// Only a regular file is opened. git lists a named pipe in a working
	// tree as an untracked file like any other, and opening one blocks until
	// somebody writes to it -- with no deadline here, that is the review
	// panel waiting for good on a file nobody is going to write to. A
	// symlink is skipped for a different reason: what git counts for one is
	// the link itself, not whatever is on the other end of it.
	if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	var (
		lines int
		last  byte
		empty = true
	)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if empty && looksBinary(chunk) {
				return 0
			}
			empty = false
			lines += bytes.Count(chunk, []byte{'\n'})
			last = chunk[n-1]
		}
		if err != nil {
			break
		}
	}
	// A file that does not end in a newline still has a final line.
	if !empty && last != '\n' {
		lines++
	}
	return lines
}

// readCapped reads at most limit bytes of a file and reports its full size, so
// a caller can say how much it left behind.
func readCapped(path string, limit int64) ([]byte, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, 0, err
	}
	return data, fi.Size(), nil
}

// maxDiffBytes bounds what is sent to the interface. A generated file can be
// enormous and nobody reads a million lines in a side panel.
const maxDiffBytes = 400 << 10

// Diff returns a unified diff for one file, including untracked files, whose
// content is shown as an addition.
func Diff(dir, path string) (string, error) {
	// The path arrives from the interface, so it has to be checked before it
	// is joined onto the working tree: "../../.ssh/id_rsa" is not a file
	// anyone asked the review panel about.
	if !insideTree(path) {
		return "", &gitError{"not a path inside the working tree: " + path}
	}
	full := filepath.Join(dir, path)
	// Two questions are asked of every click -- is this something git does
	// not track, and what is its diff against the last commit -- and they are
	// asked at once. Each is a process, and one after the other they were most
	// of the wait: 282ms a click in BenchmarkDiff on Windows. The diff is
	// thrown away for a new file, which costs that click nothing it did not
	// already wait for.
	//
	// Lstat, so a symlink is seen as a symlink rather than as whatever it
	// points at. A directory is only ever asked about with a slash on the end,
	// which is how status names a repository sitting inside this one.
	fi, lerr := os.Lstat(full)
	var (
		isNew bool
		wg    sync.WaitGroup
	)
	if lerr == nil && (!fi.IsDir() || strings.HasSuffix(path, "/")) {
		wg.Add(1)
		go func() { defer wg.Done(); isNew = untracked(dir, path) }()
	}

	// HEAD covers staged and unstaged changes together, which is what someone
	// reviewing "what changed" wants to see. Before the first commit there is
	// no HEAD to name -- git fails with "bad revision" rather than treating it
	// as empty -- and everything staged is the change.
	//
	// Trying it and falling back costs one call in a repository that has a
	// commit, where asking first cost two: this runs on every click in the
	// file list. When the fallback fails as well the first failure is the one
	// worth reporting, since a missing HEAD is not what went wrong.
	against := "HEAD"
	out, err := gitDiff(dir, against, "--", pathspec(path))
	wg.Wait()
	if isNew {
		if text, ok, nerr := newFileDiff(path, full, fi); ok || nerr != nil {
			return text, nerr
		}
	}
	if err != nil {
		against = "--cached"
		staged, stagedErr := gitDiff(dir, against, "--", pathspec(path))
		if stagedErr != nil {
			return "", err
		}
		out = staged
	}
	if isWholeFileAddition(out) {
		// git pairs a rename by looking at both names, and a pathspec naming
		// only the new one leaves it nothing to pair against: what comes back
		// is the whole file as a fresh addition. A file that moved and had
		// three lines changed in it read as two thousand added ones, with the
		// actual edit somewhere inside them. Asking again with both names is
		// what lets git see it for what it is.
		if from := renameSource(dir, path); from != "" {
			if paired, perr := gitDiff(dir, against, "--", pathspec(path), pathspec(from)); perr == nil {
				out = paired
			}
		}
	}
	// A submodule whose only change is work inside it -- a new file there --
	// has no diff of its own here: nothing at all, which the panel explained
	// as a file that matches the last commit, or git's one line that it
	// "contains untracked content". Asked only of an empty diff or a
	// submodule's, so an ordinary file's click costs nothing more.
	if t := strings.TrimSpace(out); t == "" || strings.HasPrefix(t, "Submodule ") {
		if subs := submoduleWork(dir, path); len(subs) > 0 {
			return fmt.Sprintf("%s is a submodule with work inside it that is not committed there yet. That work belongs "+
				"to the submodule's own repository: commit it there, and this one can then record the submodule's new commit.\n", path), nil
		}
	}
	// An empty answer is the answer. Asking again for the index against the
	// working tree, as this once did, only ever found something when the
	// working tree had gone back to the last commit and the index had not --
	// and then showed an edit the commit would not contain: a line staged and
	// then taken out again read as a line being deleted.
	return out, nil
}

// newFileDiff shows something git does not track yet, and so has no diff for.
// ok is false for what it leaves to git: anything that is neither a regular
// file, a symlink nor a repository -- a named pipe, a device -- which reading
// here could block on until something else writes to it.
func newFileDiff(path, full string, fi os.FileInfo) (text string, ok bool, err error) {
	switch {
	case fi.IsDir():
		// A repository inside the tree is listed as one entry because its
		// files belong to it and not to this one. git has no diff to give for
		// it, and the empty answer was shown as "this file matches the last
		// commit". What a commit would do with it is worth knowing before
		// pressing the button.
		return fmt.Sprintf("--- /dev/null\n+++ b/%s\nA separate git repository, not tracked by this one. "+
			"Committing records only which commit it is on, none of its files.\n", path), true, nil
	case fi.Mode()&os.ModeSymlink != 0:
		// git stores a symlink as the path it points at and shows that as the
		// file's one line. Following it would print the contents of whatever
		// is on the other end, which is not what was added and need not even
		// be inside the working tree.
		target, err := os.Readlink(full)
		if err != nil {
			return "", false, err
		}
		return renderAsAddition(path, target, 0), true, nil
	case fi.Mode().IsRegular():
		// Only as much as the panel will show is read: a new file can be a
		// gigabyte of generated output.
		data, size, err := readCapped(full, maxDiffBytes+1)
		if err != nil {
			return "", false, err
		}
		if looksBinary(data) {
			// git says this rather than printing the bytes, and so should a
			// panel that has to render them as text.
			return fmt.Sprintf("--- /dev/null\n+++ b/%s\nBinary file (%d bytes)\n", path, size), true, nil
		}
		if size > int64(len(data)) {
			// Do not end on half a line.
			if i := bytes.LastIndexByte(data, '\n'); i >= 0 {
				data = data[:i+1]
			}
		}
		return renderAsAddition(path, string(data), size-int64(len(data))), true, nil
	}
	return "", false, nil
}

// pathspec wraps a file name so git reads it as the name of one file rather
// than as a pattern.
//
// A bare pathspec is glob-matched, so a file genuinely called "report[1].csv"
// -- what a browser calls a second download -- does not match itself, and does
// match "report1.csv" instead: clicking the first in the review panel showed
// the diff of the second alongside it. ":(literal)" turns matching off, and
// stops a name that begins with a colon being read as magic in its own right.
func pathspec(path string) string { return ":(literal)" + path }

// diffFlags pin git's diff output to the shape the panel reads, whatever the
// user keeps in their config.
//
// "color.ui = always" is what people set when they want colour through a pager,
// and git obeys it here too even though nothing is attached to a terminal: the
// panel then shows the escape sequences as text and colours nothing, because no
// line begins with a "+" any more. "diff.external" replaces the diff wholesale
// with the output of some other program. "diff.mnemonicPrefix" and
// "diff.noprefix" rename or drop the "a/" and "b/" that an untracked file's
// rendering writes by hand, leaving the two sources of diff text unalike.
// "--submodule=log" shows a submodule that moved as the commits it moved
// through, by subject -- "> fix the parser", "<" for one it went back past --
// where the default was two forty-character hashes that said nothing of what
// changed, and nothing of a bump being undone.
var diffFlags = []string{"--no-color", "--no-ext-diff", "--find-renames", "--src-prefix=a/", "--dst-prefix=b/", "--submodule=log"}

// gitDiff runs a diff with those flags ahead of the caller's arguments, and
// returns it cut down to what the panel is sent.
//
// Only that much is kept as git writes it. A regenerated lockfile or bundle
// can differ by tens of megabytes, and holding all of it to throw nearly all
// of it away allocated 222MB for a 70MB diff, on every click of the file.
func gitDiff(dir string, args ...string) (string, error) {
	argv := make([]string, 0, len(diffFlags)+len(args)+1)
	argv = append(argv, "diff")
	argv = append(argv, diffFlags...)
	argv = append(argv, args...)
	out := &headWriter{limit: maxDiffBytes + 1}
	if _, err := runTo(context.Background(), commandTimeout, dir, nil, out, argv...); err != nil {
		return "", err
	}
	return truncateDiff(out.String(), out.total), nil
}

// headWriter keeps the first limit bytes written to it and counts the rest.
type headWriter struct {
	head  []byte
	limit int
	total int
}

func (w *headWriter) Write(p []byte) (int, error) {
	w.total += len(p)
	if room := w.limit - len(w.head); room > 0 {
		w.head = append(w.head, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (w *headWriter) String() string { return string(w.head) }

// isWholeFileAddition reports whether a diff says the file did not exist
// before. The test is anchored to the start of a line: a diff body carries
// the same words as content often enough, behind a "+" or a space.
func isWholeFileAddition(diff string) bool {
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "new file mode ") {
			return true
		}
	}
	return false
}

// renameSource names the file a path was renamed from, or "" if it was not.
//
// The status has to cover the whole tree: rename detection needs both sides,
// and limiting it to the new name leaves git pairing it against nothing and
// calling it an addition, which is the very answer being checked here. That
// is why it is only asked once a diff has already come back looking like a
// file that never existed before, which is rare among the files anyone
// clicks.
func renameSource(dir, path string) string {
	out, err := run(dir, "status", "--porcelain", "--untracked-files=no", "-z")
	if err != nil {
		return ""
	}
	records := strings.Split(out, "\x00")
	for i := 0; i < len(records); i++ {
		entry := records[i]
		if len(entry) < 4 || (entry[0] != 'R' && entry[0] != 'C') {
			continue
		}
		// The name it came from follows as its own record.
		i++
		// A copy is a new file; its source is still there with a row of its
		// own. Paired with it, the copy's diff carried the source's edits too.
		if entry[0] == 'C' {
			continue
		}
		if i >= len(records) {
			break
		}
		if entry[3:] == path {
			return records[i]
		}
	}
	return ""
}

// insideTree reports whether a repository-relative path stays within the tree.
func insideTree(path string) bool {
	if path == "" || filepath.IsAbs(path) || filepath.VolumeName(path) != "" {
		return false
	}
	// git speaks in forward slashes; both separators have to be considered on
	// Windows, where a leading one is still a rooted path.
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\`) {
		return false
	}
	clean := filepath.Clean(path)
	return clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

// untracked reports whether the file is one git does not have in its index,
// and so has no diff to show for.
//
// It asks the positive question -- is this one of the "other" files? -- rather
// than reading the exit code of `ls-files --error-unmatch`, which is also
// non-zero when the directory is not a repository at all. Answering "yes" to
// that used to make a tracked file's contents appear as one huge addition
// whenever git failed for any reason.
func untracked(dir, path string) bool {
	out, err := run(dir, "ls-files", "--others", "--", pathspec(path))
	return err == nil && strings.TrimSpace(out) != ""
}

// renderAsAddition shows a file's content as one big addition. omitted is the
// number of bytes the caller did not read, and is reported at the end.
func renderAsAddition(path, content string, omitted int64) string {
	// A file's trailing newline terminates its last line rather than starting
	// an empty one, so splitting it off would show a phantom "+" at the end.
	// git marks the other case explicitly, and so do we.
	endsWithNewline := strings.HasSuffix(content, "\n")
	var lines []string
	if content != "" {
		lines = strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	}

	var b strings.Builder
	b.WriteString("--- /dev/null\n+++ b/" + path + "\n")
	b.WriteString(fmt.Sprintf("@@ -0,0 +1,%d @@\n", len(lines)))
	var (
		capped bool
		shown  int // bytes of content written out so far
	)
	for i, line := range lines {
		b.WriteString("+" + line + "\n")
		shown += len(line) + 1
		if !endsWithNewline && i == len(lines)-1 {
			b.WriteString("\\ No newline at end of file\n")
		}
		if b.Len() > maxDiffBytes {
			if omitted > 0 {
				// The lines left over are only those of the part that was
				// read. Counting them said "68282 more lines" of a file with
				// a quarter of a million still to go, so what is left is
				// given in bytes, which are known for all of it.
				b.WriteString(fmt.Sprintf("… truncated, %d more bytes\n", max(0, int64(len(content)-shown))+omitted))
			} else {
				b.WriteString(fmt.Sprintf("… truncated, %d more lines\n", len(lines)-i-1))
			}
			capped = true
			break
		}
	}
	if omitted > 0 && !capped {
		b.WriteString(fmt.Sprintf("… truncated, %d more bytes\n", omitted))
	}
	return b.String()
}

// truncateDiff caps a diff at maxDiffBytes, on a line boundary: cutting at an
// exact byte count leaves half a line, which the panel colours as though it
// were a real one, and can slice a UTF-8 character in two.
//
// s is at least the start of the diff, and total is how long all of it was.
func truncateDiff(s string, total int) string {
	if total <= maxDiffBytes {
		return s
	}
	cut := s[:maxDiffBytes]
	if i := strings.LastIndexByte(cut, '\n'); i >= 0 {
		cut = cut[:i+1]
	} else {
		cut += "\n"
	}
	return cut + fmt.Sprintf("… truncated, %d more bytes\n", total-len(cut))
}

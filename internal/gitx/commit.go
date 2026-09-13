package gitx

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// CommitAll stages everything and commits it.
//
// Both steps run the user's own programs -- a clean filter such as LFS for
// the add, the pre-commit hooks for the commit, which as often as not lint or
// run the tests -- so neither is held to the deadline for reading the
// repository. A hook still going at twenty seconds was killed there, and the
// commit it guarded reported as a hang.
func CommitAll(dir, message string) error {
	if strings.TrimSpace(message) == "" {
		return errEmptyMessage
	}
	out, err := run(dir, "status", "--porcelain", "--untracked-files=all", "-z")
	if err != nil {
		return err
	}
	if err := unresolved(dir, statusRecords(out)); err != nil {
		return err
	}
	if err := writingIndex(dir, "", "add", "--all"); err != nil {
		return err
	}
	return commitIndex(dir, message)
}

// commitIndex commits what has been staged.
func commitIndex(dir, message string) error {
	// The message goes in on stdin: on the command line a long one was more
	// than Windows would start a program with.
	err := writingIndex(dir, message, "commit", "-F", "-")
	if err != nil {
		// Nothing staged, while the file list showed a submodule as changed:
		// the work is inside it, in a repository of its own, and git's
		// "Changes not staged for commit" advice was about commands the panel
		// has no way to run. Asked only when nothing is staged, so a commit
		// refused for any other reason -- a hook -- still says so.
		if _, qerr := run(dir, "diff", "--cached", "--quiet"); qerr == nil {
			if subs := submoduleWork(dir); len(subs) > 0 {
				return &gitError{fmt.Sprintf("nothing here to commit: the changes are inside %s, a submodule -- a repository "+
					"of its own. Commit them there first; committing here then records its new commit.", strings.Join(subs, ", "))}
			}
		}
	}
	return err
}

// CommitReviewed commits the files somebody was shown, and refuses when they
// are no longer what a commit would record.
//
// The review panel lists the tree when it is read and commits when a button is
// pressed, and agents go on writing in between. CommitAll stages whatever is
// there at that moment, so a file written after the list was read went into a
// commit nobody had looked at. listed are the paths the panel showed and
// unlisted how many more it left out. When the files git would now record are
// not those, nothing is staged, and the error says how many moved so that the
// list can be read again.
func CommitReviewed(dir, message string, listed []string, unlisted int) error {
	if strings.TrimSpace(message) == "" {
		return errEmptyMessage
	}
	moved, err := changedSince(dir, listed, unlisted)
	if err != nil {
		return err
	}
	if moved == 1 {
		return &gitError{"1 file changed since you looked — refresh"}
	}
	if moved > 0 {
		return &gitError{fmt.Sprintf("%d files changed since you looked — refresh", moved)}
	}
	return CommitAll(dir, message)
}

// changedSince counts the files that differ between what a list showed and
// what a commit would record now: those listed that no longer differ from the
// last commit, those that now differ and were not listed, and -- for a list
// that left some out -- how far the count of the ones it left out has moved.
// A listed file written to again is not counted; it is the same file.
func changedSince(dir string, listed []string, unlisted int) (int, error) {
	out, err := run(dir, "status", "--porcelain", "--untracked-files=all", "-z")
	if err != nil {
		return 0, err
	}
	shown := make(map[string]bool, len(listed))
	for _, p := range listed {
		shown[p] = true
	}
	now := make(map[string]bool)
	extra := 0
	for _, f := range parseStatus(out) {
		now[f.Path] = true
		if !shown[f.Path] {
			extra++
		}
	}
	moved := 0
	for p := range shown {
		if !now[p] {
			moved++
		}
	}
	if extra > unlisted {
		moved += extra - unlisted
	} else {
		moved += unlisted - extra
	}
	return moved, nil
}

// statusRecord is one entry of `git status --porcelain -z`: its two-letter
// code, its name, and for a rename or a copy the name it came from.
type statusRecord struct{ code, path, from string }

// statusRecords reads the records of `git status --porcelain -z`.
func statusRecords(out string) []statusRecord {
	var recs []statusRecord
	records := strings.Split(out, "\x00")
	for i := 0; i < len(records); i++ {
		entry := records[i]
		if len(entry) < 4 {
			continue
		}
		rec := statusRecord{code: entry[:2], path: entry[3:]}
		if rec.code[0] == 'R' || rec.code[0] == 'C' {
			i++ // the name it came from follows as its own record
			if i < len(records) {
				rec.from = records[i]
			}
		}
		recs = append(recs, rec)
	}
	return recs
}

// shown is the name the panel lists a record under, and false for a record it
// leaves out.
func (r statusRecord) shown() (string, bool) {
	switch {
	case r.code == "AD" || r.code == "CD":
		// Staged as new -- or as a copy -- then deleted: the last commit
		// never had it, and the working tree no longer does, so a commit
		// does nothing with it. It was listed as a deletion of content
		// that was never committed.
		return "", false
	case r.code == "RD" && r.from != "":
		// Renamed, then the new name deleted: what a commit does is delete
		// the old one. It was listed under the new name, which the last
		// commit never had, with an empty diff -- while the file actually
		// going, the old name, had no row at all.
		return r.from, true
	}
	return r.path, true
}

// lockWait is how long a command that writes the index waits for another git
// process to let go of it. It is a variable so a test can shorten it.
var lockWait = 3 * time.Second

// writingIndex runs a command that writes the index, waiting out another git
// process that holds it.
//
// Agents commit in their own panes all the time, and the panel's Commit
// pressed at that moment failed on git's index.lock with an explanation whose
// last line -- the one saying what to do -- was cut from the toast. Their git
// commands are brief, so the lock is waited for and the command tried once
// more; a lock that stays is one to say so about, in the reader's terms.
func writingIndex(dir, input string, args ...string) error {
	_, _, err := runWithInput(networkTimeout, dir, input, args...)
	if err == nil {
		return nil
	}
	switch lock, held := waitForIndex(dir, err); {
	case lock == "":
		return err
	case held:
		return indexHeld(lock)
	}
	_, _, err = runWithInput(networkTimeout, dir, input, args...)
	return err
}

// waitForIndex is asked after a command that writes the index has failed with
// failure. lock is empty when no other git process held the index, so the
// failure was something else; otherwise it is the lock's path, and held says
// whether it was still there after lockWait.
//
// The lock is usually gone by the time anyone looks -- the other git was
// brief, which is the point of waiting for it -- so a failure that names the
// lock file counts as having met it even when the file is no longer there.
func waitForIndex(dir string, failure error) (lock string, held bool) {
	out, err := run(dir, "rev-parse", "--git-path", "index.lock")
	if err != nil {
		return "", false
	}
	lock = strings.TrimSpace(out)
	if !filepath.IsAbs(lock) {
		lock = filepath.Join(dir, lock)
	}
	there := func() bool { _, serr := os.Lstat(lock); return serr == nil }
	if !there() && !strings.Contains(failure.Error(), "index.lock") {
		return "", false
	}
	for deadline := time.Now().Add(lockWait); there() && time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
	}
	return lock, there()
}

// indexHeld is what a command that could not get at the index says.
func indexHeld(lock string) error {
	return &gitError{"another git command is using this repository right now; try again in a moment. " +
		"If nothing is running, " + lock + " was left behind by one that stopped part way, and deleting it lets git go on."}
}

// unresolved refuses a commit while a merge has left a file in conflict that
// committing from the panel would settle with a side nobody chose.
//
// git refuses to commit while a merge has files in conflict, and "add --all"
// is what tells it they are resolved. A text file both sides changed is fine
// once its markers are gone: it has been sorted out and merely not added, and
// committing is how the panel adds it. The rest have no markers to look for,
// so there is no telling from here whether anyone chose: a binary file, which
// git leaves as this side's copy; a file one side deleted or only one side
// added; and a file whose merge attribute keeps git from writing markers. Each
// of those was committed as it happened to lie.
func unresolved(dir string, recs []statusRecord) error {
	type left struct{ path, why string }
	var (
		stuck []left
		both  []string // changed on both sides, which may hold markers
	)
	for _, rec := range recs {
		switch rec.code {
		case "UD", "DU":
			stuck = append(stuck, left{rec.path, "deleted on one side"})
		case "AU", "UA":
			stuck = append(stuck, left{rec.path, "added on one side"})
		case "UU", "AA":
			both = append(both, rec.path)
		}
		// DD is both sides deleting it, which is a choice already made.
	}
	if len(both) > 0 {
		attrs := mergeAttrs(dir, both)
		for _, p := range both {
			full := filepath.Join(dir, filepath.FromSlash(p))
			switch a := attrs[p]; {
			case isBinaryFile(full):
				stuck = append(stuck, left{p, "binary"})
			case a.noMarkers:
				stuck = append(stuck, left{p, "merged without markers"})
			case hasConflictMarkers(full, a.markerSize):
				stuck = append(stuck, left{p, ""})
			}
		}
	}
	if len(stuck) == 0 {
		return nil
	}
	var names []string
	markersOnly := true
	for _, s := range stuck[:min(len(stuck), 3)] {
		if s.why == "" {
			names = append(names, s.path)
			continue
		}
		markersOnly = false
		names = append(names, s.path+" ("+s.why+")")
	}
	for _, s := range stuck[min(len(stuck), 3):] {
		markersOnly = markersOnly && s.why == ""
	}
	list := strings.Join(names, ", ")
	if len(stuck) > 3 {
		list += fmt.Sprintf(" and %d more", len(stuck)-3)
	}
	if markersOnly {
		return &gitError{"still in conflict: " + list + " — resolve the <<<<<<< markers first, then commit"}
	}
	return &gitError{"still in conflict: " + list + " — a conflict with no markers to edit cannot be settled from here. " +
		"In a terminal, keep the side you want (git checkout --ours or --theirs, or git rm) and git add it, then commit"}
}

// mergeAttr is what a file's attributes say about how git merges it.
type mergeAttr struct {
	markerSize int  // how long git's conflict markers are in it
	noMarkers  bool // merged as binary, or not merged at all: no markers are written
}

// mergeAttrs reads the merge attributes of the files named. A
// conflict-marker-size attribute makes git write markers of that length, and
// they were not recognised as markers at all.
func mergeAttrs(dir string, paths []string) map[string]mergeAttr {
	attrs := make(map[string]mergeAttr, len(paths))
	for _, p := range paths {
		attrs[p] = mergeAttr{markerSize: 7}
	}
	args := append([]string{"check-attr", "-z", "conflict-marker-size", "merge", "--"}, paths...)
	out, err := run(dir, args...)
	if err != nil {
		return attrs
	}
	f := strings.Split(out, "\x00")
	for i := 0; i+2 < len(f); i += 3 {
		path, name, value := f[i], f[i+1], f[i+2]
		a, ok := attrs[path]
		if !ok {
			continue
		}
		switch name {
		case "conflict-marker-size":
			if n, err := strconv.Atoi(value); err == nil && n > 0 {
				a.markerSize = n
			}
		case "merge":
			a.noMarkers = value == "binary" || value == "unset"
		}
		attrs[path] = a
	}
	return attrs
}

// isBinaryFile applies looksBinary to the start of a file.
func isBinaryFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 8000)
	n, _ := io.ReadFull(f, buf)
	return looksBinary(buf[:n])
}

// hasConflictMarkers reports whether a file has a line git starts a conflict
// with, or ends one with, size characters long. It reads a line at a time,
// since a conflicted file can be large and is only wanted for the answer.
func hasConflictMarkers(path string, size int) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	start := []byte(strings.Repeat("<", size))
	end := []byte(strings.Repeat(">", size))
	isMarker := func(line, marker []byte) bool {
		rest, ok := bytes.CutPrefix(line, marker)
		return ok && (len(rest) == 0 || rest[0] == ' ' || rest[0] == '\n' || rest[0] == '\r')
	}
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadSlice('\n')
		if isMarker(line, start) || isMarker(line, end) {
			return true
		}
		// The rest of a line too long for the buffer is not the start of one.
		for err == bufio.ErrBufferFull {
			_, err = r.ReadSlice('\n')
		}
		if err != nil {
			return false
		}
	}
}

// errEmptyMessage is returned rather than letting git open an editor, which
// would hang with nowhere to type.
var errEmptyMessage = &gitError{"a commit message is required"}

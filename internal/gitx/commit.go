package gitx

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
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
	// git refuses to commit while a merge has files in conflict, and "add
	// --all" is what tells it they are resolved -- so the button committed a
	// merge with the conflict markers still in it. A file that has been sorted
	// out and merely not added is fine: committing is how the panel adds it.
	if left := conflicted(dir); len(left) > 0 {
		names := strings.Join(left[:min(len(left), 3)], ", ")
		if len(left) > 3 {
			names += fmt.Sprintf(" and %d more", len(left)-3)
		}
		return &gitError{"still in conflict: " + names + " — resolve the <<<<<<< markers first, then commit"}
	}
	if err := writingIndex(dir, "", "add", "--all"); err != nil {
		return err
	}
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

// conflicted names the files a merge left unresolved that still hold the
// markers git wrote into them.
func conflicted(dir string) []string {
	out, err := run(dir, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil
	}
	var names []string
	for _, name := range strings.Split(out, "\x00") {
		if name != "" && hasConflictMarkers(filepath.Join(dir, name)) {
			names = append(names, name)
		}
	}
	return names
}

// hasConflictMarkers reports whether a file has a line git starts a conflict
// with, or ends one with. It reads a line at a time, since a conflicted file
// can be large and is only wanted for the answer.
func hasConflictMarkers(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadSlice('\n')
		if bytes.HasPrefix(line, []byte("<<<<<<< ")) || bytes.HasPrefix(line, []byte(">>>>>>> ")) {
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

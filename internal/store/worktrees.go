package store

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// WorktreeProcess is what Flockdeck remembers, for as long as a pane keeps
// working in a git worktree, about the process it put there: enough for a
// later run to find that process again and end it, if this one never gets
// the chance to itself.
//
// Ending a pane's process is normally this run's own job, done the moment it
// is closed (see session.Session.Close) or, failing that, when the whole
// workspace is torn down on the way out (see workspace.Workspace.Close).
// Ended some other way instead -- the machine turned off under it, the
// process killed from outside, an update relaunching before the old run had
// finished closing everything, a bug in either of those two paths -- what is
// left behind is a process nothing in the new run's own state has ever heard
// of, free to go on holding a worktree's folder open until it is found some
// other way. This record is that other way: read back at the start of the
// next run, in workspace.ReapStaleWorktreeProcesses.
type WorktreeProcess struct {
	// PaneID names the pane the process belongs to, and the file this is kept
	// in. It is read from the file's own name rather than written into it, so
	// renaming the file -- which nothing here does -- could never disagree
	// with what it holds.
	PaneID string `json:"-"`
	PID    int    `json:"pid"`
	// Started is an opaque reading of when PID began -- session.Started's
	// answer at the moment this was recorded -- compared against, never
	// parsed: a live process answering to PID today whose own reading
	// disagrees is not the one that was put to work here, since the id has
	// been handed to something else since, and is left alone.
	Started uint64 `json:"started"`
	// Cwd is the worktree the process was put to work in, and Repo the
	// checkout it was cut from -- what lets the reaper tell a directory git
	// still lists as a worktree apart from one that is only a folder now.
	Cwd  string `json:"cwd"`
	Repo string `json:"repo"`
}

// worktreeSuffix marks a file in SessionsDir as one of these records rather
// than a generated settings file. SweepSessions goes by age alone for those,
// and must never mistake one of these for it: a pane can work in a worktree
// for days, and its record has to outlast it.
const worktreeSuffix = ".worktree.json"

func worktreePath(dir, paneID string) string {
	return filepath.Join(dir, paneID+worktreeSuffix)
}

// RecordWorktreeProcess notes that a pane's process is working in a git
// worktree, so that a run which never gets to end it cleanly leaves something
// the next one can still find it by.
func RecordWorktreeProcess(paneID string, p WorktreeProcess) error {
	dir, err := SessionsDir()
	if err != nil {
		return err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return WriteAtomic(worktreePath(dir, paneID), data)
}

// ForgetWorktreeProcess removes a pane's record, once its process has ended
// the ordinary way: closed with the pane, or with the run that started it. A
// pane that was never recorded -- one that never worked in a worktree -- has
// nothing to remove, which is not an error.
func ForgetWorktreeProcess(paneID string) error {
	dir, err := SessionsDir()
	if err != nil {
		return err
	}
	err = os.Remove(worktreePath(dir, paneID))
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// WorktreeProcesses reads every record left behind, for the reaper to check
// each one against. A file it cannot make sense of -- half-written, or from a
// version that shaped it differently -- is skipped rather than failing the
// whole read: one unreadable record must not hide the rest.
func WorktreeProcesses() ([]WorktreeProcess, error) {
	dir, err := SessionsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []WorktreeProcess
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, worktreeSuffix) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var p WorktreeProcess
		if json.Unmarshal(data, &p) != nil {
			continue
		}
		p.PaneID = strings.TrimSuffix(name, worktreeSuffix)
		out = append(out, p)
	}
	return out, nil
}

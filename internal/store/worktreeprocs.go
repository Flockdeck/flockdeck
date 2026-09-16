package store

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// WorktreeProc records a pane's process while it works in a git checkout, so a
// launch that comes after this one crashed, or was killed, or simply took too
// long to shut down, can tell a process it never started from one it did and
// is owed the chance to end.
//
// Nothing here says the checkout is a linked worktree rather than a project's
// main one -- that is asked of git, which knows, when a record is old enough
// to be swept; recording it here costs nothing extra for every pane, where
// asking git would have cost one process per pane started.
type WorktreeProc struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	Path    string    `json:"path"`
}

const worktreeProcsFile = "worktree-procs.json"

// worktreeProcsMu guards the read-modify-write around the file: several panes
// can start or close at once, a fan-out's dozen among them, and each has to
// see what the last one wrote.
var worktreeProcsMu sync.Mutex

func worktreeProcsPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, worktreeProcsFile), nil
}

// TrackWorktreeProc records that a pane's process is running in path, keyed by
// the pane's own id so a later Untrack or a restart of the same pane replaces
// rather than duplicates it.
func TrackWorktreeProc(id string, pid int, started time.Time, path string) error {
	worktreeProcsMu.Lock()
	defer worktreeProcsMu.Unlock()
	recs, err := loadWorktreeProcsLocked()
	if err != nil {
		return err
	}
	recs[id] = WorktreeProc{PID: pid, Started: started, Path: path}
	return saveWorktreeProcsLocked(recs)
}

// UntrackWorktreeProc removes the record for a pane whose process has been
// given the chance to end cleanly -- Close has already run, or is not going
// to be asked to because the pane never started -- so there is nothing left
// for a later launch to find and reap.
func UntrackWorktreeProc(id string) error {
	worktreeProcsMu.Lock()
	defer worktreeProcsMu.Unlock()
	recs, err := loadWorktreeProcsLocked()
	if err != nil {
		return err
	}
	if _, ok := recs[id]; !ok {
		return nil
	}
	delete(recs, id)
	return saveWorktreeProcsLocked(recs)
}

// LoadWorktreeProcs returns every record on file, keyed by the pane id that
// made it. A launch sweeping for stale processes reads this once, near the
// start of the run and before it has recorded any pane of its own: everything
// it finds here was left by a run before this one.
func LoadWorktreeProcs() (map[string]WorktreeProc, error) {
	worktreeProcsMu.Lock()
	defer worktreeProcsMu.Unlock()
	return loadWorktreeProcsLocked()
}

func loadWorktreeProcsLocked() (map[string]WorktreeProc, error) {
	path, err := worktreeProcsPath()
	if err != nil {
		return nil, err
	}
	data, err := readState(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]WorktreeProc{}, nil
		}
		return nil, err
	}
	var recs map[string]WorktreeProc
	// A file left over from a build that wrote it differently, or one caught
	// mid-write by something outside Flockdeck's own locking, is treated as
	// empty rather than failing every start and every close for as long as it
	// sits there.
	if json.Unmarshal(data, &recs) != nil || recs == nil {
		recs = map[string]WorktreeProc{}
	}
	return recs, nil
}

func saveWorktreeProcsLocked(recs map[string]WorktreeProc) error {
	path, err := worktreeProcsPath()
	if err != nil {
		return err
	}
	if len(recs) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}

// ProcessStartedAt reports when a process id started, for telling a process
// still running under a recorded id from a different one the system has since
// handed that id to. ok is false when that cannot be told -- on macOS, always
// -- and then a live id is taken at its word, as Instance.StillRunning does.
func ProcessStartedAt(pid int) (time.Time, bool) { return processStarted(pid) }

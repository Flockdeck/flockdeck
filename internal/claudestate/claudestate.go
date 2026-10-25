// Package claudestate reads the state Claude Code keeps about each of its own
// running sessions, in a small file per process under its config directory
// (sessions/<pid>.json). The file says whether the session is busy or idle and
// when that last changed, which is the agent's own word on it, written whether or
// not any lifecycle hook fired. Flockdeck's status for a pane is built from
// those hooks, which a turn started by a message from another session, or one
// that calls no tool, does not send; this is what it is checked against.
//
// The file is Claude Code's, not a Flockdeck interface: nothing here writes it,
// and a file that is missing, damaged or in a shape not understood is no answer.
package claudestate

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Entry is one session file.
type Entry struct {
	Pid       int
	SessionID string
	// Busy is the file's status: "busy" is true, "idle" false. A status of any
	// other word is not an Entry at all, since Known would be false.
	Busy bool
	// Updated is when the file says the status last changed.
	Updated time.Time
	// Started is when the file says the session's process started. It is zero
	// when the file does not say.
	Started time.Time
}

type file struct {
	Pid       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
	At        int64  `json:"statusUpdatedAt"`
	Started   int64  `json:"startedAt"`
}

// maxFile is the longest a session file is read as.
const maxFile = 64 << 10

// Dir is where Claude Code keeps its session files: CLAUDE_CONFIG_DIR's
// sessions folder, or ~/.claude/sessions. It is "" when neither can be found.
//
// It is Flockdeck's own environment that is asked. A Claude pane whose agent
// entry sets CLAUDE_CONFIG_DIR for itself (a second account) keeps its files
// elsewhere and is not found here; it stays on its hooks.
func Dir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude", "sessions")
}

// readFile reads one session file. The file is looked at before it is opened: a
// symbolic link, a pipe or a device is not read (opening a pipe would wait for
// a writer), and nothing over maxFile is.
func readFile(path string) (Entry, bool) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxFile {
		return Entry{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return Entry{}, false
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if err != nil || len(raw) > maxFile {
		return Entry{}, false
	}
	var v file
	if json.Unmarshal(raw, &v) != nil || v.Pid <= 0 || v.At <= 0 {
		return Entry{}, false
	}
	var busy bool
	switch strings.ToLower(v.Status) {
	case "busy":
		busy = true
	case "idle":
	default:
		return Entry{}, false
	}
	e := Entry{Pid: v.Pid, SessionID: v.SessionID, Busy: busy, Updated: time.UnixMilli(v.At)}
	if v.Started > 0 {
		e.Started = time.UnixMilli(v.Started)
	}
	return e, true
}

// ReadPid reads the file of one process, <pid>.json, and nothing else.
func ReadPid(dir string, pid int) (Entry, bool) {
	if dir == "" || pid <= 0 {
		return Entry{}, false
	}
	e, ok := readFile(filepath.Join(dir, strconv.Itoa(pid)+".json"))
	if !ok || e.Pid != pid {
		return Entry{}, false
	}
	return e, true
}

// Read returns every session file in dir that is usable: a pid, a status of busy
// or idle and a time. A file that is not valid, or says something else, is left out.
func Read(dir string) []Entry {
	if dir == "" {
		return nil
	}
	names, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil
	}
	var out []Entry
	for _, n := range names {
		if e, ok := readFile(n); ok {
			out = append(out, e)
		}
	}
	return out
}

// ByConversation picks the entry for a conversation id: the one with that
// sessionId, the most recently updated if more than one process has it. It is the
// fallback for a pane whose process id has no file.
func ByConversation(entries []Entry, sessionID string) (Entry, bool) {
	if sessionID == "" {
		return Entry{}, false
	}
	var best Entry
	found := false
	for _, e := range entries {
		if e.SessionID == sessionID && (!found || e.Updated.After(best.Updated)) {
			best, found = e, true
		}
	}
	return best, found
}

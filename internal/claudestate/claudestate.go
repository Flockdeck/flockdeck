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
	"os"
	"path/filepath"
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
}

type file struct {
	Pid       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
	At        int64  `json:"statusUpdatedAt"`
}

// Dir is where Claude Code keeps its session files: CLAUDE_CONFIG_DIR's
// sessions folder, or ~/.claude/sessions. It is "" when neither can be found.
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

// Read returns every session file in dir that has a pid, a status of busy or
// idle and a time. A file that is not valid, or says something else, is left out.
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
		raw, err := os.ReadFile(n)
		if err != nil || len(raw) > 64<<10 {
			continue
		}
		var f file
		if json.Unmarshal(raw, &f) != nil || f.Pid <= 0 || f.At <= 0 {
			continue
		}
		var busy bool
		switch strings.ToLower(f.Status) {
		case "busy":
			busy = true
		case "idle":
		default:
			continue
		}
		out = append(out, Entry{Pid: f.Pid, SessionID: f.SessionID, Busy: busy, Updated: time.UnixMilli(f.At)})
	}
	return out
}

// Find picks the entry for a pane: the one for its process, else the one for
// its conversation. ok is false when neither is there.
func Find(entries []Entry, pid int, sessionID string) (Entry, bool) {
	if pid > 0 {
		for _, e := range entries {
			if e.Pid == pid {
				return e, true
			}
		}
	}
	if sessionID != "" {
		var best Entry
		found := false
		for _, e := range entries {
			if e.SessionID == sessionID && (!found || e.Updated.After(best.Updated)) {
				best, found = e, true
			}
		}
		return best, found
	}
	return Entry{}, false
}

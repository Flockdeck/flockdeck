package route

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// LogName is the routing log, in Flockdeck's state directory.
//
// It is how the user, and later the spend view, can tell whether routing
// saves anything and how often its choices are overridden. It stays on this
// machine: nothing reads it but Flockdeck, on the user's behalf, and Settings
// clears it. It holds no task text and no prompt -- only the name of the rule
// that chose -- because the task is already in the user's own transcripts, and
// a log of prompts would be one more copy of them to leak.
const LogName = "routing.jsonl"

// logCap is how many decisions the log keeps. Older ones are dropped as new
// ones are written, so it never grows without end.
const logCap = 10_000

// LogEntry is one decision, as the log records it.
type LogEntry struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Project string    `json:"project,omitempty"`
	Pane    string    `json:"pane,omitempty"`
	// Agent is the agent the work ran on, as it always has been. BaselineAgent
	// is the agent it would have run on without routing, and is left empty
	// unless a rule moved the work to another agent -- so an old log line,
	// and every same-agent decision since, still reads as it always has.
	Agent         string `json:"agent,omitempty"`
	BaselineAgent string `json:"baselineAgent,omitempty"`
	Source        string `json:"source"`
	Rule          string `json:"rule,omitempty"`
	// Baseline is the model the work would have run without routing, and
	// Routed the model routing chose for it.
	Baseline string `json:"baseline"`
	Routed   string `json:"routed"`
	// Outcome is what became of the choice: "kept", or "overridden:<model>"
	// when the user chose another before it started.
	Outcome string `json:"outcome"`
}

// Outcomes a decision can have.
const (
	OutcomeKept       = "kept"
	OutcomeOverridden = "overridden:"
)

// logMu keeps two writers in this process -- two fan-outs started from two
// windows -- from each trimming the log and writing back a copy without the
// other's lines.
var logMu sync.Mutex

// AppendLog adds decisions to the log in dir.
func AppendLog(dir string, entries ...LogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	var add bytes.Buffer
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			return err
		}
		add.Write(line)
		add.WriteByte('\n')
	}
	logMu.Lock()
	defer logMu.Unlock()
	path := filepath.Join(dir, LogName)
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read the routing log: %w", err)
	}
	data := add.Bytes()
	if len(old) > 0 && old[len(old)-1] != '\n' {
		// The last line has no end: an editor that drops the final newline, or
		// a write cut short. Appended to as it stood, it and the first new
		// decision became one line that parsed as neither, and both were lost.
		data = append([]byte{'\n'}, data...)
	}
	if bytes.Count(old, []byte("\n"))+bytes.Count(data, []byte("\n")) <= logCap {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("write the routing log: %w", err)
		}
		_, err = f.Write(data)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("write the routing log: %w", err)
		}
		return nil
	}
	// data, not add: once the log is full every append comes this way, and
	// a last line without its newline would otherwise still swallow the
	// first new decision.
	lines := bytes.SplitAfter(append(old, data...), []byte("\n"))
	if n := len(lines); n > 0 && len(lines[n-1]) == 0 {
		lines = lines[:n-1]
	}
	if len(lines) > logCap {
		lines = lines[len(lines)-logCap:]
	}
	return replace(path, bytes.Join(lines, nil))
}

// replace writes a file whole, through store.WriteAtomic, so a trim
// interrupted halfway leaves the old log rather than half of one.
//
// It used to rename a temporary file into place itself, and on Windows a
// rename onto a file somebody has open -- a second instance reading the log,
// a virus scanner looking at it -- is refused with "Access is denied", which
// lost that fan-out's decisions. WriteAtomic waits that out, and queues saves
// of the one file from this process, as it does for every other file kept in
// the state directory; the 0600 is the same.
func replace(path string, data []byte) error {
	if err := store.WriteAtomic(path, data); err != nil {
		return fmt.Errorf("write the routing log: %w", err)
	}
	return nil
}

// ReadLog returns every decision in the log in dir, oldest first. A line that
// does not parse is passed over: the log is the user's to edit or truncate.
func ReadLog(dir string) ([]LogEntry, error) {
	logMu.Lock()
	defer logMu.Unlock()
	f, err := os.Open(filepath.Join(dir, LogName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []LogEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		var e LogEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// ClearLog deletes the log in dir.
func ClearLog(dir string) error {
	logMu.Lock()
	defer logMu.Unlock()
	if err := os.Remove(filepath.Join(dir, LogName)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

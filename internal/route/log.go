package route

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// LogName is the routing log, in Flockdeck's state directory.
//
// Nothing writes it any more: Fan out was the only thing that did, and its
// rows were the decisions a person could override. A log an earlier build
// wrote is still read back for Settings, which can also clear it. It holds no
// task text and no prompt, only the name of the rule that chose.
const LogName = "routing.jsonl"

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

// logMu keeps a read or a clear from overlapping another.
var logMu sync.Mutex

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

// RuleStat is one rule's record in the routing log: how often the log has it
// deciding, and how often what it chose was changed before the work started.
type RuleStat struct {
	Rule       string
	Kept       int
	Overridden int
}

// Total is how many logged decisions the rule made, kept or overridden.
func (s RuleStat) Total() int { return s.Kept + s.Overridden }

// OverrideRate is the share of the rule's decisions that were changed before
// the work started, or 0 for a rule the log has never seen decide.
func (s RuleStat) OverrideRate() float64 {
	if s.Total() == 0 {
		return 0
	}
	return float64(s.Overridden) / float64(s.Total())
}

// Stats summarises a routing log by rule: this is the evidence routing.jsonl
// is kept for -- the override rate that says which rules are worth
// hand-editing -- read back rather than left for a person to add up line by
// line. It only reports; nothing here changes a rule or a policy. A decision
// StrategyCost's fallback made (Source SourceFallback, Rule "") is not a
// named rule, so it is left out: there is nothing in agents.json to edit for
// it. Rules are returned in the order they are first seen, oldest first.
func Stats(entries []LogEntry) []RuleStat {
	at := map[string]int{}
	var out []RuleStat
	for _, e := range entries {
		if e.Rule == "" {
			continue
		}
		i, ok := at[e.Rule]
		if !ok {
			i = len(out)
			at[e.Rule] = i
			out = append(out, RuleStat{Rule: e.Rule})
		}
		if strings.HasPrefix(e.Outcome, OutcomeOverridden) {
			out[i].Overridden++
		} else {
			out[i].Kept++
		}
	}
	return out
}

// StatsByOverrideRate is Stats, ordered worst first -- the rule most often
// overridden is the one most worth a person's attention -- and stable for
// rules tied on rate, so the same log always reads the same way.
func StatsByOverrideRate(entries []LogEntry) []RuleStat {
	out := Stats(entries)
	sort.SliceStable(out, func(i, j int) bool { return out[i].OverrideRate() > out[j].OverrideRate() })
	return out
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

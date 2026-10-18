package route

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A log an earlier build wrote is read back, and cleared on request. Lines
// from before this build carry a "jev" object and a kind of "fanout"; both
// are read past without a word.
func TestAnOldLogIsReadBackAndCleared(t *testing.T) {
	dir := t.TempDir()
	old := strings.Join([]string{
		`{"at":"2026-01-02T03:04:05Z","kind":"fanout","agent":"claude","source":"rule","rule":"hard work","baseline":"sonnet","routed":"opus","outcome":"kept"}`,
		`{"at":"2026-01-02T03:04:06Z","kind":"fanout","agent":"claude","source":"fallback","baseline":"opus","routed":"haiku","outcome":"kept","jev":{"score":2.1,"confidence":0.8,"tier":"mid"}}`,
		"not json",
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, LogName), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLog(dir)
	if err != nil || len(got) != 2 || got[0].Rule != "hard work" || got[0].Routed != "opus" || got[1].Routed != "haiku" || got[1].Source != SourceFallback {
		t.Fatalf("read back %+v, %v", got, err)
	}
	if rs := Stats(got); len(rs) != 1 || rs[0].Rule != "hard work" || rs[0].Kept != 1 {
		t.Errorf("Stats = %+v", rs)
	}
	if err := ClearLog(dir); err != nil {
		t.Fatal(err)
	}
	if got, _ := ReadLog(dir); len(got) != 0 {
		t.Errorf("a cleared log still holds %d decisions", len(got))
	}
	if err := ClearLog(dir); err != nil {
		t.Errorf("clearing a log that is not there: %v", err)
	}
}

// Stats is the evidence the log is kept for: how often each rule's decision
// was kept versus overridden, so a person editing rules by hand has the
// override rate instead of counting lines themselves.
func TestStats(t *testing.T) {
	entries := []LogEntry{
		{Source: SourceRule, Rule: "hard work", Outcome: OutcomeKept},
		{Source: SourceRule, Rule: "hard work", Outcome: OutcomeOverridden + "sonnet"},
		{Source: SourceRule, Rule: "hard work", Outcome: OutcomeKept},
		{Source: SourceRule, Rule: "run the tests", Outcome: OutcomeKept},
		// A fallback decision names no rule, and is left out: there is
		// nothing in agents.json for it to say is worth editing.
		{Source: SourceFallback, Rule: "", Outcome: OutcomeKept},
	}
	got := Stats(entries)
	if len(got) != 2 {
		t.Fatalf("Stats = %+v, want two rules", got)
	}
	if got[0].Rule != "hard work" || got[0].Kept != 2 || got[0].Overridden != 1 || got[0].Total() != 3 {
		t.Errorf("hard work = %+v, want 2 kept, 1 overridden", got[0])
	}
	if rate := got[0].OverrideRate(); rate < 0.333 || rate > 0.334 {
		t.Errorf("hard work's override rate = %v, want ~1/3", rate)
	}
	if got[1].Rule != "run the tests" || got[1].OverrideRate() != 0 {
		t.Errorf("run the tests = %+v, want never overridden", got[1])
	}

	byRate := StatsByOverrideRate(entries)
	if byRate[0].Rule != "hard work" {
		t.Errorf("StatsByOverrideRate = %+v, want the most-overridden rule first", byRate)
	}
}

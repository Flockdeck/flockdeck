package route

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestTheLogKeepsDecisionsAndNoTask(t *testing.T) {
	dir := t.TempDir()
	e := LogEntry{At: time.Now(), Kind: KindFanout, Pane: "p1", Agent: "claude", Source: SourceRule,
		Rule: "run the tests", Baseline: "sonnet", Routed: "haiku", Outcome: OutcomeKept}
	if err := AppendLog(dir, e, e); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLog(dir)
	if err != nil || len(got) != 2 || got[1].Rule != "run the tests" || got[1].Routed != "haiku" {
		t.Fatalf("read back %+v, %v", got, err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, LogName))
	for _, field := range []string{`"task"`, `"prompt"`} {
		if strings.Contains(string(data), field) {
			t.Errorf("the log records %s:\n%s", field, data)
		}
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(filepath.Join(dir, LogName)); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("the log is %v, want 0600 (%v)", fi.Mode().Perm(), err)
		}
	}
	if err := ClearLog(dir); err != nil {
		t.Fatal(err)
	}
	if got, _ := ReadLog(dir); len(got) != 0 {
		t.Errorf("a cleared log still holds %d decisions", len(got))
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

func TestTheLogIsTrimmedToItsCap(t *testing.T) {
	dir := t.TempDir()
	batch := make([]LogEntry, 1000)
	for i := range 11 {
		for j := range batch {
			batch[j] = LogEntry{Kind: KindFanout, Rule: string(rune('a' + i)), Outcome: OutcomeKept}
		}
		if err := AppendLog(dir, batch...); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ReadLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != logCap {
		t.Fatalf("the log holds %d decisions, want %d", len(got), logCap)
	}
	if got[0].Rule != "b" || got[len(got)-1].Rule != "k" {
		t.Errorf("the log kept %q to %q, want the newest", got[0].Rule, got[len(got)-1].Rule)
	}
}

// A log whose last line lost its newline -- an editor that strips it, a write
// cut short -- is appended to on a line of its own, so neither decision is lost.
func TestALogWithoutAFinalNewlineKeepsBothDecisions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, LogName), []byte(`{"kind":"fanout","rule":"old","outcome":"kept"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AppendLog(dir, LogEntry{Kind: KindFanout, Rule: "new", Outcome: OutcomeKept}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLog(dir)
	if err != nil || len(got) != 2 || got[0].Rule != "old" || got[1].Rule != "new" {
		t.Errorf("read back %+v, %v; want the old decision and the new", got, err)
	}
}

// A full log whose last line lost its newline keeps both decisions too. Once
// the log reaches its cap every append trims it, which is a different path
// from the plain append, and the one a log in steady use always takes.
func TestAFullLogWithoutAFinalNewlineKeepsBothDecisions(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for range logCap - 1 {
		b.WriteString(`{"kind":"fanout","rule":"filler","outcome":"kept"}` + "\n")
	}
	b.WriteString(`{"kind":"fanout","rule":"old","outcome":"kept"}`)
	if err := os.WriteFile(filepath.Join(dir, LogName), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AppendLog(dir, LogEntry{Kind: KindFanout, Rule: "new", Outcome: OutcomeKept}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != logCap {
		t.Errorf("the log holds %d decisions, want %d", len(got), logCap)
	}
	if n := len(got); n < 2 || got[n-2].Rule != "old" || got[n-1].Rule != "new" {
		t.Errorf("the log ends %+v; want the old decision and then the new", got[max(0, len(got)-2):])
	}
}

// A line written before Jev existed has no jev field and parses as it always
// did; one with it round-trips; and only the latter counts as Jev-assisted.
func TestTheLogTakesOldLinesAndCarriesJevFields(t *testing.T) {
	dir := t.TempDir()
	old := `{"at":"2026-01-02T03:04:05Z","kind":"fanout","agent":"claude","source":"rule","rule":"hard work","baseline":"sonnet","routed":"opus","outcome":"kept"}` + "\n" +
		`{"at":"2026-01-02T03:04:06Z","kind":"fanout","agent":"claude","source":"fallback","baseline":"opus","routed":"haiku","outcome":"kept"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, LogName), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	note := &JevNote{Score: 2.1, Confidence: 0.8, Mechanical: 0.1, MultiFile: 0.3, Tier: "mid", Model: "jev-1.13.0"}
	if err := AppendLog(dir,
		LogEntry{At: time.Now(), Kind: KindFanout, Agent: "claude", Source: SourceFallback, Baseline: "opus", Routed: "sonnet", Outcome: OutcomeKept, Jev: note},
		LogEntry{At: time.Now(), Kind: KindFanout, Agent: "claude", Source: SourceFallback, Baseline: "opus", Routed: "sonnet", Outcome: OutcomeOverridden + "opus", Jev: note},
		LogEntry{At: time.Now(), Kind: KindFanout, Agent: "claude", Source: SourceFallback, Baseline: "opus", Routed: "haiku", Outcome: OutcomeOverridden + "sonnet"},
	); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLog(dir)
	if err != nil || len(got) != 5 {
		t.Fatalf("read back %d entries, %v", len(got), err)
	}
	if got[0].Jev != nil || got[1].Jev != nil || got[0].Rule != "hard work" || got[1].Routed != "haiku" {
		t.Errorf("old lines read as %+v, %+v", got[0], got[1])
	}
	if !reflect.DeepEqual(got[2].Jev, note) {
		t.Errorf("the note read back as %+v", got[2].Jev)
	}
	data, _ := os.ReadFile(filepath.Join(dir, LogName))
	if strings.Contains(string(data), `"jev":null`) {
		t.Errorf("a line with no classification says so with null:\n%s", data)
	}

	stats := FallbackStats(got)
	if len(stats) != 2 || stats[0].Rule != FallbackPlain || stats[0].Kept != 1 || stats[0].Overridden != 1 ||
		stats[1].Rule != FallbackJev || stats[1].Kept != 1 || stats[1].Overridden != 1 {
		t.Errorf("FallbackStats = %+v", stats)
	}
	// Rules are still counted by Stats alone, and a log with no fallback
	// decisions has no groups.
	if rs := Stats(got); len(rs) != 1 || rs[0].Rule != "hard work" {
		t.Errorf("Stats = %+v", rs)
	}
	if FallbackStats([]LogEntry{{Source: SourceRule, Rule: "x", Outcome: OutcomeKept}}) != nil {
		t.Error("a group with no decisions was listed")
	}
}

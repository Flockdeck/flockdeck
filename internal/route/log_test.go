package route

import (
	"os"
	"path/filepath"
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

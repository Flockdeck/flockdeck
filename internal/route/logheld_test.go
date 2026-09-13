package route

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestATrimWaitsForAReaderToLetGo covers the log being trimmed while something
// else has it open: a second instance reading it, or a virus scanner. On
// Windows a rename onto an open file is refused with "Access is denied", and
// the trim renamed its copy into place once, so the fan-out's decisions were
// lost. It now goes through the state directory's own writer, which waits a
// reader out. Elsewhere a rename onto an open file simply succeeds, so only a
// Windows run can fail this.
func TestATrimWaitsForAReaderToLetGo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, LogName)
	line, err := json.Marshal(LogEntry{Kind: "fanout", Source: "rule", Baseline: "a", Routed: "b", Outcome: OutcomeKept})
	if err != nil {
		t.Fatal(err)
	}
	var full bytes.Buffer
	for i := 0; i < logCap; i++ {
		full.Write(line)
		full.WriteByte('\n')
	}
	if err := os.WriteFile(path, full.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	held, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(150 * time.Millisecond)
		held.Close()
		close(released)
	}()
	t.Cleanup(func() { <-released })

	// The log is full, so this one trims it.
	if err := AppendLog(dir, LogEntry{Kind: "fanout", Source: "rule", Baseline: "a", Routed: "last", Outcome: OutcomeKept}); err != nil {
		t.Fatalf("a trim while the log was held open failed: %v", err)
	}
	entries, err := ReadLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != logCap || entries[len(entries)-1].Routed != "last" {
		t.Errorf("the log holds %d decisions ending %+v, want %d ending with the new one", len(entries), entries[len(entries)-1], logCap)
	}
}

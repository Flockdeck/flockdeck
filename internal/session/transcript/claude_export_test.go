package transcript

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
)

const exportFixtureID = "11111111-2222-3333-4444-555555555555"

// exportSpec points Claude's state directory at the fixture, so no test reads
// the real one.
func exportSpec(t *testing.T) agent.Spec {
	t.Helper()
	home, err := filepath.Abs(filepath.Join("testdata", "claude_export", "home"))
	if err != nil {
		t.Fatal(err)
	}
	return agent.Spec{ID: "claude", Exe: "claude", Env: []string{"CLAUDE_CONFIG_DIR=" + home}, Caps: agent.Caps{Transcript: true, Resume: true}}
}

func collect(t *testing.T, spec agent.Spec, id string) ([]ExportEvent, ExportStats, error) {
	t.Helper()
	ex, ok := ExporterFor(spec)
	if !ok {
		t.Fatal("Claude is not an Exporter")
	}
	var evs []ExportEvent
	stats, err := ex.Follow(spec, id).Poll(func(e ExportEvent) error { evs = append(evs, e); return nil })
	return evs, stats, err
}

func TestClaudeExportReplaysTheConversation(t *testing.T) {
	evs, stats, err := collect(t, exportSpec(t), exportFixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Skipped != 1 {
		t.Errorf("skipped %d entries, want the one line that is not JSON", stats.Skipped)
	}
	type row struct {
		kind ExportKind
		what string // text, or the tool
	}
	want := []row{
		{ExportPrompt, "Add a retry to the fetch client. The staging login is password=hunter2"},
		{ExportMessage, "I'll look at the client first."},
		{ExportToolCall, "Read"},
		{ExportToolResult, "Read"},
		{ExportToolCall, "Read"},
		{ExportToolResult, "Read"},
		{ExportToolCall, "Bash"},
		{ExportToolResult, "Bash"},
		{ExportMessage, "Stopped. Say go to retry."},
		{ExportPrompt, "go"},
		{ExportMessage, "Trying the change."},
		{ExportToolCall, "Edit"},
		{ExportToolResult, "Edit"},
		{ExportMessage, "Done: added a retry."},
	}
	if len(evs) != len(want) {
		for _, e := range evs {
			t.Logf("%+v", e)
		}
		t.Fatalf("%d events, want %d", len(evs), len(want))
	}
	for i, w := range want {
		e := evs[i]
		what := e.Text
		if e.Kind == ExportToolCall || e.Kind == ExportToolResult {
			what = e.Tool
		}
		if e.Kind != w.kind || what != w.what {
			t.Errorf("event %d = %+v, want %+v", i, e, w)
		}
	}
}

func TestClaudeExportKeepsTimesInOrder(t *testing.T) {
	evs, _, err := collect(t, exportSpec(t), exportFixtureID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(evs); i++ {
		if evs[i].Time.Before(evs[i-1].Time) {
			t.Errorf("event %d (%v) is before event %d (%v)", i, evs[i].Time, i-1, evs[i-1].Time)
		}
	}
}

func TestClaudeExportResults(t *testing.T) {
	evs, _, _ := collect(t, exportSpec(t), exportFixtureID)
	var results []ExportEvent
	for _, e := range evs {
		if e.Kind == ExportToolResult {
			results = append(results, e)
		}
	}
	// A structured result is written as compact JSON; a plain one as it is.
	if got := results[0].Output; got != `{"file":{"content":"package client","filePath":"src/client.go"},"type":"text"}` {
		t.Errorf("structured result = %s", got)
	}
	if results[0].ToolUseID != "toolu_1" || results[0].Tool != "Read" {
		t.Errorf("result is not tied to its call: %+v", results[0])
	}
	if results[1].Output != "API_KEY=hunter2" {
		t.Errorf("text result = %q", results[1].Output)
	}
	// An error says so, and one the user stopped says that too.
	if e := results[2]; !e.IsError || !e.Interrupted {
		t.Errorf("interrupted result = %+v", e)
	}
	if e := results[3]; e.IsError || e.Interrupted {
		t.Errorf("a result that worked is flagged: %+v", e)
	}
	// Tool inputs are decoded.
	for _, e := range evs {
		if e.Kind == ExportToolCall && e.Tool == "Bash" {
			in, _ := e.Input.(map[string]any)
			if in["command"] != "go test ./..." {
				t.Errorf("input = %#v", e.Input)
			}
		}
	}
}

func TestClaudeExportWithoutAConversation(t *testing.T) {
	spec := exportSpec(t)
	if _, _, err := collect(t, spec, "99999999-0000-0000-0000-000000000000"); !errors.Is(err, ErrNoTranscript) {
		t.Errorf("an unknown conversation gave %v, want ErrNoTranscript", err)
	}
	if _, _, err := collect(t, spec, `..\`+exportFixtureID); !errors.Is(err, ErrNoTranscript) {
		t.Errorf("a path gave %v, want ErrNoTranscript", err)
	}
}

func TestOnlyAgentsWithAReaderCanBeExported(t *testing.T) {
	if _, ok := ExporterFor(agent.Spec{ID: "claude", Exe: "claude", Caps: agent.Caps{Transcript: true}}); !ok {
		t.Error("Claude cannot be exported")
	}
	for name, spec := range map[string]agent.Spec{
		"no transcript": {ID: "claude", Exe: "claude"},
		"unknown agent": {ID: "other", Exe: "other", Caps: agent.Caps{Transcript: true}},
		"an API agent":  {ID: "gpt", Runner: agent.RunnerAPI, Caps: agent.Caps{Transcript: true}},
	} {
		if _, ok := ExporterFor(spec); ok {
			t.Errorf("%s: can be exported", name)
		}
	}
}

func TestClaudeExportStopsWhenAskedTo(t *testing.T) {
	spec := exportSpec(t)
	stop := errors.New("enough")
	n := 0
	_, err := Claude{}.Follow(spec, exportFixtureID).Poll(func(ExportEvent) error {
		n++
		if n == 3 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) || n != 3 {
		t.Errorf("err = %v after %d events", err, n)
	}
}

// A conversation still being written is followed: each look gives what is new,
// a line cut off by the look is given whole at the next, and what comes out is
// what one look at the finished file gives, however the file was cut up.
func TestFollowingAGrowingConversationGivesWhatOneLookAtTheEndDoes(t *testing.T) {
	whole, err := os.ReadFile(filepath.Join("testdata", "claude_export", "home", "projects", "C--work-shop", exportFixtureID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	want, _, _ := collect(t, exportSpec(t), exportFixtureID)

	for _, step := range []int{13, 64, 1000, len(whole)} {
		home := t.TempDir()
		spec := agent.Spec{ID: "claude", Exe: "claude", Env: []string{"CLAUDE_CONFIG_DIR=" + home}, Caps: agent.Caps{Transcript: true}}
		dir := filepath.Join(home, "projects", "C--work-shop")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, exportFixtureID+".jsonl")
		f := Claude{}.Follow(spec, exportFixtureID)
		// Nothing is stored yet.
		if _, err := f.Poll(func(ExportEvent) error { return nil }); !errors.Is(err, ErrNoTranscript) {
			t.Fatalf("step %d: before the file exists: %v", step, err)
		}
		var got []ExportEvent
		var skipped int
		for at := 0; at < len(whole); at += step {
			end := min(at+step, len(whole))
			fh, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			fh.Write(whole[at:end])
			fh.Close()
			stats, err := f.Poll(func(e ExportEvent) error { got = append(got, e); return nil })
			if err != nil {
				t.Fatal(err)
			}
			skipped += stats.Skipped
		}
		if len(got) != len(want) || skipped != 1 {
			t.Errorf("step %d: %d events and %d skipped, want %d and 1", step, len(got), skipped, len(want))
			continue
		}
		for i := range want {
			if !reflect.DeepEqual(got[i], want[i]) {
				t.Errorf("step %d: event %d = %+v, want %+v", step, i, got[i], want[i])
				break
			}
		}
	}
}

// An entry too large to hold is stepped over, and what follows it is read.
func TestFollowingStepsOverAnEntryTooLargeToHold(t *testing.T) {
	home := t.TempDir()
	spec := agent.Spec{ID: "claude", Exe: "claude", Env: []string{"CLAUDE_CONFIG_DIR=" + home}, Caps: agent.Caps{Transcript: true}}
	huge := `{"type":"user","timestamp":"2026-10-01T09:00:00Z","message":{"role":"user","content":"` + strings.Repeat("x", maxTranscriptEntry+10) + `"}}`
	writeTranscript(t, filepath.Join(home, "projects", "C--work"), exportFixtureID,
		huge,
		`{"type":"user","timestamp":"2026-10-01T09:00:01Z","message":{"role":"user","content":"after"}}`)
	evs, stats, err := collect(t, spec, exportFixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Text != "after" || stats.Skipped != 1 {
		t.Errorf("events %+v, skipped %d", evs, stats.Skipped)
	}
}

func TestCwdOfAStoredConversation(t *testing.T) {
	home := t.TempDir()
	spec := agent.Spec{ID: "claude", Exe: "claude", Env: []string{"CLAUDE_CONFIG_DIR=" + home}, Caps: agent.Caps{Transcript: true}}
	writeTranscript(t, filepath.Join(home, "projects", "C--work"), exportFixtureID,
		`{"type":"user","cwd":"/work/shop","timestamp":"2026-10-01T09:00:00Z","message":{"role":"user","content":"hi"}}`)
	ex, _ := ExporterFor(spec)
	if got := ex.Cwd(spec, exportFixtureID); got != "/work/shop" {
		t.Errorf("cwd = %q", got)
	}
	if got := ex.Cwd(spec, "no-such"); got != "" {
		t.Errorf("cwd of nothing = %q", got)
	}
}

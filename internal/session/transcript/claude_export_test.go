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
	// The text the agent was shown, not the tool's own structured result, which
	// can hold what a transcript has no use for.
	if got := results[0].Output; got != "package client" {
		t.Errorf("result = %s", got)
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

// A conversation that comes to be shorter than what was read of it is read again
// from the start, and one that moves is looked for again.
func TestFollowingAReplacedConversationStartsOver(t *testing.T) {
	home := t.TempDir()
	spec := agent.Spec{ID: "claude", Exe: "claude", Env: []string{"CLAUDE_CONFIG_DIR=" + home}, Caps: agent.Caps{Transcript: true}}
	one := `{"type":"user","timestamp":"2026-10-01T09:00:00Z","message":{"role":"user","content":"first"}}`
	two := `{"type":"user","timestamp":"2026-10-01T09:00:01Z","message":{"role":"user","content":"second"}}`
	path := writeTranscript(t, filepath.Join(home, "projects", "C--work"), exportFixtureID, one, two)
	f := Claude{}.Follow(spec, exportFixtureID)
	var got []string
	poll := func() error {
		_, err := f.Poll(func(e ExportEvent) error { got = append(got, e.Text); return nil })
		return err
	}
	if err := poll(); err != nil || len(got) != 2 {
		t.Fatalf("first look: %v %v", got, err)
	}
	short := `{"type":"user","timestamp":"2026-10-01T09:00:00Z","message":{"role":"user","content":"x"}}`
	if err := os.WriteFile(path, []byte(short+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := poll(); !errors.Is(err, ErrReplaced) {
		t.Fatalf("after the replacement: %v", err)
	}
	got = nil
	if err := poll(); err != nil || len(got) != 1 || got[0] != "x" {
		t.Errorf("after starting over: %v %v", got, err)
	}
	// Gone, and then somewhere else.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := poll(); !errors.Is(err, ErrNoTranscript) {
		t.Fatalf("with the file gone: %v", err)
	}
	moved := writeTranscript(t, filepath.Join(home, "projects", "C--elsewhere"), exportFixtureID, short, `{"type":"user","timestamp":"2026-10-01T09:00:02Z","message":{"role":"user","content":"y"}}`)
	_ = moved
	if err := poll(); !errors.Is(err, ErrReplaced) {
		t.Fatalf("after it moved: %v", err)
	}
	got = nil
	if err := poll(); err != nil || len(got) != 2 {
		t.Errorf("after starting over from the copy: %v %v", got, err)
	}
}

// An entry with no time to put it at is not written, and is counted.
func TestAnEntryWithNoTimeIsCountedAsSkipped(t *testing.T) {
	home := t.TempDir()
	spec := agent.Spec{ID: "claude", Exe: "claude", Env: []string{"CLAUDE_CONFIG_DIR=" + home}, Caps: agent.Caps{Transcript: true}}
	writeTranscript(t, filepath.Join(home, "projects", "C--work"), exportFixtureID,
		`{"type":"user","message":{"role":"user","content":"no time"}}`,
		`{"type":"user","timestamp":"2026-10-01T09:00:00Z","message":{"role":"user","content":"timed"}}`)
	evs, stats, err := collect(t, spec, exportFixtureID)
	if err != nil || len(evs) != 1 || evs[0].Text != "timed" || stats.Skipped != 1 {
		t.Errorf("events %+v, skipped %d, %v", evs, stats.Skipped, err)
	}
}

// An event the caller could not take is not lost, and not given twice: it, and
// the rest of what had been read, come at the next look.
func TestFollowingLosesNothingWhenTheCallerFails(t *testing.T) {
	want, _, _ := collect(t, exportSpec(t), exportFixtureID)
	for failAt := 0; failAt < len(want); failAt++ {
		f := Claude{}.Follow(exportSpec(t), exportFixtureID)
		var got []ExportEvent
		n := 0
		fail := errors.New("disk")
		take := func(e ExportEvent) error {
			if n == failAt {
				n++
				return fail
			}
			n++
			got = append(got, e)
			return nil
		}
		if _, err := f.Poll(take); !errors.Is(err, fail) {
			t.Fatalf("failAt %d: %v", failAt, err)
		}
		if _, err := f.Poll(func(e ExportEvent) error { got = append(got, e); return nil }); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("failAt %d: %d events, want %d", failAt, len(got), len(want))
		}
	}
}

// What a tool's structured result holds that the agent was not shown is not in
// the transcript.
func TestToolResultsAreTheTextTheAgentWasShown(t *testing.T) {
	home := t.TempDir()
	spec := agent.Spec{ID: "claude", Exe: "claude", Env: []string{"CLAUDE_CONFIG_DIR=" + home}, Caps: agent.Caps{Transcript: true}}
	writeTranscript(t, filepath.Join(home, "projects", "C--work"), exportFixtureID,
		`{"type":"assistant","timestamp":"2026-10-01T09:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{}}]}}`,
		`{"type":"user","timestamp":"2026-10-01T09:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"edited"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAABASE64AAAA"}}]}]},"toolUseResult":{"originalFile":"WHOLE FILE","base64":"AAAABASE64AAAA"}}`)
	evs, _, err := collect(t, spec, exportFixtureID)
	if err != nil || len(evs) != 2 {
		t.Fatal(evs, err)
	}
	if got := evs[1].Output; got != "edited" {
		t.Errorf("output = %q", got)
	}
}

// A file that is there but cannot be opened is not a file that is not there: the
// first is tried again, and nothing is concluded from it.
func TestAnOpenThatFailsIsAReadErrorNotAMissingFile(t *testing.T) {
	home := t.TempDir()
	spec := agent.Spec{ID: "claude", Exe: "claude", Env: []string{"CLAUDE_CONFIG_DIR=" + home}, Caps: agent.Caps{Transcript: true}}
	writeTranscript(t, filepath.Join(home, "projects", "C--work"), exportFixtureID,
		`{"type":"user","timestamp":"2026-10-01T09:00:00Z","message":{"role":"user","content":"hi"}}`)
	old := openTranscript
	openTranscript = func(string) (*os.File, error) { return nil, os.ErrPermission }
	t.Cleanup(func() { openTranscript = old })
	_, _, err := collect(t, spec, exportFixtureID)
	if !errors.Is(err, ErrRead) || errors.Is(err, ErrNoTranscript) {
		t.Errorf("err = %v, want ErrRead", err)
	}
	openTranscript = old
	if evs, _, err := collect(t, spec, exportFixtureID); err != nil || len(evs) != 1 {
		t.Errorf("after the lock: %v, %v", evs, err)
	}
}

const modelFixtureID = "22222222-3333-4444-5555-666666666666"

func modelSpec(t *testing.T) agent.Spec {
	t.Helper()
	home, err := filepath.Abs(filepath.Join("testdata", "claude_model", "home"))
	if err != nil {
		t.Fatal(err)
	}
	return agent.Spec{ID: "claude", Exe: "claude", Env: []string{"CLAUDE_CONFIG_DIR=" + home}, Caps: agent.Caps{Transcript: true}}
}

// An assistant turn carries the model its stored entry names, and only that: a
// prompt and a tool result carry none, a placeholder or an empty or missing
// model is no model, and a switch mid-conversation is followed turn by turn.
func TestClaudeExportCarriesTheModelOfEachAssistantTurn(t *testing.T) {
	evs, _, err := collect(t, modelSpec(t), modelFixtureID)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		Kind  ExportKind
		Text  string
		Tool  string
		Model string
	}
	var got []row
	for _, e := range evs {
		got = append(got, row{e.Kind, e.Text, e.Tool, e.Model})
	}
	want := []row{
		{ExportPrompt, "Add a retry.", "", ""},
		{ExportMessage, "Looking.", "", "claude-opus-5-5"},
		{ExportToolCall, "", "Read", "claude-opus-5-5"},
		{ExportToolResult, "", "Read", ""},
		{ExportPrompt, "Now switch and continue.", "", ""},
		{ExportToolCall, "", "Bash", "claude-sonnet-5-5"},
		{ExportToolResult, "", "Bash", ""},
		{ExportMessage, "API Error: please run /login", "", ""},
		{ExportMessage, "No model recorded.", "", ""},
		{ExportMessage, "Empty model.", "", ""},
		{ExportMessage, "Done.", "", "claude-sonnet-5-5"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("events = %+v\nwant %+v", got, want)
	}
}

// A conversation stored before the model was read, or by a Claude Code that
// did not write it, has events with none.
func TestClaudeExportWithoutStoredModelsHasNone(t *testing.T) {
	evs, _, err := collect(t, exportSpec(t), exportFixtureID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Model != "" {
			t.Errorf("%+v has a model the conversation does not name", e)
		}
	}
}

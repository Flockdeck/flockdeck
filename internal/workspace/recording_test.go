package workspace

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/record/schemacheck"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// isolatedRecordings isolates the config directory and then checks that it
// worked before anything is written: on Windows an environment override does
// not reach everything, and a recording test that leaked would put
// transcripts in the developer's real state directory.
func isolatedRecordings(t *testing.T) {
	t.Helper()
	isolateConfig(t)
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	tmp, err := filepath.Abs(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if rel, err := filepath.Rel(tmp, dir); err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("the state directory %s is not under the temp directory %s: refusing to run, this would write to a real one", dir, tmp)
	}
}

func recordingFiles(t *testing.T) []string {
	t.Helper()
	dir, _ := store.Dir()
	files, _ := filepath.Glob(filepath.Join(dir, "recordings", "*", "*.jsonl"))
	return files
}

func readTranscript(t *testing.T, path string) []record.Entry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []record.Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var e record.Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("not JSON: %v: %s", err, sc.Text())
		}
		out = append(out, e)
	}
	return out
}

func TestRecordingIsOffUntilTurnedOn(t *testing.T) {
	isolatedRecordings(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := agentPaneIn(t, ws, root, "quiet")
	if ws.PaneRecording(p.ID) {
		t.Fatal("a new pane is recording")
	}
	ws.handleHook(hooks.Event{SessionID: p.ID, Event: "UserPromptSubmit", Prompt: "hello", Launch: p.launch,
		Detail: &hooks.Detail{Prompt: "hello"}})
	if files := recordingFiles(t); len(files) != 0 {
		t.Errorf("a pane that was never asked to record wrote %v", files)
	}
}

func TestRecordingWritesTheInteractionFromHookEvents(t *testing.T) {
	isolatedRecordings(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := agentPaneIn(t, ws, root, "work")
	p.Agent, p.Model = "claude", "opus"

	if found, err := ws.SetPaneRecording(p.ID, true); !found || err != nil {
		t.Fatalf("SetPaneRecording = %v, %v", found, err)
	}
	send := func(ev hooks.Event) {
		ev.SessionID, ev.Launch = p.ID, p.launch
		ws.handleHook(ev)
	}
	send(hooks.Event{Event: "UserPromptSubmit", Detail: &hooks.Detail{Prompt: "run the tests with key sk-ant-api03-abcdefghijklmnop"}})
	send(hooks.Event{Event: "PreToolUse", Tool: "Bash", Detail: &hooks.Detail{ToolUseID: "t1", Input: json.RawMessage(`{"command":"go test ./..."}`)}})
	send(hooks.Event{Event: "PermissionRequest", Tool: "Bash", Detail: &hooks.Detail{Input: json.RawMessage(`{"command":"go test ./..."}`)}})
	send(hooks.Event{Event: "PostToolUse", Tool: "Bash", Detail: &hooks.Detail{ToolUseID: "t1", Result: "ok  all passed"}})
	send(hooks.Event{Event: "PostToolUseFailure", Tool: "Bash", Detail: &hooks.Detail{ToolUseID: "t2", Error: "exit status 1"}})
	send(hooks.Event{Event: "Stop", Detail: &hooks.Detail{Message: "All green."}})

	// A Stop with nothing to say records no message.
	send(hooks.Event{Event: "Stop"})

	if found, err := ws.SetPaneRecording(p.ID, false); !found || err != nil {
		t.Fatalf("stop = %v, %v", found, err)
	}
	send(hooks.Event{Event: "UserPromptSubmit", Detail: &hooks.Detail{Prompt: "after the stop"}})

	files := recordingFiles(t)
	if len(files) != 1 {
		t.Fatalf("want one session file, got %v", files)
	}
	es := readTranscript(t, files[0])
	// Status lines come from the session's own transitions and are checked
	// apart from the rest.
	var got []string
	status := 0
	for _, e := range es {
		if e.Type == record.TypeStatus {
			status++
			if e.Status == "" {
				t.Errorf("status line without a status: %+v", e)
			}
			continue
		}
		got = append(got, e.Type)
	}
	// The permission outcome is written when the tool's result settles it,
	// ahead of the result's own line.
	want := []string{record.TypeStarted, record.TypePrompt, record.TypeToolCall, record.TypePermission, record.TypeOutcome, record.TypeToolResult, record.TypeToolResult, record.TypeAssistant, record.TypeStopped}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("types = %v\nwant    %v", got, want)
	}
	if status == 0 {
		t.Error("no status change was recorded")
	}
	if es[0].Agent != "claude" || es[0].Model != "opus" || es[0].PaneName == "" || es[0].Project != filepath.Base(root) || es[0].Pane != p.ID {
		t.Errorf("the first line does not say whose it is: %+v", es[0])
	}
	raw, _ := os.ReadFile(files[0])
	// What the workspace really wrote, status lines included, matches the
	// published schema.
	schema, err := schemacheck.Load("../../docs/recording-line.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if errs := schema.Validate([]byte(line)); len(errs) != 0 {
			t.Errorf("line does not match the schema: %v\n%s", errs, line)
		}
	}
	if strings.Contains(string(raw), "sk-ant-api03") || strings.Contains(string(raw), "after the stop") {
		t.Errorf("a secret, or something said after the stop, is in the file:\n%s", raw)
	}
}

func TestOnlyAnAgentPaneCanRecord(t *testing.T) {
	isolatedRecordings(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	tab := ws.NewTab(session.KindShell, root, "shell")
	id := tab.Tree.Panes()[0]
	if found, _ := ws.SetPaneRecording(id, true); found {
		t.Error("a shell pane was set recording")
	}
	if found, _ := ws.SetPaneRecording("no-such-pane", true); found {
		t.Error("a pane that is not there was found")
	}
	if len(recordingFiles(t)) != 0 {
		t.Error("a refused recording left a file")
	}
}

func TestClosingARecordingPaneEndsItsTranscript(t *testing.T) {
	isolatedRecordings(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := agentPaneIn(t, ws, root, "short-lived")
	ws.SetPaneRecording(p.ID, true)
	ws.ClosePaneByID(p.ID)
	files := recordingFiles(t)
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	es := readTranscript(t, files[0])
	if last := es[len(es)-1]; last.Type != record.TypeStopped {
		t.Errorf("the transcript ends with %s, not a stop", last.Type)
	}
}

func TestRecordingSurvivesRestartMoveAndRestore(t *testing.T) {
	isolatedRecordings(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := agentPaneIn(t, ws, root, "kept")
	plain := agentPaneIn(t, ws, root, "plain")
	ws.SetPaneRecording(p.ID, true)

	if !ws.RestartPaneByID(p.ID) {
		t.Fatal("restart did not find the pane")
	}
	if !ws.PaneRecording(p.ID) {
		t.Error("restart turned recording off")
	}
	other := ws.NewTab(session.KindShell, root, "two")
	if err := ws.MovePaneToTab(p.ID, other.ID); err != nil {
		t.Fatalf("move: %v", err)
	}
	if !ws.PaneRecording(p.ID) {
		t.Error("moving the pane to another tab turned recording off")
	}

	if err := ws.SaveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}
	ws.Close()
	restored := newTestWorkspace(t, root)
	if ok, err := restored.Restore(); err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	if !restored.PaneRecording(p.ID) {
		t.Error("recording was lost across a restore")
	}
	if restored.PaneRecording(plain.ID) {
		t.Error("a pane that was not recording came back recording")
	}
	// A restored pane records again, in a session of its own, from its first event.
	// Written through the recorder directly, as handleHook does: a pane whose
	// agent is not installed here (CI) has no session to hear a hook event.
	rp := restored.Pane(p.ID)
	restored.mu.Lock()
	meta := restored.recMetaLocked(rp)
	restored.mu.Unlock()
	restored.rec.Record(meta, record.Entry{Type: record.TypePrompt, Text: "back again"})
	if n := len(recordingFiles(t)); n != 2 {
		t.Errorf("want a second session file after the restore, got %d files", n)
	}
}

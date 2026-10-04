package workspace

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/record/schemacheck"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
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

// claudeHome points Claude Code's state directory at a temporary one, so the
// stored conversations these tests make are the only ones there are.
func claudeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	return home
}

// storeConversation appends lines to the file Claude Code would keep a
// conversation in.
func storeConversation(t *testing.T, home, id string, lines ...string) {
	t.Helper()
	dir := filepath.Join(home, "projects", "C--work-shop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, id+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}

const (
	promptLine = `{"type":"user","cwd":"/work/shop","timestamp":"2026-10-01T09:00:00Z","message":{"role":"user","content":"run the tests with key sk-ant-api03-abcdefghijklmnop"}}`
	callLine   = `{"type":"assistant","timestamp":"2026-10-01T09:00:01Z","message":{"role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"Running them."},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}`
	resultLine = `{"type":"user","timestamp":"2026-10-01T09:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok  all passed"}]},"toolUseResult":"ok  all passed"}`
	sayLine    = `{"type":"assistant","timestamp":"2026-10-01T09:00:03Z","message":{"role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"All green."}]}}`
	lateLine   = `{"type":"user","timestamp":"2026-10-01T09:05:00Z","message":{"role":"user","content":"after the stop"}}`
)

// syncRecording has a recording pane's transcript brought up to date with its
// stored conversation, and waits for that to be done.
func syncRecording(ws *Workspace, id string) {
	ws.kickRecording(id, true)
	ws.recAct.wait()
}

// startRecording turns a pane's recording on and waits for what was stored so
// far to be written.
func startRecording(t *testing.T, ws *Workspace, id string) {
	t.Helper()
	if found, err := ws.SetPaneRecording(id, true); !found || err != nil {
		t.Fatalf("SetPaneRecording = %v, %v", found, err)
	}
	ws.recAct.wait()
}

// recordingPane is a pane running Claude.
func recordingPane(t *testing.T, ws *Workspace, root, title string) *Pane {
	t.Helper()
	p := agentPaneIn(t, ws, root, title)
	ws.mu.Lock()
	p.Agent, p.Model = "claude", "opus"
	ws.mu.Unlock()
	return p
}

func recordingFiles(t *testing.T) []string {
	t.Helper()
	dir, _ := store.Dir()
	files, _ := filepath.Glob(filepath.Join(dir, "recordings", "*", "*.jsonl"))
	return files
}

func exportFiles(t *testing.T) []string {
	t.Helper()
	dir, _ := store.Dir()
	files, _ := filepath.Glob(filepath.Join(dir, "recordings", "*", "exports", "*.jsonl"))
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
	home := claudeHome(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := recordingPane(t, ws, root, "quiet")
	if ws.PaneRecording(p.ID) {
		t.Fatal("a new pane is recording")
	}
	storeConversation(t, home, p.ID, promptLine, sayLine)
	syncRecording(ws, p.ID)
	if files := recordingFiles(t); len(files) != 0 {
		t.Errorf("a pane that was never asked to record wrote %v", files)
	}
}

// Recording is the stored conversation, from its beginning, followed as it
// grows: what was said before it was turned on is in it, and what is said after
// it is turned off is not.
func TestRecordingFollowsTheStoredConversation(t *testing.T) {
	isolatedRecordings(t)
	home := claudeHome(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := recordingPane(t, ws, root, "work")
	storeConversation(t, home, p.ID, promptLine, callLine)

	startRecording(t, ws, p.ID)
	// Already there when it was turned on.
	files := recordingFiles(t)
	if len(files) != 1 {
		t.Fatalf("want one transcript, got %v", files)
	}
	if es := readTranscript(t, files[0]); len(es) != 4 {
		t.Fatalf("the conversation so far is %d lines, want 4", len(es))
	}

	storeConversation(t, home, p.ID, resultLine, sayLine)
	syncRecording(ws, p.ID)
	if found, err := ws.SetPaneRecording(p.ID, false); !found || err != nil {
		t.Fatalf("stop = %v, %v", found, err)
	}
	ws.recAct.wait()
	storeConversation(t, home, p.ID, lateLine)
	syncRecording(ws, p.ID)

	es := readTranscript(t, files[0])
	var got []string
	for _, e := range es {
		got = append(got, e.Type)
	}
	want := []string{record.TypeStarted, record.TypePrompt, record.TypeAssistant, record.TypeToolCall, record.TypeToolResult, record.TypeAssistant, record.TypeStopped}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("types = %v\nwant    %v", got, want)
	}
	if es[0].Agent != "claude" || es[0].Model != "" || es[0].Project != "shop" || es[0].Conversation != p.ID {
		t.Errorf("the first line does not say whose it is: %+v", es[0])
	}
	raw, _ := os.ReadFile(files[0])
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

// A recording and an export of the same conversation are the same bytes.
func TestExportMatchesTheRecording(t *testing.T) {
	isolatedRecordings(t)
	home := claudeHome(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := recordingPane(t, ws, root, "work")
	storeConversation(t, home, p.ID, promptLine, callLine)

	// Exported with recording never on.
	res, err := ws.ExportTranscript(p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := exportFiles(t); len(got) != 1 || got[0] != res.Path {
		t.Fatalf("export files = %v, result %s", got, res.Path)
	}
	if len(recordingFiles(t)) != 0 {
		t.Error("an export made a recording")
	}

	startRecording(t, ws, p.ID)
	storeConversation(t, home, p.ID, resultLine, sayLine)
	syncRecording(ws, p.ID)
	ws.SetPaneRecording(p.ID, false)
	ws.recAct.wait()
	// The pane is not what it was, which is not in the lines.
	ws.mu.Lock()
	p.Name, p.Model = "renamed", "sonnet"
	ws.mu.Unlock()
	res, err = ws.ExportTranscript(p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	files := recordingFiles(t)
	if len(files) != 1 {
		t.Fatalf("recordings = %v", files)
	}
	rec, _ := os.ReadFile(files[0])
	exp, _ := os.ReadFile(res.Path)
	if string(rec) != string(exp) {
		t.Errorf("the recording and the export differ\nrecorded:\n%s\nexported:\n%s", rec, exp)
	}
	// The model is the one the stored conversation says produced each turn, on
	// the agent's lines only, and not the pane's (now "sonnet").
	var assistantLines, toolCalls int
	for _, l := range strings.Split(strings.TrimSpace(string(exp)), "\n") {
		var e struct{ Type, Model string }
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatal(err)
		}
		switch e.Type {
		case "assistant_message", "tool_call":
			assistantLines++
			if e.Type == "tool_call" {
				toolCalls++
			}
			if e.Model != "claude-opus-5-5" {
				t.Errorf("a %s line has model %q: %s", e.Type, e.Model, l)
			}
		default:
			if e.Model != "" {
				t.Errorf("a %s line has model %q", e.Type, e.Model)
			}
		}
	}
	if assistantLines != 3 || toolCalls != 1 {
		t.Errorf("assistant lines = %d, tool calls = %d, want 3 and 1", assistantLines, toolCalls)
	}
}

func TestAnAgentThatStoresNothingCannotRecordOrExport(t *testing.T) {
	isolatedRecordings(t)
	claudeHome(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := recordingPane(t, ws, root, "other")
	ws.mu.Lock()
	p.Agent = "codex"
	ws.mu.Unlock()
	if name, ok := ws.PaneTranscriptSupported(p.ID); ok || name == "" {
		t.Errorf("PaneTranscriptSupported = %q, %v", name, ok)
	}
	_, err := ws.ExportTranscript(p.ID, "")
	if err == nil || !strings.Contains(err.Error(), "stores no conversation") {
		t.Fatalf("export said %v", err)
	}
	// Recording is refused, so the pane does not show as recording.
	found, err := ws.SetPaneRecording(p.ID, true)
	if !found || err == nil || !strings.Contains(err.Error(), "nothing to record") {
		t.Fatalf("recording = %v, %v", found, err)
	}
	if ws.PaneRecording(p.ID) {
		t.Error("a pane with nothing to record shows as recording")
	}
	// One that came back recording, from a layout or a spawn, is not.
	ws.mu.Lock()
	p.Recording = true
	ws.mu.Unlock()
	syncRecording(ws, p.ID)
	if ws.PaneRecording(p.ID) {
		t.Error("a pane with nothing to record went on showing as recording")
	}
	if n := len(recordingFiles(t)) + len(exportFiles(t)); n != 0 {
		t.Errorf("%d files were written for an agent with nothing stored", n)
	}
}

func TestExportBeforeAnythingIsStored(t *testing.T) {
	isolatedRecordings(t)
	claudeHome(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := recordingPane(t, ws, root, "new")
	if _, err := ws.ExportTranscript(p.ID, ""); err == nil || !strings.Contains(err.Error(), "no conversation for this pane yet") {
		t.Errorf("export said %v", err)
	}
	if len(exportFiles(t)) != 0 {
		t.Error("an export of nothing left a file")
	}
}

func TestExportNeverGoesIntoTheProject(t *testing.T) {
	isolatedRecordings(t)
	home := claudeHome(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := recordingPane(t, ws, root, "work")
	storeConversation(t, home, p.ID, promptLine)
	if _, err := ws.ExportTranscript(p.ID, filepath.Join(root, "transcript.jsonl")); err == nil {
		t.Error("an export was written into the project")
	}
	if _, err := os.Stat(filepath.Join(root, "transcript.jsonl")); err == nil {
		t.Error("a file is in the project")
	}
}

// After /clear the agent goes on in a conversation of its own: the transcript
// of the one before ends, and the new one has a transcript of its own.
func TestRecordingFollowsTheAgentIntoANewConversation(t *testing.T) {
	isolatedRecordings(t)
	home := claudeHome(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := recordingPane(t, ws, root, "clears")
	storeConversation(t, home, p.ID, promptLine, sayLine)
	startRecording(t, ws, p.ID)

	const next = "99999999-8888-7777-6666-555555555555"
	ws.mu.Lock()
	p.Conversation = next
	ws.mu.Unlock()
	storeConversation(t, home, next, lateLine)
	syncRecording(ws, p.ID)
	ws.SetPaneRecording(p.ID, false)
	ws.recAct.wait()

	files := recordingFiles(t)
	if len(files) != 2 {
		t.Fatalf("want a transcript for each conversation, got %v", files)
	}
	for _, f := range files {
		es := readTranscript(t, f)
		if es[0].Type != record.TypeStarted || es[len(es)-1].Type != record.TypeStopped {
			t.Errorf("%s is not whole: %v", f, es)
		}
		for _, e := range es {
			if e.Conversation != es[0].Conversation {
				t.Errorf("%s mixes conversations", f)
			}
		}
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
		t.Error("a pane that was not there was found")
	}
	if _, err := ws.ExportTranscript(id, ""); err == nil {
		t.Error("a shell pane was exported")
	}
	if len(recordingFiles(t)) != 0 {
		t.Error("a refused recording left a file")
	}
}

func TestClosingARecordingPaneEndsItsTranscript(t *testing.T) {
	isolatedRecordings(t)
	home := claudeHome(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := recordingPane(t, ws, root, "short-lived")
	storeConversation(t, home, p.ID, promptLine)
	startRecording(t, ws, p.ID)
	storeConversation(t, home, p.ID, sayLine) // said since the last look
	ws.ClosePaneByID(p.ID)
	ws.recAct.wait()
	files := recordingFiles(t)
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	es := readTranscript(t, files[0])
	if last := es[len(es)-1]; last.Type != record.TypeStopped {
		t.Errorf("the transcript ends with %s, not a stop", last.Type)
	}
	if got := es[len(es)-2]; got.Type != record.TypeAssistant {
		t.Errorf("what was said just before the pane closed is %s, want it kept", got.Type)
	}
}

func TestRecordingSurvivesRestartMoveAndRestore(t *testing.T) {
	isolatedRecordings(t)
	home := claudeHome(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := recordingPane(t, ws, root, "kept")
	plain := agentPaneIn(t, ws, root, "plain")
	storeConversation(t, home, p.ID, promptLine, sayLine)
	startRecording(t, ws, p.ID)

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
	// A restored pane catches up from the stored conversation, into the file
	// the conversation has: the same one, not another.
	storeConversation(t, home, p.ID, lateLine)
	syncRecording(restored, p.ID)
	files := recordingFiles(t)
	if len(files) != 1 {
		t.Fatalf("want the one transcript after the restore, got %v", files)
	}
	if es := readTranscript(t, files[0]); es[len(es)-1].Text != "after the stop" {
		t.Errorf("the restored pane's transcript did not catch up: %+v", es[len(es)-1])
	}
}

// A conversation already past the cap when recording is turned on is cut there,
// the same as an export of it, and the user is told why recording ended.
func TestRecordingAConversationAlreadyOverTheCap(t *testing.T) {
	isolatedRecordings(t)
	home := claudeHome(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.recMax = 16 << 10
	ws.rec.SetMaxFileBytes(ws.recMax)
	p := recordingPane(t, ws, root, "long")
	line := `{"type":"assistant","cwd":"/work/shop","timestamp":"2026-10-01T09:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":"` + strings.Repeat("word ", 400) + `"}]}}`
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, line)
	}
	storeConversation(t, home, p.ID, lines...)
	var whys []string
	var mu sync.Mutex
	ws.SetRecordingEndedHook(func(_, why string) { mu.Lock(); whys = append(whys, why); mu.Unlock() })

	startRecording(t, ws, p.ID) // waits for the look, and for the ending it starts
	mu.Lock()
	got := append([]string(nil), whys...)
	mu.Unlock()
	if len(got) != 1 || !strings.Contains(got[0], "already longer") {
		t.Fatalf("recording ended with %q", got)
	}
	if ws.PaneRecording(p.ID) {
		t.Error("the pane still shows as recording")
	}
	files := recordingFiles(t)
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	es := readTranscript(t, files[0])
	if last := es[len(es)-1]; last.Type != record.TypeTruncated {
		t.Errorf("the transcript ends with %s", last.Type)
	}
	// An export of the same conversation is cut in the same place.
	res, err := ws.ExportTranscript(p.ID, "")
	if err != nil || !res.Full {
		t.Fatalf("export = %+v, %v", res, err)
	}
	rec, _ := os.ReadFile(files[0])
	exp, _ := os.ReadFile(res.Path)
	if string(rec) != string(exp) {
		t.Error("the cut recording and the cut export differ")
	}
}

// A recording does not go on looking at a conversation that has gone quiet, and
// looks again when the agent reports something.
func TestRecordingStopsLookingWhenTheConversationIsQuiet(t *testing.T) {
	isolatedRecordings(t)
	home := claudeHome(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.recSettle = time.Millisecond
	p := recordingPane(t, ws, root, "quiet")
	storeConversation(t, home, p.ID, promptLine)
	startRecording(t, ws, p.ID)

	r := ws.recorder(p.ID, false)
	state := func() (looks, idle int, timer bool) {
		r.ctl.Lock()
		idle = r.idle
		r.ctl.Unlock()
		r.work.Lock()
		defer r.work.Unlock()
		return r.looks, idle, true
	}
	// The looks run on timers of a millisecond or so: wait for them to give up,
	// which is when the idle count passes the limit.
	deadline := time.Now().Add(20 * time.Second)
	for {
		ws.recAct.wait()
		if _, idle, _ := state(); idle > recIdleLooks {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the recording never stopped looking")
		}
		time.Sleep(time.Millisecond)
	}
	// One look that found the conversation, and the idle ones that followed.
	settled, _, _ := state()
	if settled != recIdleLooks+2 {
		t.Errorf("%d looks, want %d", settled, recIdleLooks+2)
	}
	storeConversation(t, home, p.ID, sayLine)
	time.Sleep(50 * time.Millisecond) // long enough for a look that should not happen
	ws.recAct.wait()
	if again, _, _ := state(); again != settled {
		t.Errorf("it went on looking after it had stopped: %d looks", again)
	}
	ws.kickRecording(p.ID, true) // the agent reported something
	ws.recAct.wait()
	if es := readTranscript(t, recordingFiles(t)[0]); es[len(es)-1].Type != record.TypeAssistant {
		t.Errorf("an event did not bring the transcript up to date: %v", es[len(es)-1].Type)
	}
}

// A look that comes after the workspace has closed writes nothing, and a pane
// that is not recording has no recorder made for it.
func TestNoRecorderIsMadeAfterCloseOrForAPaneNotRecording(t *testing.T) {
	isolatedRecordings(t)
	home := claudeHome(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := recordingPane(t, ws, root, "late")
	storeConversation(t, home, p.ID, promptLine, sayLine)
	ws.kickRecording(p.ID, true) // not recording
	ws.recAct.wait()
	if ws.recorder(p.ID, false) != nil {
		t.Error("a recorder was made for a pane that is not recording")
	}
	startRecording(t, ws, p.ID)
	ws.SetPaneRecording(p.ID, false)
	ws.recAct.wait()
	files := recordingFiles(t)
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	finished, _ := os.ReadFile(files[0])

	ws.Close()
	ws.kickRecording(p.ID, true)
	ws.recAct.wait()
	if ws.recorder(p.ID, true) != nil {
		t.Error("a recorder was made after Close")
	}
	if now, _ := os.ReadFile(files[0]); string(now) != string(finished) {
		t.Error("a finished transcript was changed after the workspace closed")
	}
}

// A conversation is one file: a second pane in it is refused.
func TestTwoPanesCannotRecordOneConversation(t *testing.T) {
	isolatedRecordings(t)
	home := claudeHome(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	a := recordingPane(t, ws, root, "a")
	b := recordingPane(t, ws, root, "b")
	ws.mu.Lock()
	b.Conversation = a.ID // a concurrent resume of the same conversation
	ws.mu.Unlock()
	storeConversation(t, home, a.ID, promptLine, sayLine)
	startRecording(t, ws, a.ID)
	found, err := ws.SetPaneRecording(b.ID, true)
	if !found || err == nil || !strings.Contains(err.Error(), "already recording") {
		t.Fatalf("second pane: %v, %v", found, err)
	}
	if ws.PaneRecording(b.ID) {
		t.Error("the refused pane shows as recording")
	}
	if es := readTranscript(t, recordingFiles(t)[0]); len(es) != 3 {
		t.Errorf("the transcript has %d lines, want 3", len(es))
	}
	// Once the first stops, the second may.
	ws.SetPaneRecording(a.ID, false)
	ws.recAct.wait()
	if found, err := ws.SetPaneRecording(b.ID, true); !found || err != nil {
		t.Errorf("after the first stopped: %v, %v", found, err)
	}
}

// What is stored being replaced by something shorter is written again from the
// start, and a conversation that is not stored yet is found when it is.
func TestRecordingFollowsAReplacedConversation(t *testing.T) {
	isolatedRecordings(t)
	home := claudeHome(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := recordingPane(t, ws, root, "replaced")
	startRecording(t, ws, p.ID) // nothing stored yet
	if n := len(recordingFiles(t)); n != 0 {
		t.Fatalf("a transcript of nothing: %d files", n)
	}
	storeConversation(t, home, p.ID, promptLine, callLine, resultLine, sayLine)
	syncRecording(ws, p.ID)
	files := recordingFiles(t)
	if len(files) != 1 || len(readTranscript(t, files[0])) != 6 {
		t.Fatalf("files = %v", files)
	}

	short := `{"type":"user","cwd":"/work/shop","timestamp":"2026-10-01T09:00:00Z","message":{"role":"user","content":"hi"}}`
	if err := os.WriteFile(filepath.Join(home, "projects", "C--work-shop", p.ID+".jsonl"), []byte(short+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	syncRecording(ws, p.ID)
	es := readTranscript(t, files[0])
	if len(es) != 2 || es[1].Text != "hi" {
		t.Errorf("the transcript was not written again from the replacement: %+v", es)
	}
}

// Turning recording off while the agent is still reporting events leaves no
// recorder behind, no replacement file, and a finished transcript.
func TestTurningRecordingOffWhileEventsArriveLeavesNothingBehind(t *testing.T) {
	isolatedRecordings(t)
	home := claudeHome(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := recordingPane(t, ws, root, "busy")
	storeConversation(t, home, p.ID, promptLine, callLine, resultLine, sayLine)

	for i := 0; i < 15; i++ {
		startRecording(t, ws, p.ID)
		stop := make(chan struct{})
		var wg sync.WaitGroup
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
						ws.kickRecording(p.ID, true) // the agent's events
					}
				}
			}()
		}
		ws.SetPaneRecording(p.ID, false)
		ws.recAct.wait()
		close(stop)
		wg.Wait()
		ws.recAct.wait()

		ws.recMu.Lock()
		left := len(ws.recorders)
		ws.recMu.Unlock()
		if left != 0 {
			t.Fatalf("round %d: %d recorders left behind", i, left)
		}
		if ws.rec.Active(p.ID) {
			t.Fatalf("round %d: a transcript file is still open", i)
		}
		if news, _ := filepath.Glob(filepath.Join(filepath.Dir(recordingFiles(t)[0]), "*.new")); len(news) != 0 {
			t.Fatalf("round %d: left behind %v", i, news)
		}
		es := readTranscript(t, recordingFiles(t)[0])
		if len(es) != 7 || es[len(es)-1].Type != record.TypeStopped {
			t.Fatalf("round %d: the transcript is %d lines ending %s", i, len(es), es[len(es)-1].Type)
		}
	}
}

// failingFollower makes the follower it wraps fail to read, once told to, in the
// way a locked file does.
type failingFollower struct {
	transcript.Follower
	fail *atomic.Pointer[error]
}

func (f failingFollower) Poll(yield func(transcript.ExportEvent) error) (transcript.ExportStats, error) {
	if e := f.fail.Load(); e != nil {
		return transcript.ExportStats{}, *e
	}
	return f.Follower.Poll(yield)
}

func failingWorkspace(t *testing.T, ws *Workspace) *atomic.Pointer[error] {
	var fail atomic.Pointer[error]
	ws.wrapFollower = func(f transcript.Follower) transcript.Follower { return failingFollower{f, &fail} }
	return &fail
}

func failWith(fail *atomic.Pointer[error], err error) { fail.Store(&err) }

// A recording whose conversation cannot be read when it is stopped is not given
// a closing line over what it could not read, and the user is told.
func TestStoppingARecordingThatCannotReadLeavesItUnfinishedAndSaysSo(t *testing.T) {
	for name, cause := range map[string]error{
		"locked": fmt.Errorf("%w: sharing violation", transcript.ErrRead),
		"gone":   transcript.ErrNoTranscript,
	} {
		t.Run(name, func(t *testing.T) {
			isolatedRecordings(t)
			home := claudeHome(t)
			root := t.TempDir()
			ws := newTestWorkspace(t, root)
			fail := failingWorkspace(t, ws)
			var notes []string
			var mu sync.Mutex
			ws.SetRecordingNoticeHook(func(_, text string) { mu.Lock(); notes = append(notes, text); mu.Unlock() })
			p := recordingPane(t, ws, root, "locked")
			storeConversation(t, home, p.ID, promptLine, sayLine)
			startRecording(t, ws, p.ID)
			storeConversation(t, home, p.ID, resultLine, lateLine) // stored since the last look
			failWith(fail, cause)
			ws.SetPaneRecording(p.ID, false)
			ws.recAct.wait()

			es := readTranscript(t, recordingFiles(t)[0])
			if last := es[len(es)-1]; last.Type == record.TypeStopped {
				t.Errorf("a transcript with gaps was given a closing line (%d lines)", len(es))
			}
			mu.Lock()
			defer mu.Unlock()
			if len(notes) != 1 || !strings.Contains(notes[0], "left unfinished") {
				t.Errorf("notices = %q", notes)
			}
			if ws.rec.Active(p.ID) {
				t.Error("the unfinished file is still open")
			}
		})
	}
}

// Events do not bring a failing recording's next look forward, so a lock that
// lasts a few seconds is waited out and not given up on.
func TestEventsDoNotHurryAFailingRecording(t *testing.T) {
	isolatedRecordings(t)
	home := claudeHome(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.recSettle = 200 * time.Millisecond
	fail := failingWorkspace(t, ws)
	p := recordingPane(t, ws, root, "locked")
	storeConversation(t, home, p.ID, promptLine)
	failWith(fail, fmt.Errorf("%w: locked", transcript.ErrRead))
	startRecording(t, ws, p.ID)
	for i := 0; i < 500; i++ {
		ws.kickRecording(p.ID, true) // the agent's events, at once
	}
	ws.recAct.wait()
	r := ws.recorder(p.ID, false)
	r.work.Lock()
	looks, fails := r.looks, r.readFails
	r.work.Unlock()
	if looks > 3 || fails > 3 {
		t.Errorf("%d looks and %d failures from 500 events", looks, fails)
	}
	// And once the lock is gone, it records.
	fail.Store(nil)
	deadline := time.Now().Add(20 * time.Second)
	for {
		ws.recAct.wait()
		if files := recordingFiles(t); len(files) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("it never recovered")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Togglers, events and a size cap racing leave one recorder's worth of lines at
// most, no file for an empty conversation, and nothing open.
func TestConcurrentTogglesLeaveACleanTranscript(t *testing.T) {
	isolatedRecordings(t)
	home := claudeHome(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := recordingPane(t, ws, root, "toggled")
	storeConversation(t, home, p.ID, promptLine, callLine, resultLine, sayLine)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				ws.SetPaneRecording(p.ID, (i+g)%2 == 0)
				ws.kickRecording(p.ID, true)
			}
		}(g)
	}
	wg.Wait()
	ws.SetPaneRecording(p.ID, false)
	ws.recAct.wait()
	ws.SetPaneRecording(p.ID, false)
	ws.recAct.wait()

	ws.recMu.Lock()
	left := len(ws.recorders)
	ws.recMu.Unlock()
	if left != 0 || ws.rec.Active(p.ID) {
		t.Fatalf("%d recorders left, open=%v", left, ws.rec.Active(p.ID))
	}
	for _, f := range recordingFiles(t) {
		if strings.HasSuffix(f, "-.jsonl") {
			t.Errorf("a transcript for no conversation: %s", f)
		}
		es := readTranscript(t, f)
		for i, e := range es {
			if e.Seq != int64(i+1) {
				t.Fatalf("%s: line %d has seq %d: lines were repeated or interleaved", f, i+1, e.Seq)
			}
		}
	}
}

// Turning recording off and straight on again is not lost: the pane that shows as
// recording has a live recorder, and writes when its agent says more.
func TestAFastOffThenOnKeepsRecording(t *testing.T) {
	isolatedRecordings(t)
	home := claudeHome(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.recSettle = time.Millisecond
	p := recordingPane(t, ws, root, "flip")
	storeConversation(t, home, p.ID, promptLine, callLine)
	startRecording(t, ws, p.ID)
	for i := 0; i < 60; i++ {
		ws.SetPaneRecording(p.ID, false)
		ws.SetPaneRecording(p.ID, true)
		deadline := time.Now().Add(10 * time.Second)
		for {
			ws.recAct.wait()
			r := ws.recorder(p.ID, false)
			live := false
			if r != nil {
				r.ctl.Lock()
				live = !r.stopped
				r.ctl.Unlock()
			}
			if live {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("round %d: the pane shows as recording and has no live recorder", i)
			}
			time.Sleep(time.Millisecond)
		}
	}
	// And it writes: what is stored now is in the file.
	storeConversation(t, home, p.ID, resultLine, sayLine)
	ws.kickRecording(p.ID, true)
	ws.recAct.wait()
	ws.SetPaneRecording(p.ID, false)
	ws.recAct.wait()
	es := readTranscript(t, recordingFiles(t)[0])
	if len(es) != 7 || es[len(es)-1].Type != record.TypeStopped {
		t.Errorf("the transcript is %d lines ending %s", len(es), es[len(es)-1].Type)
	}
}

// A pane that takes over a conversation while the pane that had it is being
// closed waits for it to be finished, and does not write into its open file.
func TestAPaneTakingAConversationDuringAStopDoesNotDuplicateLines(t *testing.T) {
	isolatedRecordings(t)
	home := claudeHome(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	a := recordingPane(t, ws, root, "a")
	b := recordingPane(t, ws, root, "b")
	ws.mu.Lock()
	b.Conversation = a.ID
	ws.mu.Unlock()
	storeConversation(t, home, a.ID, promptLine, callLine, resultLine, sayLine)
	startRecording(t, ws, a.ID)
	for i := 0; i < 20; i++ {
		ws.SetPaneRecording(a.ID, false) // being stopped...
		if found, err := ws.SetPaneRecording(b.ID, true); !found || err != nil {
			t.Fatalf("round %d: the other pane was refused: %v, %v", i, found, err)
		}
		ws.recAct.wait()
		ws.SetPaneRecording(b.ID, false)
		ws.recAct.wait()
		if found, err := ws.SetPaneRecording(a.ID, true); !found || err != nil {
			t.Fatalf("round %d: %v, %v", i, found, err)
		}
		ws.recAct.wait()
	}
	ws.SetPaneRecording(a.ID, false)
	ws.recAct.wait()
	for _, f := range recordingFiles(t) {
		es := readTranscript(t, f)
		if len(es) != 7 {
			t.Fatalf("%s has %d lines, want 7", f, len(es))
		}
		for i, e := range es {
			if e.Seq != int64(i+1) {
				t.Fatalf("%s: line %d has seq %d", f, i+1, e.Seq)
			}
		}
	}
	if news, _ := filepath.Glob(filepath.Join(filepath.Dir(recordingFiles(t)[0]), "*.new")); len(news) != 0 {
		t.Errorf("left behind: %v", news)
	}
}

// Closing the workspace while a stop is on its way does not wait for itself.
func TestCloseDoesNotWaitOnAStopItHasStopped(t *testing.T) {
	isolatedRecordings(t)
	home := claudeHome(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	gate := make(chan struct{})
	ws.stopGate = gate
	p := recordingPane(t, ws, root, "closing")
	storeConversation(t, home, p.ID, promptLine)
	startRecording(t, ws, p.ID)
	ws.endRecordingAsync(p.ID, "ended") // reaches the gate, holding at the start of its stop
	for deadline := time.Now().Add(10 * time.Second); ws.gateHits.Load() == 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the stop never reached the gate")
		}
	}
	closed := make(chan struct{})
	go func() { ws.Close(); close(closed) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		ws.recMu.Lock()
		marked := ws.recClosed
		ws.recMu.Unlock()
		if marked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Close never began")
		}
		time.Sleep(time.Millisecond)
	}
	close(gate)
	select {
	case <-closed:
	case <-time.After(20 * time.Second):
		t.Fatal("Close did not return: it waited on a stop that waited on it")
	}
}

// The file to show is the live recording's if the pane is recording, else its
// newest export, else there is none; and it is one of Flockdeck's own.
func TestTranscriptFileIsTheRecordingElseTheExport(t *testing.T) {
	isolatedRecordings(t)
	home := claudeHome(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := recordingPane(t, ws, root, "shown")
	if _, err := ws.TranscriptFile(p.ID); !errors.Is(err, ErrNoTranscriptFile) {
		t.Fatalf("nothing made yet: %v", err)
	}
	storeConversation(t, home, p.ID, promptLine, sayLine)
	res, err := ws.ExportTranscript(p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ws.TranscriptFile(p.ID)
	if want, _ := filepath.EvalSymlinks(res.Path); err != nil || got != want {
		t.Fatalf("export: %s, %v; want %s", got, err, want)
	}
	startRecording(t, ws, p.ID)
	got, err = ws.TranscriptFile(p.ID)
	files := recordingFiles(t)
	if want, _ := filepath.EvalSymlinks(files[0]); err != nil || got != want {
		t.Fatalf("recording: %s, %v; want %s", got, err, want)
	}
	if _, err := ws.TranscriptFile("no-such-pane"); err == nil {
		t.Error("a pane that is not there had a transcript")
	}
}

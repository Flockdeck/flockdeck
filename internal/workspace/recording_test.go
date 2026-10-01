package workspace

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	callLine   = `{"type":"assistant","timestamp":"2026-10-01T09:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"Running them."},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}`
	resultLine = `{"type":"user","timestamp":"2026-10-01T09:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok  all passed"}]},"toolUseResult":"ok  all passed"}`
	sayLine    = `{"type":"assistant","timestamp":"2026-10-01T09:00:03Z","message":{"role":"assistant","content":[{"type":"text","text":"All green."}]}}`
	lateLine   = `{"type":"user","timestamp":"2026-10-01T09:05:00Z","message":{"role":"user","content":"after the stop"}}`
)

// syncRecording has a recording pane's transcript brought up to date with its
// stored conversation, and waits for that to be done.
func syncRecording(ws *Workspace, id string) {
	ws.kickRecording(id, true)
	ws.recWG.Wait()
}

// startRecording turns a pane's recording on and waits for what was stored so
// far to be written.
func startRecording(t *testing.T, ws *Workspace, id string) {
	t.Helper()
	if found, err := ws.SetPaneRecording(id, true); !found || err != nil {
		t.Fatalf("SetPaneRecording = %v, %v", found, err)
	}
	ws.recWG.Wait()
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
	if es[0].Agent != "claude" || es[0].Model != "" || es[0].PaneName != "" || es[0].Project != "shop" || es[0].Pane != p.ID || es[0].Conversation != p.ID {
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
	p := recordingPane(t, ws, root, "long")
	big := strings.Repeat("word ", 5000)
	line := `{"type":"assistant","cwd":"/work/shop","timestamp":"2026-10-01T09:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":"` + big + `"}]}}`
	var lines []string
	for i := 0; i < 700; i++ {
		lines = append(lines, line)
	}
	storeConversation(t, home, p.ID, lines...)
	ended := make(chan string, 1)
	ws.SetRecordingEndedHook(func(_, why string) { ended <- why })

	startRecording(t, ws, p.ID)
	select {
	case why := <-ended:
		if !strings.Contains(why, "already longer") {
			t.Errorf("recording ended with %q", why)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("recording was not ended by the cap")
	}
	ws.recWG.Wait()
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
	old := recSettle
	recSettle = 5 * time.Millisecond
	t.Cleanup(func() { recSettle = old })
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := recordingPane(t, ws, root, "quiet")
	storeConversation(t, home, p.ID, promptLine)
	startRecording(t, ws, p.ID)

	looks := func() int {
		r := ws.recorder(p.ID)
		r.work.Lock()
		defer r.work.Unlock()
		return r.looks
	}
	time.Sleep(500 * time.Millisecond) // well past 5+10+20+40 ms
	settled := looks()
	if settled > recIdleLooks+3 {
		t.Errorf("%d looks at a quiet conversation", settled)
	}
	storeConversation(t, home, p.ID, sayLine)
	time.Sleep(200 * time.Millisecond)
	if looks() != settled {
		t.Error("it went on looking after it had stopped")
	}
	ws.kickRecording(p.ID, true) // the agent reported something
	ws.recWG.Wait()
	if es := readTranscript(t, recordingFiles(t)[0]); es[len(es)-1].Type != record.TypeAssistant {
		t.Errorf("an event did not bring the transcript up to date: %v", es[len(es)-1].Type)
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

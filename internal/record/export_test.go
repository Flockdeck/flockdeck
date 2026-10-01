package record

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

const fixtureConversation = "11111111-2222-3333-4444-555555555555"

var exportMeta = Meta{Pane: fixtureConversation, PaneName: "shop", Project: "shop", ProjectRoot: "/work/shop", Agent: "claude", Model: "opus", Conversation: fixtureConversation}

// claudeAt is Claude Code's agent, with its state directory at home and nowhere
// else, so no test reads the real one.
func claudeAt(t *testing.T, home string) (agent.Spec, transcript.Exporter) {
	t.Helper()
	spec := agent.Spec{ID: "claude", Exe: "claude", Env: []string{"CLAUDE_CONFIG_DIR=" + home}, Caps: agent.Caps{Transcript: true}}
	ex, ok := transcript.ExporterFor(spec)
	if !ok {
		t.Fatal("no exporter for Claude")
	}
	return spec, ex
}

// fixtureHome is the Claude Code state directory in the transcript package's
// test data.
func fixtureHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.Abs("../session/transcript/testdata/claude_export/home")
	if err != nil {
		t.Fatal(err)
	}
	return home
}

func fixtureFollower(t *testing.T, id string) transcript.Follower {
	t.Helper()
	spec, ex := claudeAt(t, fixtureHome(t))
	return ex.Follow(spec, id)
}

func exportToTemp(t *testing.T, opts ExportOptions) (ExportResult, string) {
	t.Helper()
	dir := t.TempDir()
	res, err := Export(func() (string, error) { return dir, nil }, exportMeta, fixtureFollower(t, fixtureConversation), opts)
	if err != nil {
		t.Fatal(err)
	}
	return res, dir
}

func TestExportIsInTheRecordingFormat(t *testing.T) {
	res, dir := exportToTemp(t, ExportOptions{})
	es := readEntries(t, res.Path)

	want := []string{TypeStarted, TypePrompt, TypeAssistant, TypeToolCall, TypeToolResult, TypeToolCall, TypeToolResult, TypeToolCall, TypeToolResult,
		TypeAssistant, TypePrompt, TypeAssistant, TypeToolCall, TypeToolResult, TypeAssistant, TypeStopped}
	if got := types(es); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("lines are\n%v\nwant\n%v", got, want)
	}
	if res.Lines != len(es) || res.Prompts != 2 || res.Messages != 4 || res.ToolCalls != 4 || res.Full || res.Skipped != 1 {
		t.Errorf("result = %+v for %d lines", res, len(es))
	}

	// In the project's folder under the recordings folder, 0600, named for its
	// session, and where an export of it is kept apart from the recording.
	folder := filepath.Join(dir, "recordings", Folder("shop", "/work/shop"), "exports")
	if res.Path != filepath.Join(folder, "20261001T090000Z-11111111.jsonl") {
		t.Errorf("path = %s", res.Path)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(res.Path); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, %v", fi.Mode(), err)
		}
	}

	for i, e := range es {
		if e.V != Version || e.Seq != int64(i+1) || e.Pane != exportMeta.Pane || e.Agent != "claude" || e.Conversation != fixtureConversation || e.Session != "20261001T090000Z-11111111" {
			t.Errorf("line %d envelope = %+v", i+1, e)
		}
	}
	// The lines carry the times things happened, and never go back.
	first, last := es[0], es[len(es)-1]
	if !strings.HasPrefix(first.Time, "2026-10-01T09:00:00") || !strings.HasPrefix(last.Time, "2026-10-01T09:01:04") {
		t.Errorf("times run %s to %s", first.Time, last.Time)
	}
	for i := 1; i < len(es); i++ {
		if es[i].Time < es[i-1].Time {
			t.Errorf("line %d goes back in time", i+1)
		}
	}
}

// Nothing the agent's hooks said is in a transcript, and nothing in it says how
// it was made.
func TestExportHasOnlyWhatTheStoredConversationHolds(t *testing.T) {
	res, _ := exportToTemp(t, ExportOptions{})
	es := readEntries(t, res.Path)
	for _, e := range es {
		switch e.Type {
		case TypePermission, TypeOutcome, TypeSession, TypeStatus:
			t.Errorf("a %s line, which the stored conversation has no record of", e.Type)
		}
		if e.Source != "" {
			t.Errorf("line %d says where it came from: %q", e.Seq, e.Source)
		}
	}
	if es[0].Text != startText || es[len(es)-1].Text != endText {
		t.Errorf("first and last lines say %q and %q", es[0].Text, es[len(es)-1].Text)
	}
}

func TestExportAppliesRedactionAndSecretFileWithholding(t *testing.T) {
	res, _ := exportToTemp(t, ExportOptions{})
	raw, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "hunter2") {
		t.Errorf("a secret is in the export:\n%s", raw)
	}
	es := readEntries(t, res.Path)
	var prompt, envResult Entry
	for _, e := range es {
		if e.Type == TypePrompt && prompt.Type == "" {
			prompt = e
		}
		if e.Type == TypeToolResult && e.ToolUseID == "toolu_2" {
			envResult = e
		}
	}
	if !prompt.Redacted {
		t.Errorf("the prompt with a password in it is not flagged redacted: %+v", prompt)
	}
	if envResult.Output != Withheld || !envResult.Redacted {
		t.Errorf("the .env read's output = %q redacted=%v", envResult.Output, envResult.Redacted)
	}
}

// Every message the agent said is in it, not only the last of each turn.
func TestExportHasEveryMessage(t *testing.T) {
	res, _ := exportToTemp(t, ExportOptions{})
	var got []string
	for _, e := range readEntries(t, res.Path) {
		if e.Type == TypeAssistant {
			got = append(got, e.Text)
		}
	}
	want := "I'll look at the client first.|Stopped. Say go to retry.|Trying the change.|Done: added a retry."
	if strings.Join(got, "|") != want {
		t.Errorf("messages = %q", got)
	}
}

// The point of there being one source: a conversation recorded as it went on
// and the same conversation exported afterwards are the same bytes. The
// recording is made the way a pane's is -- looked at between writes to the
// agent's file, which are cut anywhere, a line half written included -- and
// the export in one look at the finished file.
func TestRecordedAndExportedTranscriptsAreIdentical(t *testing.T) {
	whole, err := os.ReadFile(filepath.Join(fixtureHome(t), "projects", "C--work-shop", fixtureConversation+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	exported, _ := exportToTemp(t, ExportOptions{})
	want, err := os.ReadFile(exported.Path)
	if err != nil {
		t.Fatal(err)
	}

	for _, step := range []int{13, 200, 4096} {
		home := t.TempDir()
		folder := filepath.Join(home, "projects", "C--work-shop")
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
		spec, ex := claudeAt(t, home)
		f := ex.Follow(spec, fixtureConversation)
		state := t.TempDir()
		m := NewManager(func() (string, error) { return state, nil })

		for at := 0; at < len(whole); at += step {
			fh, err := os.OpenFile(filepath.Join(folder, fixtureConversation+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			fh.Write(whole[at:min(at+step, len(whole))])
			fh.Close()
			if _, err := Sync(m, exportMeta, f); err != nil {
				t.Fatal(err)
			}
		}
		path := m.Path(exportMeta.Pane)
		m.Finish(exportMeta)
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("step %d: the recording and the export differ\nrecorded:\n%s\nexported:\n%s", step, got, want)
		}
		m.Close()
	}
}

func TestExportToAPathOfItsOwn(t *testing.T) {
	out := filepath.Join(t.TempDir(), "sub", "mine.jsonl")
	res, dir := exportToTemp(t, ExportOptions{Path: out})
	if res.Path != out {
		t.Fatalf("wrote %s, want %s", res.Path, out)
	}
	// The session is the conversation's, not the file's name, so the lines are
	// the same wherever the file is put.
	if es := readEntries(t, out); es[0].Session != "20261001T090000Z-11111111" {
		t.Errorf("session = %q", es[0].Session)
	}
	if _, err := os.Stat(filepath.Join(dir, "recordings")); err == nil {
		t.Error("the recordings folder was made for an export that went elsewhere")
	}
	// Never over a file that is there.
	keep := filepath.Join(filepath.Dir(out), "keep.jsonl")
	if err := os.WriteFile(keep, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Export(func() (string, error) { return dir, nil }, exportMeta, fixtureFollower(t, fixtureConversation), ExportOptions{Path: keep})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("exporting over a file: %v", err)
	}
	if b, _ := os.ReadFile(keep); string(b) != "mine" {
		t.Errorf("the file was changed: %q", b)
	}
}

// Exporting twice is one file, not two.
func TestExportingTwiceMakesOneFile(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := 0; i < 2; i++ {
		res, err := Export(func() (string, error) { return dir, nil }, exportMeta, fixtureFollower(t, fixtureConversation), ExportOptions{})
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, res.Path)
	}
	if paths[0] != paths[1] {
		t.Errorf("two files: %v", paths)
	}
	if es := readEntries(t, paths[0]); len(es) != 16 {
		t.Errorf("%d lines", len(es))
	}
}

func TestCheckExportPathRefusesTheProjectAndGitRepositories(t *testing.T) {
	project := t.TempDir()
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	for name, c := range map[string]struct {
		path    string
		wantErr bool
	}{
		"in the project":         {filepath.Join(project, "t.jsonl"), true},
		"deep in the project":    {filepath.Join(project, "a", "b", "t.jsonl"), true},
		"in a git repository":    {filepath.Join(repo, "docs", "t.jsonl"), true},
		"somewhere else":         {filepath.Join(elsewhere, "t.jsonl"), false},
		"beside the project dir": {project + "-notes" + string(filepath.Separator) + "t.jsonl", false},
	} {
		err := CheckExportPath(c.path, project)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestExportOfNothingMakesNoFile(t *testing.T) {
	dir := t.TempDir()
	_, err := Export(func() (string, error) { return dir, nil }, exportMeta, fixtureFollower(t, "99999999-0000-0000-0000-000000000000"), ExportOptions{})
	if !errors.Is(err, transcript.ErrNoTranscript) {
		t.Fatalf("a conversation that is not there gave %v", err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("left behind: %v", ents)
	}
	// One that is there and holds nothing a transcript has.
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "projects", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "projects", "x", "c1.jsonl"), []byte(`{"type":"summary","summary":"s"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec, ex := claudeAt(t, home)
	if _, err := Export(func() (string, error) { return dir, nil }, exportMeta, ex.Follow(spec, "c1"), ExportOptions{}); err != ErrNothingToExport {
		t.Errorf("a conversation with nothing in it gave %v", err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("left behind: %v", ents)
	}
}

// A failure part way leaves no half-made file to be taken for an export.
func TestExportThatFailsLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	if _, err := Export(func() (string, error) { return dir, nil }, exportMeta, failingFollower{}, ExportOptions{}); err == nil {
		t.Fatal("no error")
	}
	var found []string
	_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			found = append(found, p)
		}
		return nil
	})
	if len(found) != 0 {
		t.Errorf("left behind: %v", found)
	}
}

type failingFollower struct{}

func (failingFollower) Poll(yield func(transcript.ExportEvent) error) (transcript.ExportStats, error) {
	_ = yield(transcript.ExportEvent{Time: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), Kind: transcript.ExportPrompt, Text: "hi"})
	return transcript.ExportStats{}, os.ErrDeadlineExceeded
}

// An export does not delete recordings to make room, which making one does.
func TestExportDeletesNothing(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "recordings", Folder("shop", "/work/shop"))
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(folder, "old.jsonl")
	if err := os.WriteFile(old, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-90 * 24 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	if _, err := Export(func() (string, error) { return dir, nil }, exportMeta, fixtureFollower(t, fixtureConversation), ExportOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Errorf("an old recording was deleted by an export: %v", err)
	}
}

// A conversation that goes past the size cap is cut there, and the file ends
// with the line that says so.
func TestExportStopsAtTheSizeCap(t *testing.T) {
	big := strings.Repeat("word ", 5000) // 25 KB, under the message cap
	src := cappedFollower{text: big}
	dir := t.TempDir()
	res, err := Export(func() (string, error) { return dir, nil }, exportMeta, src, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Full {
		t.Fatal("not cut at the cap")
	}
	fi, _ := os.Stat(res.Path)
	if fi.Size() > MaxFileBytes+1<<10 {
		t.Errorf("file is %d bytes, over the cap", fi.Size())
	}
	es := readEntries(t, res.Path)
	last := es[len(es)-1]
	if last.Type != TypeTruncated || res.Lines != len(es) {
		t.Errorf("last line = %+v, %d lines for result %d", last, len(es), res.Lines)
	}
}

type cappedFollower struct{ text string }

func (c cappedFollower) Poll(yield func(transcript.ExportEvent) error) (transcript.ExportStats, error) {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 1000; i++ {
		if err := yield(transcript.ExportEvent{Time: at.Add(time.Duration(i) * time.Second), Kind: transcript.ExportPrompt, Text: c.text}); err != nil {
			return transcript.ExportStats{}, err
		}
	}
	return transcript.ExportStats{}, nil
}

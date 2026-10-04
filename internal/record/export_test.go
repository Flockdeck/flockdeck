package record

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

const fixtureConversation = "11111111-2222-3333-4444-555555555555"

var exportMeta = Meta{Project: "shop", ProjectRoot: "/work/shop", Agent: "claude", Conversation: fixtureConversation}

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
		if e.V != Version || e.Seq != int64(i+1) || e.Agent != "claude" || e.Conversation != fixtureConversation || e.Session != "20261001T090000Z-11111111" {
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
		if !slices.Contains(allTypes, e.Type) {
			t.Errorf("a %s line, which the stored conversation has no record of", e.Type)
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
			if _, err := Sync(m, func() Meta { return exportMeta }, f); err != nil {
				t.Fatal(err)
			}
		}
		path := m.Path(exportMeta.Conversation)
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

// A link into the project does not get a transcript into it.
func TestCheckExportPathFollowsSymbolicLinks(t *testing.T) {
	project := t.TempDir()
	elsewhere := t.TempDir()
	link := filepath.Join(elsewhere, "link")
	if err := os.Symlink(project, link); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if err := CheckExportPath(filepath.Join(link, "t.jsonl"), project); err == nil {
		t.Error("a path into the project through a link was allowed")
	}
	if err := CheckExportPath(filepath.Join(link, "new", "t.jsonl"), project); err == nil {
		t.Error("a path below a folder that does not exist yet, through a link, was allowed")
	}
}

// Whose lines a transcript has is the conversation's alone: nothing a pane or a
// saved layout says about itself is in them.
func TestMetaForIsTheConversationsAlone(t *testing.T) {
	spec, ex := claudeAt(t, fixtureHome(t))
	a := MetaFor(spec, ex, fixtureConversation)
	if a.Conversation != fixtureConversation || a.Agent != "claude" {
		t.Errorf("meta = %+v", a)
	}
	if b := MetaFor(spec, ex, fixtureConversation); b != a {
		t.Errorf("two askings differ: %+v and %+v", a, b)
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
	src := cappedFollower{text: strings.Repeat("word ", 400)}
	dir := t.TempDir()
	res, err := Export(func() (string, error) { return dir, nil }, exportMeta, src, ExportOptions{MaxBytes: 32 << 10})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Full {
		t.Fatal("not cut at the cap")
	}
	fi, _ := os.Stat(res.Path)
	if fi.Size() > 32<<10+1<<10 {
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
	for i := 0; i < 200; i++ {
		if err := yield(transcript.ExportEvent{Time: at.Add(time.Duration(i) * time.Second), Kind: transcript.ExportPrompt, Text: c.text}); err != nil {
			return transcript.ExportStats{}, err
		}
	}
	return transcript.ExportStats{}, nil
}

// A Manager that has been closed writes nothing more, and does not open the
// file again to truncate a transcript that was finished.
func TestAClosedManagerWritesNothing(t *testing.T) {
	m, _ := newTestManager(t)
	f := newFeed(t, m)
	f.prompt("one")
	f.say("two")
	path := f.path()
	f.finish()
	before, _ := os.ReadFile(path)
	m.Close()
	g := newFeed(t, m)
	if err := g.send(transcript.ExportEvent{Kind: transcript.ExportPrompt, Text: "late"}); !errors.Is(err, ErrClosed) {
		t.Errorf("a write after Close gave %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Errorf("a finished transcript was changed after Close:\n%s\n%s", before, after)
	}
}

// A finished transcript is only replaced by one that has everything it had, and
// what was being written in its place does not survive otherwise.
func TestAFinishedTranscriptIsNotReplacedByLess(t *testing.T) {
	m, _ := newTestManager(t)
	f := newFeed(t, m)
	f.prompt("one")
	f.say("two")
	path := f.path()
	f.finish()
	finishedBytes, _ := os.ReadFile(path)

	// The same transcript, grown: it takes the file's place.
	g := newFeed(t, m)
	g.prompt("one")
	g.say("two")
	g.say("three")
	if p := g.path(); p != path {
		t.Fatalf("the replacement is going to %s, not %s", p, path)
	}
	if during, _ := os.ReadFile(path); string(during) != string(finishedBytes) {
		t.Error("the finished transcript was changed while its replacement was being written")
	}
	g.finish()
	if es := readEntries(t, path); len(es) != 5 || es[3].Text != "three" {
		t.Errorf("the grown transcript did not replace it: %v", types(es))
	}
	grown, _ := os.ReadFile(path)

	// One that is not: left alone.
	h := newFeed(t, m)
	h.prompt("one")
	h.say("something else")
	h.finish()
	if now, _ := os.ReadFile(path); string(now) != string(grown) {
		t.Errorf("a transcript was replaced by one that did not have its lines:\n%s", now)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.new")); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
}

// A conversation whose first event moved is one file, not two.
func TestAConversationWhoseFirstEventMovedIsOneFile(t *testing.T) {
	m, _ := newTestManager(t)
	f := newFeed(t, m)
	f.prompt("one")
	old := f.path()
	f.finish()

	g := newFeed(t, m)
	g.at = g.at.Add(-time.Hour)
	g.prompt("one, earlier")
	now := g.path()
	g.finish()
	if now == old {
		t.Fatal("the test did not move the first event")
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("the old file of the same conversation is still there")
	}
	if files, _ := filepath.Glob(filepath.Join(filepath.Dir(now), "*.jsonl")); len(files) != 1 {
		t.Errorf("files: %v", files)
	}
	// Another conversation's file is not touched.
	h := newFeed(t, m)
	h.meta.Conversation = "ffffffff-0000"
	h.prompt("other")
	other := h.path()
	h.finish()
	i := newFeed(t, m)
	i.at = i.at.Add(-2 * time.Hour)
	i.prompt("again")
	i.finish()
	if _, err := os.Stat(other); err != nil {
		t.Errorf("another conversation's transcript was removed: %v", err)
	}
}

// A write that fails is reported, not counted, and not lost track of.
func TestAWriteThatCannotBeOpenedIsReported(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(func() (string, error) { return blocker, nil }) // a file where a folder must be
	t.Cleanup(m.Close)
	f := newFeed(t, m)
	if err := f.send(transcript.ExportEvent{Kind: transcript.ExportPrompt, Text: "x"}); err == nil || errors.Is(err, ErrFull) {
		t.Errorf("err = %v", err)
	}
	if m.Active(f.meta.Conversation) {
		t.Error("a file is open for a transcript that could not be made")
	}
}

// Long ids are cut, not written whole.
func TestIdsAreClipped(t *testing.T) {
	m, _ := newTestManager(t)
	f := newFeed(t, m)
	long := strings.Repeat("t", 5000)
	f.call(long, long, map[string]any{"x": 1})
	path := f.path()
	f.finish()
	for _, e := range readEntries(t, path) {
		if len(e.Tool) > 256 || len(e.ToolUseID) > 256 {
			t.Errorf("an id of %d / %d bytes", len(e.Tool), len(e.ToolUseID))
		}
	}
}

// Whose lines they are is asked for when the first is about to be written, so
// what the conversation has said of itself by then is in them.
func TestMetaIsAskedForAtTheFirstEvent(t *testing.T) {
	m, _ := newTestManager(t)
	cwd := ""
	fol := lateFollower{before: func() { cwd = "/work/late" }}
	meta := func() Meta {
		return Meta{Conversation: "conv-late", ProjectRoot: cwd, Project: filepath.Base(cwd)}
	}
	if _, err := Sync(m, meta, fol); err != nil {
		t.Fatal(err)
	}
	path := m.Path("conv-late")
	m.Finish(Meta{Conversation: "conv-late"})
	if es := readEntries(t, path); es[0].Project != "late" {
		t.Errorf("project = %q, want the directory known by the first event", es[0].Project)
	}
}

type lateFollower struct{ before func() }

func (l lateFollower) Poll(yield func(transcript.ExportEvent) error) (transcript.ExportStats, error) {
	l.before()
	return transcript.ExportStats{}, yield(transcript.ExportEvent{Time: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), Kind: transcript.ExportPrompt, Text: "hi"})
}

// An export never deletes another export of the conversation, as a recording's
// finishing removes a superseded recording.
func TestAnExportDoesNotDeleteAnEarlierExport(t *testing.T) {
	dir := t.TempDir()
	d := func() (string, error) { return dir, nil }
	at := func(h int) transcript.Follower {
		return cappedFollowerAt{start: time.Date(2026, 10, 1, h, 0, 0, 0, time.UTC)}
	}
	a, err := Export(d, exportMeta, at(9), ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Export(d, exportMeta, at(8), ExportOptions{}) // another first event, same conversation
	if err != nil {
		t.Fatal(err)
	}
	if a.Path == b.Path {
		t.Fatal("the test did not give the exports different names")
	}
	for _, p := range []string{a.Path, b.Path} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("an export is gone: %v", err)
		}
	}
}

type cappedFollowerAt struct{ start time.Time }

func (c cappedFollowerAt) Poll(yield func(transcript.ExportEvent) error) (transcript.ExportStats, error) {
	return transcript.ExportStats{}, yield(transcript.ExportEvent{Time: c.start, Kind: transcript.ExportPrompt, Text: "hi"})
}

// A replacement left by a quit or a crash is swept when it is old and nothing
// holds it, and not before.
func TestStaleReplacementsAreSwept(t *testing.T) {
	m, dir := newTestManager(t)
	folder := filepath.Join(dir, "recordings", Folder(testMeta.Project, testMeta.ProjectRoot))
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	mk := func(name string, age time.Duration) string {
		p := filepath.Join(folder, name)
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		_ = os.Chtimes(p, at, at)
		return p
	}
	old := mk("20250101T000000Z-aaaaaaaa.jsonl.new", 48*time.Hour)
	fresh := mk("20250102T000000Z-bbbbbbbb.jsonl.new", time.Hour)
	newFeed(t, m).prompt("hello")
	if _, err := os.Stat(old); err == nil {
		t.Error("an old abandoned replacement was kept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("a recent replacement was swept")
	}
}

// An export to a file of the user's own sweeps nothing in the folder it is in.
func TestAnExportToAPathOfItsOwnTouchesNothingBesideIt(t *testing.T) {
	dir := t.TempDir()
	notes := filepath.Join(dir, "notes.jsonl.new")
	if err := os.WriteFile(notes, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour)
	_ = os.Chtimes(notes, old, old)
	if _, err := Export(func() (string, error) { return t.TempDir(), nil }, exportMeta, fixtureFollower(t, fixtureConversation), ExportOptions{Path: filepath.Join(dir, "x.jsonl")}); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(notes); err != nil || string(b) != "mine" {
		t.Errorf("a file beside the export was touched: %q, %v", b, err)
	}
}

// An export that would replace a fuller earlier one leaves it and says so.
func TestAnExportThatWouldLoseLinesKeepsTheEarlierOne(t *testing.T) {
	dir := t.TempDir()
	d := func() (string, error) { return dir, nil }
	first, err := Export(d, exportMeta, fixtureFollower(t, fixtureConversation), ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(first.Path)
	// The same conversation, begun at the same time, with less in it.
	short := shortFollower{}
	res, err := Export(d, exportMeta, short, ExportOptions{})
	if err != nil {
		t.Fatalf("a kept earlier export is not an error: %v", err)
	}
	if !res.Kept || res.Path != first.Path {
		t.Errorf("result = %+v", res)
	}
	if after, _ := os.ReadFile(first.Path); string(after) != string(before) {
		t.Error("the earlier export was changed")
	}
	if news, _ := filepath.Glob(filepath.Join(filepath.Dir(first.Path), "*.new")); len(news) != 0 {
		t.Errorf("left behind: %v", news)
	}
}

type shortFollower struct{}

func (shortFollower) Poll(yield func(transcript.ExportEvent) error) (transcript.ExportStats, error) {
	// The fixture's first event time, and one prompt.
	return transcript.ExportStats{}, yield(transcript.ExportEvent{Time: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), Kind: transcript.ExportPrompt, Text: "different"})
}

// A transcript with the same events but different text -- another version's
// redaction, say -- replaces the earlier one: it is the same transcript.
func TestSameEventsWithDifferentTextReplace(t *testing.T) {
	m, _ := newTestManager(t)
	f := newFeed(t, m)
	f.prompt("one")
	f.say("two")
	path := f.path()
	f.finish()
	g := newFeed(t, m)
	g.prompt("one, redacted another way")
	g.say("two")
	if err := m.Finish(g.meta); err != nil {
		t.Fatal(err)
	}
	if es := readEntries(t, path); es[1].Text != "one, redacted another way" {
		t.Errorf("the replacement did not take its place: %q", es[1].Text)
	}
}

func TestFindExportTakesTheNewestOfTheConversation(t *testing.T) {
	dir := t.TempDir()
	d := func() (string, error) { return dir, nil }
	meta := exportMeta
	folder := filepath.Join(dir, "recordings", Folder(meta.Project, meta.ProjectRoot), "exports")
	if got, err := FindExport(d, meta); err != nil || got != "" {
		t.Fatalf("nothing there: %q, %v", got, err)
	}
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, age time.Duration) string {
		p := filepath.Join(folder, name)
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		_ = os.Chtimes(p, at, at)
		return p
	}
	write("20260101T000000Z-11111111.jsonl", 3*time.Hour)
	newest := write("20260102T000000Z-11111111.jsonl", time.Hour)
	write("20260103T000000Z-99999999.jsonl", 0) // another conversation
	if got, _ := FindExport(d, meta); got != newest {
		t.Errorf("found %s, want %s", got, newest)
	}
}

// Only a regular file inside the recordings folder may be shown, links
// followed: not a file elsewhere, a folder, or a link out of the folder.
func TestInRecordingsRefusesAnythingButItsOwnFiles(t *testing.T) {
	dir := t.TempDir()
	d := func() (string, error) { return dir, nil }
	folder := filepath.Join(dir, "recordings", "p-1")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(folder, "t.jsonl")
	outside := filepath.Join(t.TempDir(), "other.txt")
	for _, p := range []string{inside, outside} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := InRecordings(d, inside)
	if err != nil {
		t.Fatalf("a file of its own was refused: %v", err)
	}
	if want, _ := filepath.EvalSymlinks(inside); got != want {
		t.Errorf("returned %s, want the resolved %s", got, want)
	}
	if _, err := InRecordings(d, outside); err == nil {
		t.Error("a file outside the recordings folder was accepted")
	}
	if _, err := InRecordings(d, folder); err == nil {
		t.Error("a folder was accepted")
	}
	if _, err := InRecordings(d, filepath.Join(folder, "missing.jsonl")); err == nil {
		t.Error("a file that is not there was accepted")
	}
	link := filepath.Join(folder, "link.jsonl")
	if err := os.Symlink(outside, link); err == nil {
		if _, err := InRecordings(d, link); err == nil {
			t.Error("a link out of the recordings folder was accepted")
		}
	}
}

const modelFixtureConversation = "22222222-3333-4444-5555-666666666666"

var modelMeta = Meta{Project: "shop", ProjectRoot: "/work/shop", Agent: "claude", Conversation: modelFixtureConversation}

func modelFixtureHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.Abs("../session/transcript/testdata/claude_model/home")
	if err != nil {
		t.Fatal(err)
	}
	return home
}

// modelsOf is the model on each line of a transcript, by type.
func modelsOf(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Type+"="+e.Model)
	}
	return out
}

// The lines of an assistant turn name the model that produced it, in the
// export and in a recording alike, and the two are the same bytes.
func TestExportAndRecordingCarryTheModelOfEachAssistantTurn(t *testing.T) {
	whole, err := os.ReadFile(filepath.Join(modelFixtureHome(t), "projects", "C--work-shop", modelFixtureConversation+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	spec, ex := claudeAt(t, modelFixtureHome(t))
	dir := t.TempDir()
	res, err := Export(func() (string, error) { return dir, nil }, modelMeta, ex.Follow(spec, modelFixtureConversation), ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	wantModels := []string{
		TypeStarted + "=",
		TypePrompt + "=",
		TypeAssistant + "=claude-opus-5-5",
		TypeToolCall + "=claude-opus-5-5",
		TypeToolResult + "=",
		TypePrompt + "=",
		TypeToolCall + "=claude-sonnet-5-5",
		TypeToolResult + "=",
		TypeAssistant + "=",
		TypeAssistant + "=",
		TypeAssistant + "=",
		TypeAssistant + "=claude-sonnet-5-5",
		TypeStopped + "=",
	}
	schema := loadSchema(t)
	for _, line := range rawLines(t, res.Path) {
		if errs := schema.Validate(line); len(errs) != 0 {
			t.Errorf("line does not match the schema: %v\n%s", errs, line)
		}
	}
	if got := modelsOf(readEntries(t, res.Path)); !reflect.DeepEqual(got, wantModels) {
		t.Errorf("models = %v\nwant %v", got, wantModels)
	}

	// Recorded as it grows, cut anywhere.
	home := t.TempDir()
	folder := filepath.Join(home, "projects", "C--work-shop")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	rspec, rex := claudeAt(t, home)
	f := rex.Follow(rspec, modelFixtureConversation)
	m := NewManager(func() (string, error) { return t.TempDir(), nil })
	defer m.Close()
	for at := 0; at < len(whole); at += 37 {
		fh, err := os.OpenFile(filepath.Join(folder, modelFixtureConversation+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		fh.Write(whole[at:min(at+37, len(whole))])
		fh.Close()
		if _, err := Sync(m, func() Meta { return modelMeta }, f); err != nil {
			t.Fatal(err)
		}
	}
	path := m.Path(modelMeta.Conversation)
	m.Finish(modelMeta)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the recording and the export differ\nrecorded:\n%s\nexported:\n%s", got, want)
	}
}

// A transcript from before the model was written has no model on any line,
// and is still a valid line of the format.
func TestExportOfAConversationWithNoModelsHasNone(t *testing.T) {
	res, _ := exportToTemp(t, ExportOptions{})
	for _, e := range readEntries(t, res.Path) {
		if e.Model != "" {
			t.Errorf("line %d (%s) has model %q", e.Seq, e.Type, e.Model)
		}
	}
}

const moreFixtureConversation = "33333333-4444-5555-6666-777777777777"

var moreMeta = Meta{Project: "shop", ProjectRoot: "/work/shop", Agent: "claude", Conversation: moreFixtureConversation}

func moreFixtureHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.Abs("../session/transcript/testdata/claude_more/home")
	if err != nil {
		t.Fatal(err)
	}
	return home
}

// A reply's usage and stop reason, each line's branch, folder and version, the
// title and the compaction are in the export and in a recording alike, and the
// two are the same bytes.
func TestExportAndRecordingCarryUsageDetailsTitlesAndCompaction(t *testing.T) {
	whole, err := os.ReadFile(filepath.Join(moreFixtureHome(t), "projects", "C--work-shop", moreFixtureConversation+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	spec, ex := claudeAt(t, moreFixtureHome(t))
	dir := t.TempDir()
	res, err := Export(func() (string, error) { return dir, nil }, moreMeta, ex.Follow(spec, moreFixtureConversation), ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	schema := loadSchema(t)
	for _, line := range rawLines(t, res.Path) {
		if errs := schema.Validate(line); len(errs) != 0 {
			t.Errorf("line does not match the schema: %v\n%s", errs, line)
		}
	}
	es := readEntries(t, res.Path)
	sum := map[string]int{}
	var titles, compacts int
	var last time.Time
	for i, e := range es {
		at, err := time.Parse(time.RFC3339Nano, e.Time)
		if err != nil {
			t.Fatal(err)
		}
		if at.Before(last) {
			t.Errorf("line %d goes back in time: %s after %s", e.Seq, e.Time, last)
		}
		last = at
		if e.Seq != int64(i+1) {
			t.Errorf("seq %d at line %d", e.Seq, i+1)
		}
		if e.Usage != nil {
			// A key that is not there adds nothing.
			for k, p := range map[string]*int{"in": e.Usage.InputTokens, "out": e.Usage.OutputTokens, "create": e.Usage.CacheCreationInputTokens, "read": e.Usage.CacheReadInputTokens} {
				if p != nil {
					sum[k] += *p
				}
			}
		}
		switch e.Type {
		case TypeTitle:
			titles++
		case TypeCompacted:
			compacts++
		}
	}
	// The lines that open and close the file say where the first and the last event
	// were, as the entry behind each does, so that none has less than the lines
	// between them.
	where := func(e Entry) [3]string { return [3]string{e.GitBranch, e.Cwd, e.AgentVersion} }
	if where(es[0]) != where(es[1]) || where(es[0]) == ([3]string{}) {
		t.Errorf("the first line is at %v, the first event at %v", where(es[0]), where(es[1]))
	}
	if n := len(es); where(es[n-1]) != where(es[n-2]) {
		t.Errorf("the last line is at %v, the last event at %v", where(es[n-1]), where(es[n-2]))
	}
	if want := map[string]int{"in": 7, "out": 150, "create": 1000, "read": 5000}; !reflect.DeepEqual(sum, want) {
		t.Errorf("usage adds up to %+v", sum)
	}
	if titles != 2 || compacts != 2 {
		t.Errorf("titles = %d, compactions = %d, want 2 and 2", titles, compacts)
	}
	for _, e := range es {
		if e.Type == TypeAssistant && e.Text == "Looking." {
			if e.Usage == nil || e.Usage.OutputTokens == nil || *e.Usage.OutputTokens != 100 || e.StopReason != "tool_use" || e.Cwd != "/work/shop" || e.GitBranch != "main" || e.AgentVersion != "2.1.1" {
				t.Errorf("the first reply's line: %+v", e)
			}
		}
		if e.Type == TypeToolCall && e.Tool == "Bash" && (e.Cwd != "/work/shop-wt" || e.GitBranch != "feature/retry") {
			t.Errorf("the line after the worktree switch: %+v", e)
		}
		if e.Type == TypeCompacted && (e.Trigger != "" && (e.Trigger != "manual" || e.TokensBefore != 170000 || e.TokensAfter != 9000)) {
			t.Errorf("the compaction: %+v", e)
		}
	}

	// Recorded as it grows, cut anywhere.
	home := t.TempDir()
	folder := filepath.Join(home, "projects", "C--work-shop")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	rspec, rex := claudeAt(t, home)
	f := rex.Follow(rspec, moreFixtureConversation)
	m := NewManager(func() (string, error) { return t.TempDir(), nil })
	defer m.Close()
	for at := 0; at < len(whole); at += 41 {
		fh, err := os.OpenFile(filepath.Join(folder, moreFixtureConversation+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		fh.Write(whole[at:min(at+41, len(whole))])
		fh.Close()
		if _, err := Sync(m, func() Meta { return moreMeta }, f); err != nil {
			t.Fatal(err)
		}
	}
	path := m.Path(moreMeta.Conversation)
	m.Finish(moreMeta)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the recording and the export differ\nrecorded:\n%s\nexported:\n%s", got, want)
	}
}

// A conversation that records none of these has none of them on any line: nothing
// is made up.
func TestExportOfAConversationWithNoneOfTheNewDetailsHasNone(t *testing.T) {
	res, _ := exportToTemp(t, ExportOptions{})
	for _, e := range readEntries(t, res.Path) {
		if e.Usage != nil || e.StopReason != "" || e.GitBranch != "" || e.AgentVersion != "" || e.Title != "" || e.Type == TypeTitle || e.Type == TypeCompacted {
			t.Errorf("line %d (%s) has %+v", e.Seq, e.Type, e)
		}
	}
}

// The folder is redacted and cut like any string, and flagged as such.
func TestCwdAndBranchAreRedactedAndClipped(t *testing.T) {
	m, _ := newTestManager(t)
	f := newFeed(t, m)
	f.must(transcript.ExportEvent{Kind: transcript.ExportPrompt, Text: "hi", Cwd: `C:\Users\sam\shop?token=hunter2abcdef`, GitBranch: strings.Repeat("b", MaxFieldBytes+10), AgentVersion: "2.1.1"})
	path := f.path()
	f.finish()
	var e Entry
	if err := json.Unmarshal(rawLines(t, path)[1], &e); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.Cwd, `C:\Users\sam\shop`) || strings.Contains(e.Cwd, "hunter2") || !e.Redacted {
		t.Errorf("cwd = %q, redacted = %v", e.Cwd, e.Redacted)
	}
	if e.Clipped["gitBranch"] != MaxFieldBytes+10 || len(e.GitBranch) > MaxFieldBytes+64 {
		t.Errorf("gitBranch clipped = %v, len %d", e.Clipped, len(e.GitBranch))
	}
}

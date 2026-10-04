package record

import (
	"encoding/json"
	"errors"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// An export made by a version that wrote fewer kinds of line (here, one with no
// title or compaction lines, so that every line after the first of them has
// another seq) is replaced when the conversation is exported again. It used to be
// kept as it was, and so a conversation exported before an upgrade never gained
// what the upgrade writes.
func TestAnExportMadeBeforeNewLinesWereWrittenIsReplaced(t *testing.T) {
	spec, ex := claudeAt(t, moreFixtureHome(t))
	dir := t.TempDir()
	d := func() (string, error) { return dir, nil }
	fresh, err := Export(d, moreMeta, ex.Follow(spec, moreFixtureConversation), ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	current, _ := os.ReadFile(fresh.Path)

	// What an earlier version wrote: the same events without the title and
	// compaction lines, numbered again.
	var old []string
	for _, line := range rawLines(t, fresh.Path) {
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatal(err)
		}
		if e.Type == TypeTitle || e.Type == TypeCompacted {
			continue
		}
		e.Seq = int64(len(old) + 1)
		b, _ := json.Marshal(e)
		old = append(old, string(b))
	}
	if len(old) == len(rawLines(t, fresh.Path)) {
		t.Fatal("the fixture has no title or compaction line, so this tests nothing")
	}
	if err := os.WriteFile(fresh.Path, []byte(strings.Join(old, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Export(d, moreMeta, ex.Follow(spec, moreFixtureConversation), ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Kept || !res.Replaced || res.Path != fresh.Path {
		t.Fatalf("result = %+v, want the earlier export replaced", res)
	}
	after, _ := os.ReadFile(fresh.Path)
	if string(after) != string(current) {
		t.Errorf("the replaced export is not what a fresh one is")
	}
	if news, _ := filepath.Glob(filepath.Join(filepath.Dir(fresh.Path), "*.new")); len(news) != 0 {
		t.Errorf("left behind: %v", news)
	}

	// A third time, nothing has changed, and it is still the one file.
	again, err := Export(d, moreMeta, ex.Follow(spec, moreFixtureConversation), ExportOptions{})
	if err != nil || again.Kept || !again.Replaced {
		t.Fatalf("third export = %+v, %v", again, err)
	}
	if final, _ := os.ReadFile(fresh.Path); string(final) != string(current) {
		t.Error("exporting the same conversation again changed the file")
	}
}

// A first export says it replaced nothing.
func TestAFirstExportReplacesNothing(t *testing.T) {
	res, _ := exportToTemp(t, ExportOptions{})
	if res.Replaced || res.Kept {
		t.Errorf("result = %+v", res)
	}
}

// Every line of every fixture conversation, exported whole and cut at a size
// cap, validates against the schema, so the schema and the writer cannot drift.
func TestEveryFixtureExportsToLinesTheSchemaAccepts(t *testing.T) {
	schema := loadSchema(t)
	for name, c := range map[string]struct {
		home string
		meta Meta
	}{
		"export": {fixtureHome(t), exportMeta},
		"model":  {modelFixtureHome(t), modelMeta},
		"more":   {moreFixtureHome(t), moreMeta},
	} {
		for _, max := range []int64{0, 700} {
			spec, ex := claudeAt(t, c.home)
			dir := t.TempDir()
			res, err := Export(func() (string, error) { return dir, nil }, c.meta, ex.Follow(spec, c.meta.Conversation), ExportOptions{MaxBytes: max})
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			lines := rawLines(t, res.Path)
			for i, l := range lines {
				if errs := schema.Validate(l); len(errs) != 0 {
					t.Errorf("%s (cap %d) line %d: %v\n%s", name, max, i+1, errs, l)
				}
				var e struct {
					Type        string
					IsError     *bool `json:"isError"`
					Interrupted *bool `json:"interrupted"`
				}
				_ = json.Unmarshal(l, &e)
				if e.Type == TypeToolResult && *e.Interrupted && !*e.IsError {
					t.Errorf("%s line %d: interrupted without isError", name, i+1)
				}
			}
			if max > 0 && !res.Full {
				t.Errorf("%s: the cap of %d did not cut the export", name, max)
			}
		}
	}
}

// A tool_result always says whether it failed and whether the user stopped it,
// true or false, so that a consumer never has to read absence as false; a usage
// with a count missing is written without that key, not with 0.
func TestToolResultsAlwaysSayIfTheyFailedAndUsageLeavesOutWhatIsMissing(t *testing.T) {
	m, _ := newTestManager(t)
	f := newFeed(t, m)
	f.must(transcript.ExportEvent{Kind: transcript.ExportMessage, Text: "x", Usage: &transcript.ExportUsage{InputTokens: transcript.Count(1), OutputTokens: transcript.Count(2)}})
	f.result("Bash", "t1", "ok")
	f.must(transcript.ExportEvent{Kind: transcript.ExportToolResult, Tool: "Bash", ToolUseID: "t2", IsError: true, Interrupted: true, Output: "stopped"})
	path := f.path()
	f.finish()
	var results int
	for _, l := range rawLines(t, path) {
		var m map[string]any
		if err := json.Unmarshal(l, &m); err != nil {
			t.Fatal(err)
		}
		switch m["type"] {
		case TypeToolResult:
			results++
			if _, ok := m["isError"].(bool); !ok {
				t.Errorf("no isError: %s", l)
			}
			if _, ok := m["interrupted"].(bool); !ok {
				t.Errorf("no interrupted: %s", l)
			}
		case TypeAssistant:
			u, _ := m["usage"].(map[string]any)
			if len(u) != 2 || u["inputTokens"] != float64(1) || u["outputTokens"] != float64(2) {
				t.Errorf("usage = %v", u)
			}
		default:
			if _, ok := m["isError"]; ok {
				t.Errorf("isError on a %v line", m["type"])
			}
		}
	}
	if results != 2 {
		t.Errorf("%d tool results", results)
	}
}

// exportThenPlace exports the "more" fixture, then replaces the file with body,
// and exports again.
func exportOverOld(t *testing.T, body string) (ExportResult, []byte, []byte) {
	t.Helper()
	spec, ex := claudeAt(t, moreFixtureHome(t))
	dir := t.TempDir()
	d := func() (string, error) { return dir, nil }
	fresh, err := Export(d, moreMeta, ex.Follow(spec, moreFixtureConversation), ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(fresh.Path)
	if err := os.WriteFile(fresh.Path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Export(d, moreMeta, ex.Follow(spec, moreFixtureConversation), ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(fresh.Path)
	return res, got, want
}

// A file an earlier format wrote, or a damaged one, is always replaced by a fresh
// export, whatever it holds.
func TestAnOldFormatOrDamagedExportIsAlwaysReplaced(t *testing.T) {
	const env = `"session":"20261001T090000Z-33333333","pane":"p","agent":"claude","conversation":"33333333-4444-5555-6666-777777777777"`
	// An event the new export lacks, which would keep a version 2 file.
	extra := `{"v":2,"seq":2,"time":"2026-10-01T09:00:00.5Z",` + env + `,"type":"user_prompt","text":"lost"}`
	start := func(v string) string {
		return `{"v":` + v + `,"seq":1,"time":"2026-10-01T09:00:00Z",` + env + `,"type":"recording_started","text":"start of the transcript"}`
	}
	end := `{"v":2,"seq":9,"time":"2026-10-01T09:00:09Z",` + env + `,"type":"recording_stopped","text":"end of the transcript"}`
	for name, body := range map[string]string{
		"version 1 with a status line": start("1") + "\n" + `{"v":1,"seq":2,"time":"2026-10-01T09:00:01Z",` + env + `,"type":"status","status":"idle"}` + "\n" + strings.Replace(end, `"v":2`, `"v":1`, 1) + "\n",
		"version 1 with lost events":   start("1") + "\n" + extra + "\n" + end + "\n",
		"a line that is not JSON":      start("2") + "\n" + "not json\n" + extra + "\n" + end + "\n",
		"a type no version writes":     start("2") + "\n" + `{"v":2,"seq":2,"time":"2026-10-01T09:00:01Z",` + env + `,"type":"permission_prompt","tool":"Bash"}` + "\n" + end + "\n",
		"no closing line":              start("2") + "\n" + extra + "\n",
		"an empty file":                "",
	} {
		res, got, want := exportOverOld(t, body)
		if res.Kept || !res.Replaced {
			t.Errorf("%s: result = %+v, want it replaced", name, res)
		}
		if string(got) != string(want) {
			t.Errorf("%s: the file is not a fresh export", name)
		}
	}
}

// A version 2 file that has an event the new export lacks is kept.
func TestAVersionTwoExportThatWouldLoseAnEventIsKept(t *testing.T) {
	const env = `"session":"20261001T090000Z-33333333","agent":"claude","conversation":"33333333-4444-5555-6666-777777777777"`
	old := `{"v":2,"seq":1,"time":"2026-10-01T09:00:00Z",` + env + `,"type":"recording_started","text":"start of the transcript"}` + "\n" +
		`{"v":2,"seq":2,"time":"2026-10-01T09:00:00.5Z",` + env + `,"type":"user_prompt","text":"lost"}` + "\n" +
		`{"v":2,"seq":3,"time":"2026-10-01T09:00:09Z",` + env + `,"type":"recording_stopped","text":"end of the transcript"}` + "\n"
	res, got, _ := exportOverOld(t, old)
	if !res.Kept || res.Replaced {
		t.Errorf("result = %+v, want the earlier file kept", res)
	}
	if string(got) != old {
		t.Error("the earlier file was changed")
	}
}

// Several writers exporting one conversation at once, in one process, give no
// error, leave no file beside the export, and leave the export whole.
func TestConcurrentExportsOfOneConversation(t *testing.T) {
	spec, ex := claudeAt(t, moreFixtureHome(t))
	dir := t.TempDir()
	d := func() (string, error) { return dir, nil }
	first, err := Export(d, moreMeta, ex.Follow(spec, moreFixtureConversation), ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(first.Path)
	var wg sync.WaitGroup
	errs := make(chan error, 4*40)
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				res, err := Export(d, moreMeta, ex.Follow(spec, moreFixtureConversation), ExportOptions{})
				if err == nil && res.Kept {
					err = errors.New("kept the earlier export of a conversation that has not changed")
				}
				if err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got, _ := os.ReadFile(first.Path); string(got) != string(want) {
		t.Error("the export is not whole")
	}
	ents, _ := os.ReadDir(filepath.Dir(first.Path))
	if len(ents) != 1 {
		for _, e := range ents {
			t.Errorf("in the folder: %s", e.Name())
		}
	}
}

// Two exports of one conversation are never between the check of the earlier file
// and the move over it at the same time. The test makes that span long and counts
// how many are in it, so it fails if the file's lock is not held across it.
func TestTheFileLockIsHeldFromTheCheckToTheMove(t *testing.T) {
	var in, most atomic.Int32
	inCriticalSection = func() {
		n := in.Add(1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		in.Add(-1)
	}
	t.Cleanup(func() { inCriticalSection = func() {} })
	spec, ex := claudeAt(t, moreFixtureHome(t))
	dir := t.TempDir()
	d := func() (string, error) { return dir, nil }
	if _, err := Export(d, moreMeta, ex.Follow(spec, moreFixtureConversation), ExportOptions{}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Export(d, moreMeta, ex.Follow(spec, moreFixtureConversation), ExportOptions{}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if most.Load() != 1 {
		t.Errorf("%d exports were between the check and the move at once", most.Load())
	}
	fileLocksMu.Lock()
	left := len(fileLocks)
	fileLocksMu.Unlock()
	if left != 0 {
		t.Errorf("%d file locks kept after every export finished", left)
	}
}

// When the move over the earlier export fails for good, the earlier export is as
// it was and nothing is left beside it, however many times it is tried.
func TestAFailedMoveLeavesTheEarlierExportAndNothingElse(t *testing.T) {
	t.Cleanup(func() { moveInto = renameRetry })
	spec, ex := claudeAt(t, moreFixtureHome(t))
	dir := t.TempDir()
	d := func() (string, error) { return dir, nil }
	first, err := Export(d, moreMeta, ex.Follow(spec, moreFixtureConversation), ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// An export with fewer lines than the fixture, so the next one replaces it.
	short := []string{}
	for _, l := range rawLines(t, first.Path) {
		short = append(short, string(l))
	}
	old := strings.Join(short[:len(short)-2], "\n") + "\n" + short[len(short)-1] + "\n"
	if err := os.WriteFile(first.Path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	moveInto = func(from, to string) error {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: errors.New("in use")}
	}
	for i := 0; i < 5; i++ {
		_, err := Export(d, moreMeta, ex.Follow(spec, moreFixtureConversation), ExportOptions{})
		if err == nil || !strings.Contains(err.Error(), "as it was") {
			t.Fatalf("export %d: err = %v", i, err)
		}
	}
	if got, _ := os.ReadFile(first.Path); string(got) != old {
		t.Error("the earlier export was changed")
	}
	if ents, _ := os.ReadDir(filepath.Dir(first.Path)); len(ents) != 1 {
		for _, e := range ents {
			t.Errorf("in the folder: %s", e.Name())
		}
	}
}

// Only the files a writer of transcripts leaves are swept: not a user's own file
// that happens to end in .new.
func TestOnlyTemporaryTranscriptsAreSwept(t *testing.T) {
	for name, want := range map[string]bool{
		"20261001T090000Z-33333333.jsonl.0123456789ab.new": true,
		"20261001T090000Z-33333333.jsonl.new":              true,
		"mine.jsonl.backup.new":                            false,
		"notes.new":                                        false,
		"a.jsonl.new.txt":                                  false,
		"a.jsonl.0123456789ab.new.txt":                     false,
		"a.jsonl.0123456789AB.new":                         false,
		"a.jsonl.0123.new":                                 false,
		"a.jsonl":                                          false,
		"notes.jsonl-new":                                  false,
		"xjsonlXnew":                                       false,
		"a.jsonlX0123456789abXnew":                         false,
		"a.jsonl.0123456789abXnew":                         false,
	} {
		if isTemp(name) != want {
			t.Errorf("isTemp(%q) = %v", name, !want)
		}
	}
	// And a made name is one.
	if n := tempName("/x/a.jsonl"); !isTemp(filepath.Base(n)) {
		t.Errorf("tempName gave %q, which is not swept", n)
	}
	dir := t.TempDir()
	for _, n := range []string{"notes.jsonl-new", "xjsonlXnew", "mine.jsonl.backup.new", "notes.new", "a.jsonl.new.txt", "x.jsonl.0123456789ab.new"} {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-72 * time.Hour)
		_ = os.Chtimes(p, old, old)
	}
	NewManager(func() (string, error) { return t.TempDir(), nil }).sweepStale(dir)
	for n, want := range map[string]bool{"notes.jsonl-new": true, "xjsonlXnew": true, "mine.jsonl.backup.new": true, "notes.new": true, "a.jsonl.new.txt": true, "x.jsonl.0123456789ab.new": false} {
		if _, err := os.Stat(filepath.Join(dir, n)); (err == nil) != want {
			t.Errorf("%s survives = %v, want %v", n, err == nil, want)
		}
	}
}

// exportReplacing makes an export, shortens it so the next export replaces it,
// and returns what is needed to export again and the shortened bytes.
func exportReplacing(t *testing.T) (func() (ExportResult, error), string, string) {
	t.Helper()
	spec, ex := claudeAt(t, moreFixtureHome(t))
	dir := t.TempDir()
	d := func() (string, error) { return dir, nil }
	first, err := Export(d, moreMeta, ex.Follow(spec, moreFixtureConversation), ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range rawLines(t, first.Path) {
		lines = append(lines, string(l))
	}
	old := strings.Join(lines[:len(lines)-2], "\n") + "\n" + lines[len(lines)-1] + "\n"
	if err := os.WriteFile(first.Path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	again := func() (ExportResult, error) {
		return Export(d, moreMeta, ex.Follow(spec, moreFixtureConversation), ExportOptions{})
	}
	return again, first.Path, old
}

// The new file is synced after its closing line is written and before it is
// closed and moved over the earlier one.
func TestTheNewExportIsSyncedBeforeItIsMovedIntoPlace(t *testing.T) {
	again, final, old := exportReplacing(t)
	var calls int
	syncFile = func(f *os.File) error {
		calls++
		if b, _ := os.ReadFile(final); string(b) != old {
			t.Error("the earlier file was already replaced when the new one was synced")
		}
		if _, err := f.Write(nil); err != nil {
			t.Errorf("the new file was already closed when it was synced: %v", err)
		}
		if b, _ := os.ReadFile(f.Name()); !strings.Contains(string(b), endText) {
			t.Error("the new file had no closing line when it was synced")
		}
		return f.Sync()
	}
	t.Cleanup(func() { syncFile = func(f *os.File) error { return f.Sync() } })
	res, err := again()
	if err != nil || !res.Replaced {
		t.Fatalf("export = %+v, %v", res, err)
	}
	if calls != 1 {
		t.Errorf("synced %d times, want once", calls)
	}
}

// A sync that fails leaves no new file and the earlier export as it was.
func TestAFailedSyncLeavesTheEarlierExportAndNoTempFile(t *testing.T) {
	again, final, old := exportReplacing(t)
	syncFile = func(*os.File) error { return errors.New("disk full") }
	t.Cleanup(func() { syncFile = func(f *os.File) error { return f.Sync() } })
	_, err := again()
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v, want the sync error", err)
	}
	if b, _ := os.ReadFile(final); string(b) != old {
		t.Error("the earlier export was changed")
	}
	if ents, _ := os.ReadDir(filepath.Dir(final)); len(ents) != 1 {
		for _, e := range ents {
			t.Errorf("in the folder: %s", e.Name())
		}
	}
}

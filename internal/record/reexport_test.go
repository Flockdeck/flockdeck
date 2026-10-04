package record

import (
	"encoding/json"
	"errors"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
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

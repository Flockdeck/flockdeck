package record

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
			}
			if max > 0 && !res.Full {
				t.Errorf("%s: the cap of %d did not cut the export", name, max)
			}
		}
	}
}

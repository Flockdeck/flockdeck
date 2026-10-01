package record

import (
	"bufio"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/record/schemacheck"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

const schemaPath = "../../docs/recording-line.schema.json"

func loadSchema(t *testing.T) schemacheck.Schema {
	t.Helper()
	s, err := schemacheck.Load(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func rawLines(t *testing.T, path string) [][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out [][]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		out = append(out, append([]byte(nil), sc.Bytes()...))
	}
	return out
}

// writtenTypes are the line types this version writes. The others are still in
// the schema, because files made by earlier versions have them and are still
// format 1.
var writtenTypes = []string{TypeStarted, TypeStopped, TypeTruncated, TypePrompt, TypeAssistant, TypeToolCall, TypeToolResult}

// Every line the recorder can write, of every type and with clipping and
// redaction in play, validates against the published schema.
func TestEmittedLinesMatchThePublishedSchema(t *testing.T) {
	schema := loadSchema(t)
	m, _ := newTestManager(t)
	f := newFeed(t, m)
	f.prompt("use ghp_" + strings.Repeat("b", 36))
	f.call("Bash", "t1", map[string]any{"command": strings.Repeat("a", MaxFieldBytes*2)})
	f.result("Bash", "t1", strings.Repeat("o", MaxFieldBytes*2))
	f.call("Read", "t2", map[string]any{"file_path": ".env"})
	f.result("Read", "t2", "SECRET=1")
	f.must(transcript.ExportEvent{Kind: transcript.ExportToolResult, Tool: "Bash", IsError: true, Interrupted: true, Output: "stopped"})
	f.say("Done.")
	path := f.path()
	f.finish()

	seen := map[string]bool{}
	var seq int64
	for _, line := range rawLines(t, path) {
		if errs := schema.Validate(line); len(errs) != 0 {
			t.Errorf("line does not match the schema: %v\n%s", errs, line)
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatal(err)
		}
		seen[e.Type] = true
		if e.Seq != seq+1 {
			t.Errorf("seq jumps from %d to %d", seq, e.Seq)
		}
		seq = e.Seq
	}
	for _, ty := range writtenTypes {
		if ty != TypeTruncated && !seen[ty] {
			t.Errorf("the test never emitted a %s line", ty)
		}
	}
}

// Lines an earlier version wrote -- from the agent's hooks, with status and
// permission lines -- are still format 1 and still match.
func TestLinesOfEarlierVersionsMatchThePublishedSchema(t *testing.T) {
	schema := loadSchema(t)
	for _, line := range []string{
		`{"v":1,"seq":1,"time":"2026-10-01T10:15:30.1Z","session":"20261001T101530Z-0123abcd","pane":"p","type":"recording_started","text":"turned on"}`,
		`{"v":1,"seq":2,"time":"2026-10-01T10:15:31.0Z","session":"s","pane":"p","type":"session","source":"startup","text":"start"}`,
		`{"v":1,"seq":3,"time":"2026-10-01T10:15:31.0Z","session":"s","pane":"p","type":"permission_prompt","tool":"Bash","toolUseId":"t1","subagent":"sub-1","input":{"command":"ls"}}`,
		`{"v":1,"seq":4,"time":"2026-10-01T10:15:31.0Z","session":"s","pane":"p","type":"permission_outcome","tool":"Bash","outcome":"allowed","inferred":true}`,
		`{"v":1,"seq":5,"time":"2026-10-01T10:15:31.0Z","session":"s","pane":"p","type":"status","status":"idle","previous":"working","detail":"turn over"}`,
		`{"v":1,"seq":6,"time":"2026-10-01T10:31:02.8Z","session":"s","pane":"p","type":"recording_stopped","text":"turned off"}`,
	} {
		if errs := schema.Validate([]byte(line)); len(errs) != 0 {
			t.Errorf("%v: %s", errs, line)
		}
	}
}

func TestTruncatedLineMatchesTheSchema(t *testing.T) {
	schema := loadSchema(t)
	m, _ := newTestManager(t)
	m.max = 1 << 10
	f := newFeed(t, m)
	for i := 0; i < 50; i++ {
		_ = f.send(transcript.ExportEvent{Kind: transcript.ExportMessage, Text: strings.Repeat("z", 100)})
	}
	path := f.path()
	lines := rawLines(t, path)
	last := lines[len(lines)-1]
	if !strings.Contains(string(last), TypeTruncated) {
		t.Fatalf("no truncated line: %s", last)
	}
	for _, l := range lines {
		if errs := schema.Validate(l); len(errs) != 0 {
			t.Errorf("%v: %s", errs, l)
		}
	}
}

func TestClippingAndRedactionAreFlagged(t *testing.T) {
	m, _ := newTestManager(t)
	f := newFeed(t, m)
	f.result("Bash", "", strings.Repeat("x", MaxFieldBytes+500)+" password=hunter2")
	// Already clipped by something earlier, which leaves only its marker.
	f.result("Bash", "", "head…[clipped 1000 bytes]")
	f.result("Bash", "", "small")
	path := f.path()
	f.finish()
	var es []Entry
	for _, l := range rawLines(t, path) {
		var e Entry
		_ = json.Unmarshal(l, &e)
		es = append(es, e)
	}
	a, b, c := es[1], es[2], es[3]
	if !a.Redacted || a.Clipped["output"] != MaxFieldBytes+500+len(" password=hunter2") {
		t.Errorf("redacted+clipped line: redacted=%v clipped=%v", a.Redacted, a.Clipped)
	}
	if b.Clipped["output"] != len("head")+1000 || b.Redacted {
		t.Errorf("already-clipped line: redacted=%v clipped=%v", b.Redacted, b.Clipped)
	}
	if c.Redacted || c.Clipped != nil {
		t.Errorf("a plain line is flagged: %+v", c)
	}
}

// The schema describes exactly the fields Entry has, and the event types
// there are, so neither can change without the other.
var allTypes = []string{TypeStarted, TypeStopped, TypeTruncated, TypeSession, TypePrompt, TypeAssistant, TypeToolCall, TypeToolResult, TypePermission, TypeOutcome, TypeStatus}

func TestSchemaDescribesEveryFieldAndType(t *testing.T) {
	schema := loadSchema(t)
	props, _ := schema["properties"].(map[string]any)
	var fields []string
	rt := reflect.TypeOf(Entry{})
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		fields = append(fields, name)
		if _, ok := props[name]; !ok {
			t.Errorf("Entry.%s (%q) is not in the schema", rt.Field(i).Name, name)
		}
	}
	for name := range props {
		found := false
		for _, f := range fields {
			found = found || f == name
		}
		if !found {
			t.Errorf("the schema has %q, which Entry does not", name)
		}
	}
	enum, _ := props["type"].(map[string]any)["enum"].([]any)
	var got []string
	for _, e := range enum {
		got = append(got, e.(string))
	}
	want := append([]string(nil), allTypes...)
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("schema types %v, code types %v", got, want)
	}
}

func TestSchemaRejectsALineThatIsWrong(t *testing.T) {
	schema := loadSchema(t)
	for name, line := range map[string]string{
		"no seq":          `{"v":1,"time":"2026-10-01T08:00:00Z","session":"s","pane":"p","type":"status","status":"idle"}`,
		"bad time":        `{"v":1,"seq":1,"time":"yesterday","session":"s","pane":"p","type":"status","status":"idle"}`,
		"prompt no text":  `{"v":1,"seq":1,"time":"2026-10-01T08:00:00Z","session":"s","pane":"p","type":"user_prompt"}`,
		"unknown outcome": `{"v":1,"seq":1,"time":"2026-10-01T08:00:00Z","session":"s","pane":"p","type":"permission_outcome","outcome":"maybe"}`,
		"seq is a string": `{"v":1,"seq":"1","time":"2026-10-01T08:00:00Z","session":"s","pane":"p","type":"status","status":"idle"}`,
	} {
		if errs := schema.Validate([]byte(line)); len(errs) == 0 {
			t.Errorf("%s: the schema accepted %s", name, line)
		}
	}
	// A field no version of this knows is fine: consumers ignore it.
	ok := `{"v":1,"seq":1,"time":"2026-10-01T08:00:00.5Z","session":"s","pane":"p","type":"status","status":"idle","future":1}`
	if errs := schema.Validate([]byte(ok)); len(errs) != 0 {
		t.Errorf("an unknown field was rejected: %v", errs)
	}
}

// The format page names every event type and every field, and the limits it
// quotes are the code's.
func TestFormatPageNamesEveryFieldAndType(t *testing.T) {
	b, err := os.ReadFile("../../docs/recording-format.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	for _, ty := range allTypes {
		if !strings.Contains(doc, "#### `"+ty+"`") {
			t.Errorf("the format page has no section for %s", ty)
		}
	}
	rt := reflect.TypeOf(Entry{})
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if !strings.Contains(doc, "| `"+name+"` |") {
			t.Errorf("the format page has no table row for the field %q", name)
		}
	}
	for _, want := range []string{"16 MiB", "32 KiB", "8 KiB", "30 days", "**100**", "256 MiB", Redacted, Withheld} {
		if !strings.Contains(doc, want) {
			t.Errorf("the format page does not mention %q", want)
		}
	}
	if MaxFileBytes != 16<<20 || MaxMessageBytes != 32<<10 || MaxFieldBytes != 8<<10 || RetainDays != 30 || RetainFiles != 100 || RetainBytes != 256<<20 {
		t.Error("a limit changed: update docs/recording-format.md and this test")
	}
}

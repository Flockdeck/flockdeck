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
	f.must(transcript.ExportEvent{Kind: transcript.ExportMessage, Text: "With details.", Model: "m", Usage: &transcript.ExportUsage{InputTokens: transcript.Count(1), OutputTokens: transcript.Count(2), CacheCreationInputTokens: transcript.Count(3), CacheReadInputTokens: transcript.Count(4)}, StopReason: "end_turn", GitBranch: "main", Cwd: "/work/shop", AgentVersion: "2.1.286"})
	f.must(transcript.ExportEvent{Kind: transcript.ExportTitle, Text: "Add a retry"})
	f.must(transcript.ExportEvent{Kind: transcript.ExportCompact, Trigger: "auto", TokensBefore: 900000, TokensAfter: 20000})
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
	for _, ty := range allTypes {
		if ty != TypeTruncated && !seen[ty] {
			t.Errorf("the test never emitted a %s line", ty)
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
var allTypes = []string{TypeStarted, TypeStopped, TypeTruncated, TypePrompt, TypeAssistant, TypeToolCall, TypeToolResult, TypeTitle, TypeCompacted}

// schemaFields is every field the schema names: the envelope's, and each line
// type's own.
func schemaFields(schema schemacheck.Schema) map[string]any {
	out := map[string]any{}
	top, _ := schema["properties"].(map[string]any)
	for k, v := range top {
		out[k] = v
	}
	all, _ := schema["allOf"].([]any)
	for _, branch := range all {
		then, _ := branch.(map[string]any)["then"].(map[string]any)
		props, _ := then["properties"].(map[string]any)
		for k, v := range props {
			out[k] = v
		}
	}
	return out
}

func TestSchemaDescribesEveryFieldAndType(t *testing.T) {
	schema := loadSchema(t)
	props := schemaFields(schema)
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
	const env = `"v":2,"seq":1,"time":"2026-10-01T08:00:00Z","session":"20261001T080000Z-abcd","agent":"claude","conversation":"abcd"`
	for name, line := range map[string]string{
		"no seq":                     `{"v":2,"time":"2026-10-01T08:00:00Z","session":"20261001T080000Z-abcd","agent":"claude","conversation":"abcd","type":"user_prompt","text":"x"}`,
		"version 1":                  `{"v":1,"seq":1,"time":"2026-10-01T08:00:00Z","session":"20261001T080000Z-abcd","agent":"claude","conversation":"abcd","type":"user_prompt","text":"x"}`,
		"bad time":                   `{"v":2,"seq":1,"time":"yesterday","session":"20261001T080000Z-abcd","agent":"claude","conversation":"abcd","type":"user_prompt","text":"x"}`,
		"seq is a string":            `{"v":2,"seq":"1","time":"2026-10-01T08:00:00Z","session":"20261001T080000Z-abcd","agent":"claude","conversation":"abcd","type":"user_prompt","text":"x"}`,
		"prompt without text":        `{` + env + `,"type":"user_prompt"}`,
		"unknown type":               `{` + env + `,"type":"status","status":"idle"}`,
		"unknown field":              `{` + env + `,"type":"user_prompt","text":"x","future":1}`,
		"pane is gone":               `{` + env + `,"pane":"abcd","type":"user_prompt","text":"x"}`,
		"model on a prompt":          `{` + env + `,"type":"user_prompt","text":"x","model":"m"}`,
		"usage on a tool result":     `{` + env + `,"type":"tool_result","usage":{"inputTokens":1,"outputTokens":1,"cacheCreationInputTokens":0,"cacheReadInputTokens":0}}`,
		"output on a tool call":      `{` + env + `,"type":"tool_call","tool":"Bash","output":"x"}`,
		"null model":                 `{` + env + `,"type":"assistant_message","text":"x","model":null}`,
		"empty branch":               `{` + env + `,"type":"user_prompt","text":"x","gitBranch":""}`,
		"result without isError":     `{` + env + `,"type":"tool_result","interrupted":false}`,
		"result without interrupted": `{` + env + `,"type":"tool_result","isError":false}`,
		"isError on a call":          `{` + env + `,"type":"tool_call","tool":"Bash","isError":false}`,
		"empty usage":                `{` + env + `,"type":"assistant_message","text":"x","usage":{}}`,
		"negative count":             `{` + env + `,"type":"assistant_message","text":"x","usage":{"inputTokens":-1}}`,
		"zero-key placeholder":       `{` + env + `,"type":"assistant_message","text":"x","usage":{"inputTokens":1,"extra":2}}`,
		"title line without one":     `{` + env + `,"type":"conversation_title"}`,
		"unknown clipped field":      `{` + env + `,"type":"user_prompt","text":"x","clipped":{"detail":9}}`,
	} {
		if errs := schema.Validate([]byte(line)); len(errs) == 0 {
			t.Errorf("%s: the schema accepted %s", name, line)
		}
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

// Every example line on the format page validates against the schema, and every
// line type has one, so the page cannot show a line the writer would not write.
func TestFormatPageExamplesMatchTheSchema(t *testing.T) {
	b, err := os.ReadFile("../../docs/recording-format.md")
	if err != nil {
		t.Fatal(err)
	}
	schema := loadSchema(t)
	seen := map[string]bool{}
	in := false
	for _, line := range strings.Split(string(b), "\n") {
		switch {
		case line == "```json":
			in = true
		case line == "```":
			in = false
		case in:
			if errs := schema.Validate([]byte(line)); len(errs) != 0 {
				t.Errorf("example does not match the schema: %v\n%s", errs, line)
			}
			var e Entry
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				t.Errorf("example is not a line: %v\n%s", err, line)
			}
			seen[e.Type] = true
		}
	}
	for _, ty := range allTypes {
		if !seen[ty] {
			t.Errorf("the format page has no example of a %s line", ty)
		}
	}
}

// Every field a line type has is in the page's table for that type, and every
// field the schema describes has a description saying why it can be absent or
// where it comes from.
func TestSchemaFieldsAreDescribed(t *testing.T) {
	schema := loadSchema(t)
	for name, def := range schemaFields(schema) {
		m, _ := def.(map[string]any)
		if _, ref := m["$ref"]; ref {
			continue
		}
		if d, _ := m["description"].(string); d == "" {
			t.Errorf("the schema gives %q no description", name)
		}
	}
}

// A line with only some of the usage counts, and a tool_result that did not fail,
// are valid.
func TestSchemaAcceptsPartialUsageAndPlainResults(t *testing.T) {
	schema := loadSchema(t)
	const env = `"v":2,"seq":1,"time":"2026-10-01T08:00:00Z","session":"20261001T080000Z-abcd","agent":"claude","conversation":"abcd"`
	for _, line := range []string{
		`{` + env + `,"type":"assistant_message","text":"x","usage":{"inputTokens":1,"outputTokens":0}}`,
		`{` + env + `,"type":"tool_result","isError":false,"interrupted":false}`,
		`{` + env + `,"type":"tool_result","isError":true,"interrupted":true,"output":"x"}`,
	} {
		if errs := schema.Validate([]byte(line)); len(errs) != 0 {
			t.Errorf("%v: %s", errs, line)
		}
	}
}

// TestValidateTranscriptFile validates every line of a transcript file against
// the schema, and checks seq has no gaps, for a reviewer to run on a real export:
//
//	FLOCKDECK_VALIDATE_TRANSCRIPT=/path/to/file.jsonl go test ./internal/record -run TestValidateTranscriptFile -v
//
// It reads the file and nothing else, and is skipped when the variable is unset.
func TestValidateTranscriptFile(t *testing.T) {
	path := os.Getenv("FLOCKDECK_VALIDATE_TRANSCRIPT")
	if path == "" {
		t.Skip("set FLOCKDECK_VALIDATE_TRANSCRIPT to a transcript file to validate it")
	}
	schema := loadSchema(t)
	lines := rawLines(t, path)
	bad := 0
	for i, l := range lines {
		if errs := schema.Validate(l); len(errs) != 0 {
			if bad++; bad <= 10 {
				t.Errorf("line %d: %v", i+1, errs)
			}
		}
		var e struct{ Seq int64 }
		_ = json.Unmarshal(l, &e)
		if e.Seq != int64(i+1) && bad < 10 {
			bad++
			t.Errorf("line %d has seq %d", i+1, e.Seq)
		}
	}
	t.Logf("%d lines, %d invalid", len(lines), bad)
}

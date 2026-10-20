package server

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/bridge"
	"github.com/jmwri/flockdeck/internal/record/schemacheck"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/structured"
)

// docs/structured-pane.schema.json is the definition of the structured pane
// messages. These tests hold the messages the server writes, the schema and
// docs/structured-pane.md together.

const (
	structuredSchemaPath = "../../docs/structured-pane.schema.json"
	structuredDocPath    = "../../docs/structured-pane.md"
)

func checkStructured(t *testing.T, what string, schema schemacheck.Schema, raw []byte) {
	t.Helper()
	if errs := schema.Validate(raw); len(errs) > 0 {
		t.Errorf("%s does not fit the schema:\n  %s\n%.600s", what, strings.Join(errs, "\n  "), raw)
	}
}

func TestStructuredMessagesFitTheSchema(t *testing.T) {
	schema, err := schemacheck.Load(structuredSchemaPath)
	if err != nil {
		t.Fatal(err)
	}

	// A pane that is idle, one that is waiting, and one whose tool made a big result.
	for _, task := range []string{"plain", "hold-perm", "big"} {
		e := newStructuredEnv(t, task)
		if task == "hold-perm" {
			e.status(t, session.StatusWaiting)
		} else {
			e.status(t, session.StatusIdle)
		}
		raw := structuredReply(t, e.srv, e.c, command{Cmd: "structuredSnapshot", ID: e.child, Req: "r"}, "structured")
		checkStructured(t, task+" snapshot", schema, raw)
		if task == "big" {
			var m structuredMsg
			_ = json.Unmarshal(raw, &m)
			for _, en := range m.Entries {
				if en.HasDetail {
					d := structuredReply(t, e.srv, e.c, command{Cmd: "structuredDetail", ID: e.child, Entry: en.ID, Req: "d"}, "structuredDetail")
					checkDetail(t, schema, d)
				}
			}
		}
		if task == "plain" {
			c := e.watcher(t, 8)
			checkStructured(t, "the first message of a watch", schema,
				structuredReply(t, e.srv, c, command{Cmd: "structuredWatch", ID: e.child, Req: "w"}, "structured"))
			ask(e.srv, func() bool { return e.ws.ClosePaneByID(e.child) })
			e.push()
			for _, raw := range drain(c) {
				checkStructured(t, "the gone message", schema, raw)
			}
		}
	}

	// Messages made from states the fake agent does not reach.
	long := strings.Repeat("日本語", 800)
	secs := 3
	turns := 2
	for name, sn := range map[string]structured.Snapshot{
		"setup":  {Phase: structured.PhaseSetup, Setup: &structured.Setup{Kind: structured.SetupNode, Message: "Node 20 or newer is needed"}},
		"failed": {Phase: structured.PhaseFailed, Err: "the agent ended: exit status 1"},
		"busy": {Phase: structured.PhaseReady, Status: bridge.StatusWorking, Activity: "Run go test", Queued: 2,
			Plan:  []acp.PlanEntry{{Content: "write the test", Status: "in_progress", Priority: "high"}},
			Usage: bridge.Usage{Used: 1234, Size: 200000, Cost: 0.0042, Currency: "USD"},
			Entries: []transcript.Entry{
				{ID: "p", Kind: transcript.KindPrompt, TS: "2026-10-20T12:00:00Z", Text: "hello"},
				{ID: "t", Kind: transcript.KindThinking, TS: "2026-10-20T12:00:01Z", Chars: 500, Seconds: &secs},
				{ID: "c", Kind: transcript.KindCompaction, TS: "2026-10-20T12:00:02Z", Turns: &turns},
				{ID: "s", Kind: transcript.KindSubagent, TS: "2026-10-20T12:00:03Z", Status: "ok", Description: long, Report: long, HasDetail: true},
				{ID: "i", Kind: transcript.KindImage, TS: "2026-10-20T12:00:04Z", MediaType: "image/png", Bytes: 10, Width: 1, Height: 1, HasDetail: true},
				{ID: "x", Kind: transcript.KindTool, TS: "2026-10-20T12:00:05Z", Name: "edit", Label: long, Status: "ok", Summary: "s", Diff: long},
			}},
		"card": {Phase: structured.PhaseReady, Status: bridge.StatusWaiting,
			Pending: &bridge.Card{Seq: 9, RequestID: "r", Tool: "Write", Kind: "edit", Summary: long, File: "a.txt", Command: long,
				Options: []acp.PermissionOption{{OptionID: "a", Name: "Allow", Kind: "allow_once"}}}},
	} {
		raw, err := json.Marshal(buildStructured("p1", sn))
		if err != nil {
			t.Fatal(err)
		}
		checkStructured(t, name, schema, raw)
	}
}

func checkDetail(t *testing.T, schema schemacheck.Schema, raw []byte) {
	t.Helper()
	doc := schemacheck.Schema{"$ref": "#/$defs/detail", "$defs": schema["$defs"]}
	if errs := doc.Validate(raw); len(errs) > 0 {
		t.Errorf("a detail does not fit the schema:\n  %s", strings.Join(errs, "\n  "))
	}
	// And the schema is not so loose that it takes a made-up field.
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	m["secret"] = "x"
	bad, _ := json.Marshal(m)
	if errs := doc.Validate(bad); len(errs) == 0 {
		t.Error("the schema takes a field that is not in it")
	}
}

// The limits docs/structured-pane.md quotes are the code's.
func TestStructuredDocQuotesTheLimits(t *testing.T) {
	b, err := os.ReadFile(structuredDocPath)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	for _, c := range []struct {
		got  bool
		want string
	}{
		{structuredEntriesMax == 50, "The newest 50."},
		{structuredTextCut == 4<<10, "reply or notice in an entry | 4 KiB"},
		{structuredLabelCut == 1<<10, "A one-line field of an entry | 1 KiB"},
		{structuredPushCap == 128<<10, "A whole push | 128 KiB"},
		{structuredInterval == 500*time.Millisecond, "every 500 milliseconds"},
		{structuredPlanMax == 100 && structuredPlanCut == 200, "100 rows, each cut to 200 bytes"},
		{structuredCardCut == 300 && structuredCommandCut == 500, "300 bytes for each field, 500 for the command"},
		{structuredErrCut == 200, "| `err` | 200 characters"},
		{structuredDetailCut == 64<<10, "text and diff | 64 KiB each"},
		{maxStructuredWatches == 16, "over all windows | 16."},
		{structuredNotPane == "That pane is not a structured pane", "`That pane is not a structured pane`"},
		{structuredNoEntry == "That entry is not there", "`That entry is not there`"},
		{structuredTooMany == "Too many structured panes are being watched. Stop watching one first", "`Too many structured panes are being watched. Stop watching one first`"},
	} {
		if !c.got {
			t.Errorf("a limit changed: update docs/structured-pane.md (it quotes %q) and this test", c.want)
		}
		if !strings.Contains(doc, c.want) {
			t.Errorf("docs/structured-pane.md does not contain %q", c.want)
		}
	}
}

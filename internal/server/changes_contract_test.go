package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/record/schemacheck"
)

// The changes contract is a schema, a document and hand-built examples, and nothing
// serves it yet. These tests hold the three together, and hold the stubs to what the
// document says a build that does not have it must do.

const changesSchemaPath = "../../api/schema/changes.schema.json"

var changesFixtureDefs = map[string]string{
	"01": "changesTeam", "02": "changesTeam", "03": "changesFiles",
	"04": "diffHunks", "05": "diffHunks", "06": "diffHunks", "07": "diffHunks", "08": "diffHunks",
	"09": "diffContext",
	"10": "changesResult", "11": "changesResult", "12": "changesResult", "13": "changesResult", "14": "changesResult",
	"15": "changesChecks", "16": "changesResult", "17": "changesResult",
}

func changesFixtures(t *testing.T) map[string][]byte {
	t.Helper()
	files, err := filepath.Glob("../../api/fixtures/built-changes-*.json")
	if err != nil || len(files) != len(changesFixtureDefs) {
		t.Fatalf("found %d built-changes fixtures (%v), want %d", len(files), err, len(changesFixtureDefs))
	}
	out := map[string][]byte{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.Base(f)] = raw
	}
	return out
}

func TestChangesExamplesMatchTheSchema(t *testing.T) {
	schema, err := schemacheck.Load(changesSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range changesFixtures(t) {
		def := changesFixtureDefs[strings.TrimPrefix(name, "built-changes-")[:2]]
		if errs := validateDef(schema, def, raw); len(errs) > 0 {
			t.Errorf("%s is not a %s: %v", name, def, errs)
		}
	}
	// The root of the schema is changesTeam.
	raw := changesFixtures(t)["built-changes-01-team-mixed.json"]
	if errs := schema.Validate(raw); len(errs) > 0 {
		t.Errorf("the mixed team does not match the schema's root: %v", errs)
	}
}

// What the schema cannot say: a hunk's line counts agree with its lines, word ranges
// are inside the line, a gate is blocked exactly when it has blockers, and a branch's
// comments belong to its files and its head.
func TestChangesExamplesAreConsistent(t *testing.T) {
	type line struct {
		T     string  `json:"t"`
		Text  string  `json:"text"`
		Intra [][]int `json:"intra"`
	}
	type hunk struct {
		OldLines int    `json:"oldLines"`
		NewLines int    `json:"newLines"`
		Lines    []line `json:"lines"`
	}
	type branch struct {
		Pane    string `json:"pane"`
		Error   string `json:"error"`
		HeadSha string `json:"headSha"`
		Files   []struct {
			Path string `json:"path"`
		} `json:"files"`
		Comments []struct {
			Pane  string `json:"pane"`
			Path  string `json:"path"`
			AtSha string `json:"atSha"`
		} `json:"comments"`
		Gate struct {
			State    string     `json:"state"`
			Blockers []struct{} `json:"blockers"`
		} `json:"gate"`
	}
	for name, raw := range changesFixtures(t) {
		var doc struct {
			Hunks    []hunk   `json:"hunks"`
			Branches []branch `json:"branches"`
			Partial  *struct {
				Read int `json:"read"`
			} `json:"partial"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, h := range doc.Hunks {
			var oldN, newN int
			for _, l := range h.Lines {
				switch l.T {
				case "ctx":
					oldN++
					newN++
				case "del":
					oldN++
				case "add":
					newN++
				}
				for _, r := range l.Intra {
					if r[0] >= r[1] || r[1] > len([]rune(l.Text)) {
						t.Errorf("%s: word range %v is outside %q", name, r, l.Text)
					}
				}
			}
			if oldN != h.OldLines || newN != h.NewLines {
				t.Errorf("%s: a hunk says %d old and %d new lines and has %d and %d", name, h.OldLines, h.NewLines, oldN, newN)
			}
		}
		read := 0
		for _, b := range doc.Branches {
			if b.Error != "" {
				continue
			}
			read++
			if (b.Gate.State == "blocked") != (len(b.Gate.Blockers) > 0) {
				t.Errorf("%s: %s has gate %s with %d blockers", name, b.Pane, b.Gate.State, len(b.Gate.Blockers))
			}
			paths := map[string]bool{}
			for _, f := range b.Files {
				paths[f.Path] = true
			}
			for _, c := range b.Comments {
				if c.Pane != b.Pane || !paths[c.Path] {
					t.Errorf("%s: a comment on %s of %s is not on a file of the branch", name, c.Path, c.Pane)
				}
				if c.AtSha != b.HeadSha {
					t.Errorf("%s: a comment is at %s, the head is %s", name, c.AtSha, b.HeadSha)
				}
			}
		}
		if doc.Partial != nil && doc.Partial.Read != read {
			t.Errorf("%s: partial says %d read, %d were", name, doc.Partial.Read, read)
		}
	}
}

// The document names every command, message, cap and field of the contract.
func TestChangesDocumentNamesEverything(t *testing.T) {
	raw, err := os.ReadFile("../../docs/changes-api.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, n := range slices.Concat(changesCommands, changesCaps, changesMessages, []string{"changesResult", "changesChecks", "changesTeam", "changesFiles", "diffHunks", "diffContext"}) {
		if !strings.Contains(doc, "`"+n+"`") {
			t.Errorf("docs/changes-api.md does not name `%s`", n)
		}
	}
	schema, err := schemacheck.Load(changesSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	defs, _ := schema["$defs"].(map[string]any)
	for name, d := range defs {
		props, _ := d.(map[string]any)["properties"].(map[string]any)
		for prop := range props {
			if prop == "type" {
				continue
			}
			if !strings.Contains(doc, "`"+prop+"`") {
				t.Errorf("docs/changes-api.md does not name `%s` (a field of %s)", prop, name)
			}
		}
	}
	// Every result code of the schema is named, with the commands that use it.
	if r, _ := defs["changesResult"].(map[string]any); r != nil {
		props, _ := r["properties"].(map[string]any)
		code, _ := props["code"].(map[string]any)
		codes, _ := code["enum"].([]any)
		if len(codes) == 0 {
			t.Error("the schema has no result codes")
		}
		for _, c := range codes {
			if !strings.Contains(doc, "`"+c.(string)+"`") {
				t.Errorf("docs/changes-api.md does not name the result code `%s`", c)
			}
		}
	}
	// Every command field of the contract is in its table.
	for _, f := range []string{"file", "against", "context", "ignoreWhitespace", "side", "line", "commentId", "names", "reviewer", "note", "expectSha", "offset", "limit", "from", "to"} {
		if !strings.Contains(doc, "| `"+f+"`") && !strings.Contains(doc, "`"+f+"`, ") && !strings.Contains(doc, ", `"+f+"`") {
			t.Errorf("docs/changes-api.md has no row for the command field `%s`", f)
		}
	}
}

// No cap of the contract is listed until it is built.
func TestChangesCapsAreNotListedYet(t *testing.T) {
	built := []string{"changesTeam", "changesDiff", "changesChecks", "changesComments"}
	for _, n := range changesCaps {
		if slices.Contains(apiCaps, n) != slices.Contains(built, n) {
			t.Errorf("api.caps and what is built disagree about %s", n)
		}
	}
}

func TestPlannedChangesCommandsAreRefused(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, name := range changesCommands {
		if slices.Contains(changesBuilt, name) {
			continue
		}
		c := &controlClient{out: make(chan []byte, 8)}
		srv.handleCommand(c, command{Cmd: name, ID: "p1", Req: "r1"})
		var m changesResultMsg
		select {
		case raw := <-c.out:
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("%s was not answered", name)
		}
		if m.Type != "changesResult" || m.Op != name || m.Req != "r1" || m.OK || m.Code != "unsupported" || m.Error == "" {
			t.Errorf("%s: %+v, want an unsupported refusal carrying op and req", name, m)
		}
	}
}

func TestPlannedChangesCommandsAreDeskOnly(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, name := range changesCommands {
		c := &controlClient{out: make(chan []byte, 8), remote: true, device: "dev-1"}
		srv.handleCommand(c, command{Cmd: name, ID: "p1", Req: "r2"})
		var got []string
		for len(c.out) > 0 {
			got = append(got, string(<-c.out))
		}
		// Exactly the refusal and one notice, and nothing else.
		if len(got) != 2 || !strings.Contains(got[0], `"changesResult"`) || !strings.Contains(got[0], `"deskOnly"`) ||
			!strings.Contains(got[0], `"r2"`) || !strings.Contains(got[1], `"notice"`) {
			t.Errorf("%s from a relay window: %v, want one deskOnly changesResult carrying req, then one notice", name, got)
			continue
		}
		// What the relay is sent is the refusal, and nothing the command held.
		if out := scrubForRemote([]byte(got[0])); out == nil || !strings.Contains(string(out), `"deskOnly"`) {
			t.Errorf("%s: the relay was sent %s", name, out)
		}
	}
	// What the relay is sent is fixed words for one of two codes: an error that
	// holds a path, a branch or source text is not passed on, and any other code is
	// dropped.
	for _, code := range []string{"deskOnly", "unsupported"} {
		leak := mustJSON(t, map[string]any{"type": "changesResult", "op": "diffHunks", "req": "r3", "ok": false, "code": code,
			"error": "cannot read internal/secret/keys.go on agent/x: password=hunter2"})
		out := scrubForRemote(leak)
		if out == nil || strings.Contains(string(out), "hunter2") || strings.Contains(string(out), "secret") || !strings.Contains(string(out), code) {
			t.Errorf("%s: the relay was sent %s", code, out)
		}
	}
	for _, code := range []string{"headMoved", "failed", "notFound", ""} {
		leak := mustJSON(t, map[string]any{"type": "changesResult", "op": "approveMerge", "ok": false, "code": code, "error": "agent/x moved"})
		if out := scrubForRemote(leak); out != nil {
			t.Errorf("a %q refusal reached the relay: %s", code, out)
		}
	}
	// An answer that holds data is never sent to a relay window.
	ok := mustJSON(t, map[string]any{"type": "changesResult", "op": "commentAdd", "ok": true, "comment": map[string]any{"text": "secret"}})
	if out := scrubForRemote(ok); out != nil {
		t.Errorf("a successful changesResult reached the relay: %s", out)
	}
}

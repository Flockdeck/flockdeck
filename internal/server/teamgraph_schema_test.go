package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/record/schemacheck"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/tasks"
	"github.com/jmwri/flockdeck/internal/team"
)

// docs/team-graph.schema.json is the definition of the graph push. These tests
// hold the Go structs, the messages the server writes and the schema together.

const graphSchemaPath = "../../docs/team-graph.schema.json"

func loadGraphSchema(t *testing.T) schemacheck.Schema {
	t.Helper()
	s, err := schemacheck.Load(graphSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// validateDef checks a document against one of the schema's $defs.
func validateDef(s schemacheck.Schema, def string, doc []byte) []string {
	return schemacheck.Schema{"$ref": "#/$defs/" + def, "$defs": s["$defs"]}.Validate(doc)
}

func rawGraph(t *testing.T, e *graphEnv) []byte {
	t.Helper()
	e.srv.graphCommand(e.c, command{Cmd: "graphSnapshot", Root: e.root})
	for {
		select {
		case raw := <-e.c.out:
			if typeOf(raw) == "graph" {
				return raw
			}
		default:
			t.Fatal("no graph arrived")
		}
	}
}

// A realistic graph, with a prompt, a task, a message and a solo pane, validates
// against the schema.
func TestGraphMatchesThePublishedSchema(t *testing.T) {
	schema := loadGraphSchema(t)
	e := newGraphEnv(t)
	hookSrv := e.ws.HookServer()
	api, token := hookSrv.BaseURL(), hookSrv.Token()
	if _, err := hooks.Msg(api, token, e.dev, "manager", "need a decision"); err != nil {
		t.Fatal(err)
	}
	if _, err := hooks.Task(api, token, e.boss, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "Build it"}); err != nil {
		t.Fatal(err)
	}
	ask(e.srv, func() bool {
		e.ws.Pane(e.boss).Task = "Ship the thing"
		e.ws.Pane(e.dev).Sess.SetStatus(session.StatusWorking, "Edit main.go")
		e.ws.Pane(e.eyes).Sess.SetStatusFull(session.StatusWaiting, "Bash", `{"command":"go test ./..."}`)
		return true
	})

	raw := rawGraph(t, e)
	if errs := schema.Validate(raw); len(errs) != 0 {
		t.Errorf("the graph does not match the schema: %v\n%s", errs, raw)
	}
	var g graphMsg
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	// Each part, on its own against its definition.
	for _, n := range g.Nodes {
		b, _ := json.Marshal(n)
		if errs := validateDef(schema, "node", b); len(errs) != 0 {
			t.Errorf("node %s: %v", n.Pane, errs)
		}
	}
	for _, ed := range g.Edges {
		b, _ := json.Marshal(ed)
		if errs := validateDef(schema, "edge", b); len(errs) != 0 {
			t.Errorf("edge %+v: %v", ed, errs)
		}
	}
	for _, tm := range g.Teams {
		for _, it := range tm.Needs {
			b, _ := json.Marshal(it)
			if errs := validateDef(schema, "needsItem", b); len(errs) != 0 {
				t.Errorf("needs item %+v: %v", it, errs)
			}
		}
	}
	if len(g.Teams) == 0 || len(g.Teams[0].Needs) == 0 {
		t.Error("the test graph has no needs-you item to check")
	}
}

func TestSchemaRefusesWhatIsNotTheShape(t *testing.T) {
	schema := loadGraphSchema(t)
	bad := map[string]struct{ def, doc string }{
		"an extra field on a node":          {"node", `{"pane":"a","depth":0,"waitingOn":{"kind":"none"},"name":"a","status":"idle","activity":"idle","colour":"red"}`},
		"an unknown waiting-on kind":        {"node", `{"pane":"a","depth":0,"waitingOn":{"kind":"dozing"},"name":"a","status":"idle","activity":"idle"}`},
		"a message edge without its id":     {"edge", `{"kind":"message","from":"a","to":"b","time":"2026-10-19T12:00:00Z"}`},
		"a message edge without delivery":   {"edge", `{"kind":"message","from":"a","to":"b","id":1,"time":"2026-10-19T12:00:00Z"}`},
		"a message edge with a bad state":   {"edge", `{"kind":"message","from":"a","to":"b","id":1,"time":"2026-10-19T12:00:00Z","delivery":"lost"}`},
		"a sent edge with a delivery state": {"edge", `{"kind":"sent","from":"a","to":"b","time":"2026-10-19T12:00:00Z","delivery":"read"}`},
		"a sent edge with a message id":     {"edge", `{"kind":"sent","from":"a","to":"b","time":"2026-10-19T12:00:00Z","id":1}`},
		"a sent edge with text":             {"edge", `{"kind":"sent","from":"a","to":"b","time":"2026-10-19T12:00:00Z","text":"hi"}`},
		"a sent edge without a time":        {"edge", `{"kind":"sent","from":"a","to":"b"}`},
		"a needs item with an unknown kind": {"needsItem", `{"id":"x:a","kind":"nag","pane":"a","time":"2026-10-19T12:00:00Z"}`},
		"a paneSend without queued":         {"paneSend", `{"type":"paneSend","id":"a","shell":false}`},
	}
	for name, c := range bad {
		if errs := validateDef(schema, c.def, []byte(c.doc)); len(errs) == 0 {
			t.Errorf("%s was accepted", name)
		}
	}
	sent := `{"kind":"sent","from":"a","to":"b","time":"2026-10-19T12:00:00Z"}`
	if errs := validateDef(schema, "edge", []byte(sent)); len(errs) != 0 {
		t.Errorf("a sent edge was refused: %v", errs)
	}
	good := `{"type":"paneSend","id":"a","queued":false,"shell":true,"waiting":true}`
	if errs := validateDef(schema, "paneSend", []byte(good)); len(errs) != 0 {
		t.Errorf("a paneSend answer was refused: %v", errs)
	}
}

// jsonFields lists the names a struct encodes with, following embedded structs
// the way encoding/json does, and which of them are always written.
func jsonFields(t reflect.Type) (all, always []string) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if f.Anonymous && tag == "" {
			a, w := jsonFields(f.Type)
			all, always = append(all, a...), append(always, w...)
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if name == "-" || name == "" {
			continue
		}
		all = append(all, name)
		if !strings.Contains(opts, "omitempty") {
			always = append(always, name)
		}
	}
	return all, always
}

func schemaFields(t *testing.T, props, required any) (all, req []string) {
	t.Helper()
	for name := range props.(map[string]any) {
		all = append(all, name)
	}
	for _, r := range required.([]any) {
		req = append(req, r.(string))
	}
	return all, req
}

// The schema and the Go structs name the same fields, and the same ones are
// always written. A field added to a struct must be added to the schema, with its
// description, in the same change.
func TestSchemaAndStructsNameTheSameFields(t *testing.T) {
	schema := loadGraphSchema(t)
	defs, _ := schema["$defs"].(map[string]any)
	type shape struct{ props, required any }
	shapes := map[string]shape{"graph": {schema["properties"], schema["required"]}}
	for name, d := range defs {
		m := d.(map[string]any)
		shapes[name] = shape{m["properties"], m["required"]}
	}
	cases := []struct {
		def string
		typ reflect.Type
	}{
		{"graph", reflect.TypeOf(graphMsg{})},
		{"team", reflect.TypeOf(graphTeamView{})},
		{"cap", reflect.TypeOf(graphCap{})},
		{"node", reflect.TypeOf(graphNodeView{})},
		{"taskChip", reflect.TypeOf(graphTaskChip{})},
		{"edge", reflect.TypeOf(graphEdge{})},
		{"needsItem", reflect.TypeOf(team.NeedsItem{})},
		{"waitingOn", reflect.TypeOf(team.WaitingOn{})},
		{"paneSend", reflect.TypeOf(paneSendMsg{})},
		{"permission", reflect.TypeOf(permissionView{})},
		{"permissionOption", reflect.TypeOf(permissionOption{})},
	}
	for _, c := range cases {
		goAll, goAlways := jsonFields(c.typ)
		scAll, scReq := schemaFields(t, shapes[c.def].props, shapes[c.def].required)
		for _, l := range [][]string{goAll, goAlways, scAll, scReq} {
			sort.Strings(l)
		}
		if !reflect.DeepEqual(goAll, scAll) {
			t.Errorf("%s: the Go struct has fields %v, the schema %v", c.def, goAll, scAll)
		}
		if !reflect.DeepEqual(goAlways, scReq) {
			t.Errorf("%s: the Go struct always writes %v, the schema requires %v", c.def, goAlways, scReq)
		}
	}
}

// Every property in the schema has a description of its own, or is a reference
// to a definition that has one, or belongs to taskChip, whose object description
// covers its three fields.
func TestSchemaPropertiesAreDescribed(t *testing.T) {
	schema := loadGraphSchema(t)
	check := func(path string, node map[string]any, covered bool) {
		props, _ := node["properties"].(map[string]any)
		for name, p := range props {
			pm := p.(map[string]any)
			_, own := pm["description"].(string)
			_, ref := pm["$ref"].(string)
			if !own && !ref && !covered {
				t.Errorf("%s.%s has no description", path, name)
			}
		}
	}
	check("graph", schema, false)
	defs, _ := schema["$defs"].(map[string]any)
	for name, d := range defs {
		check(name, d.(map[string]any), name == "taskChip")
	}
}

var projectDir = regexp.MustCompile(`flockdeck-state-[0-9]+`)

var lastOutputInDoc = regexp.MustCompile(`,"lastOutput":"[^"]*"`)
var timeInDoc = regexp.MustCompile(`"(since|started|time)":"[^"]*"`)

// enumOf reads the values of the enum of one property of a definition.
func enumOf(t *testing.T, s schemacheck.Schema, def, prop string) []string {
	t.Helper()
	defs, _ := s["$defs"].(map[string]any)
	d, _ := defs[def].(map[string]any)
	props, _ := d["properties"].(map[string]any)
	p, _ := props[prop].(map[string]any)
	list, _ := p["enum"].([]any)
	if len(list) == 0 {
		t.Fatalf("%s.%s has no enum in the schema", def, prop)
	}
	var out []string
	for _, v := range list {
		out = append(out, v.(string))
	}
	sort.Strings(out)
	return out
}

// The values the schema lists are the ones the Go code writes. Renaming a
// constant without the schema, or the other way round, fails here.
func TestSchemaEnumsAreTheGoConstants(t *testing.T) {
	schema := loadGraphSchema(t)
	var states []string
	for _, st := range tasks.States {
		states = append(states, string(st))
	}
	cases := []struct {
		def, prop string
		want      []string
	}{
		{"needsItem", "kind", []string{team.NeedPermission, team.NeedAsk, team.NeedReturned, team.NeedConflict, team.NeedDown, team.NeedSetup, team.NeedStaleUnread, team.NeedStuck}},
		{"waitingOn", "kind", []string{team.WaitUser, team.WaitDown, team.WaitReviewer, team.WaitTask, team.WaitHelpers, team.WaitNone}},
		{"taskChip", "state", states},
		{"edge", "kind", []string{graphEdgeSpawn, graphEdgeMessage, graphEdgeSent}},
		{"node", "kind", []string{graphKindTerminal, graphKindStructured}},
		{"permissionOption", "id", permissionOptionIDs()},
		{"permissionOption", "kind", permissionOptionKinds()},
	}
	for _, c := range cases {
		want := append([]string(nil), c.want...)
		sort.Strings(want)
		if got := enumOf(t, schema, c.def, c.prop); !reflect.DeepEqual(got, want) {
			t.Errorf("%s.%s: the schema lists %v, the Go constants are %v", c.def, c.prop, got, want)
		}
	}
}

// A two-team project, with names in place of the pane ids and no times, is the
// same as the golden file. A change to the shape has to be made on purpose: run
// the test with FLOCKDECK_UPDATE_GOLDEN=1 to write the new file, and read the
// diff.
func TestTwoTeamGraphMatchesTheGoldenFile(t *testing.T) {
	e := newGraphEnv(t)
	boss2 := agentPane(t, e.srv, e.ws, "boss2", "manager")
	helper2 := agentPane(t, e.srv, e.ws, "helper2", "worker")
	ask(e.srv, func() bool {
		e.ws.Pane(helper2).Parent = boss2
		e.ws.Pane(e.boss).Task = "Ship the thing"
		e.ws.Pane(boss2).Task = "Fix the other thing"
		e.ws.Pane(e.dev).Sess.SetStatus(session.StatusWorking, "Edit main.go")
		e.ws.Pane(e.eyes).Sess.SetStatusFull(session.StatusWaiting, "Bash", `{"command":"go test ./..."}`)
		return true
	})
	hookSrv := e.ws.HookServer()
	if _, err := hooks.Msg(hookSrv.BaseURL(), hookSrv.Token(), helper2, boss2, "done\nsecond line"); err != nil {
		t.Fatal(err)
	}

	raw := rawGraph(t, e)
	var g graphMsg
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	// Each pane gets a name of its own, so the golden file pins the name field.
	names := map[string]string{e.boss: "boss", e.dev: "dev", e.eyes: "eyes", e.nested: "nested", e.lone: "lone", boss2: "boss2", helper2: "helper2"}
	extra := 0
	for _, n := range g.Nodes {
		if _, ok := names[n.Pane]; !ok {
			extra++
			names[n.Pane] = fmt.Sprintf("other%d", extra)
		}
	}
	// The shell the test server opens first is still starting, and for how long
	// is not fixed. Settle it and read again.
	ask(e.srv, func() bool {
		for _, n := range g.Nodes {
			e.ws.Pane(n.Pane).Name = names[n.Pane]
			if n.Shell {
				e.ws.Pane(n.Pane).Sess.SetStatus(session.StatusIdle, "")
			}
		}
		return true
	})
	raw = rawGraph(t, e)
	// lastOutput is whether a shell had written yet when the graph was read, so the
	// golden file leaves it out.
	out := string(lastOutputInDoc.ReplaceAll(raw, nil))
	out = timeInDoc.ReplaceAllString(out, `"$1":"<time>"`)
	out = projectDir.ReplaceAllString(out, "<project>")
	for id, n := range names {
		out = strings.ReplaceAll(out, id, n)
	}
	var doc any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	// The project's directory is a temporary one, with the account name in it.
	top := doc.(map[string]any)
	top["root"], top["project"] = "<root>", "<project>"
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		t.Fatal(err)
	}
	got := buf.Bytes()

	path := filepath.Join("testdata", "team-graph.golden.json")
	if os.Getenv("FLOCKDECK_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with FLOCKDECK_UPDATE_GOLDEN=1 to write it)", err)
	}
	if strings.ReplaceAll(string(want), "\r\n", "\n") != string(got) {
		t.Errorf("the graph differs from %s; if the change is meant, run with FLOCKDECK_UPDATE_GOLDEN=1 and read the diff.\ngot:\n%s", path, got)
	}
}

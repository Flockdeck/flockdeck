package server

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/bridge"
	"github.com/jmwri/flockdeck/internal/record/schemacheck"
	"github.com/jmwri/flockdeck/internal/structured"
)

// structuredSessionFixture is session state with every part set, and long
// texts in the parts that are cut.
func structuredSessionFixture(long string) bridge.SessionState {
	return bridge.SessionState{
		Commands: []acp.Command{{Name: "compact", Description: long, Hint: "n"}, {Name: "init"}},
		Modes:    &acp.Modes{Current: "yolo", Available: []acp.Mode{{ID: "default", Name: "Default"}, {ID: "yolo", Description: long}}},
		Models:   &acp.Models{Current: "pro", Available: []acp.Model{{ID: "pro", Name: "Pro"}}},
		ConfigOptions: []acp.ConfigOption{{ID: "model", Name: "Model", Category: "model", Type: "select", Current: "big",
			Values: []acp.ConfigValue{{Value: "big", Name: "Big"}, {Value: "small", Description: long, Group: "g"}}}},
		PlanMarkdown: "1. a\n2. b",
		Title:        "Fix the parser",
		Capabilities: acp.AgentCapabilities{LoadSession: true, Prompt: acp.PromptCapabilities{Image: true}, List: true},
	}
}

func TestStructuredMessageCarriesTheSessionState(t *testing.T) {
	sn := structured.Snapshot{Phase: structured.PhaseReady, Status: bridge.StatusIdle, Session: structuredSessionFixture("d")}
	raw, err := json.Marshal(buildStructured("p1", sn))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	cmds, _ := m["commands"].([]any)
	if len(cmds) != 2 || cmds[0].(map[string]any)["hint"] != "n" {
		t.Errorf("commands = %v", m["commands"])
	}
	if modes, _ := m["modes"].(map[string]any); modes["current"] != "yolo" || len(modes["available"].([]any)) != 2 {
		t.Errorf("modes = %v", m["modes"])
	}
	if models, _ := m["models"].(map[string]any); models["current"] != "pro" {
		t.Errorf("models = %v", m["models"])
	}
	opts, _ := m["configOptions"].([]any)
	if len(opts) != 1 || opts[0].(map[string]any)["current"] != "big" || len(opts[0].(map[string]any)["values"].([]any)) != 2 {
		t.Errorf("configOptions = %v", m["configOptions"])
	}
	if m["planMarkdown"] != "1. a\n2. b" || m["title"] != "Fix the parser" {
		t.Errorf("plan markdown %q title %q", m["planMarkdown"], m["title"])
	}
	caps, _ := m["capabilities"].(map[string]any)
	if caps["loadSession"] != true || caps["image"] != true || caps["listSessions"] != true || caps["audio"] != false || caps["forkSession"] != false {
		t.Errorf("capabilities = %v", m["capabilities"])
	}
}

// A pane with nothing to say about its session sends none of the new fields,
// and capabilities only once the agent is ready.
func TestStructuredMessageOmitsAnEmptySession(t *testing.T) {
	for _, sn := range []structured.Snapshot{
		{Phase: structured.PhaseStarting},
		{Phase: structured.PhaseSetup, Session: bridge.SessionState{Capabilities: acp.AgentCapabilities{LoadSession: true}}},
	} {
		raw, _ := json.Marshal(buildStructured("p1", sn))
		for _, k := range []string{"title", "commands", "modes", "models", "configOptions", "planMarkdown", "capabilities"} {
			if strings.Contains(string(raw), `"`+k+`"`) {
				t.Errorf("phase %s: %s is present: %s", sn.Phase, k, raw)
			}
		}
	}
	// Ready with nothing listed: the capabilities are there, the rest is not.
	raw, _ := json.Marshal(buildStructured("p1", structured.Snapshot{Phase: structured.PhaseReady}))
	if !strings.Contains(string(raw), `"capabilities"`) || strings.Contains(string(raw), `"commands"`) {
		t.Errorf("ready, nothing listed: %s", raw)
	}
}

func TestStructuredSessionStateIsBounded(t *testing.T) {
	long := strings.Repeat("日本語", 500)
	var ss bridge.SessionState
	for i := 0; i < structuredCommandsMax+10; i++ {
		ss.Commands = append(ss.Commands, acp.Command{Name: long, Description: long, Hint: long})
	}
	ss.Modes = &acp.Modes{Current: long}
	ss.Models = &acp.Models{Current: long}
	for i := 0; i < structuredChoicesMax+10; i++ {
		ss.Modes.Available = append(ss.Modes.Available, acp.Mode{ID: long + strconv.Itoa(i), Name: long, Description: long})
		ss.Models.Available = append(ss.Models.Available, acp.Model{ID: long, Name: long, Description: long})
	}
	for i := 0; i < structuredConfigMax+3; i++ {
		o := acp.ConfigOption{ID: long, Name: long, Description: long, Category: long, Type: long, Current: long}
		for j := 0; j < structuredValuesMax+3; j++ {
			o.Values = append(o.Values, acp.ConfigValue{Value: long, Name: long, Description: long, Group: long})
		}
		ss.ConfigOptions = append(ss.ConfigOptions, o)
	}
	ss.PlanMarkdown = strings.Repeat("p", structuredMarkdownCut*2)
	ss.Title = long
	m := buildStructured("p1", structured.Snapshot{Phase: structured.PhaseReady, Session: ss})

	if len(m.Commands) != structuredCommandsMax || len(m.Modes.Available) != structuredChoicesMax || len(m.Models.Available) != structuredChoicesMax ||
		len(m.ConfigOpts) != structuredConfigMax || len(m.ConfigOpts[0].Values) != structuredValuesMax {
		t.Errorf("counts: %d commands, %d modes, %d models, %d options, %d values", len(m.Commands), len(m.Modes.Available), len(m.Models.Available), len(m.ConfigOpts), len(m.ConfigOpts[0].Values))
	}
	over := func(what, s string, max int) {
		if len(s) > max {
			t.Errorf("%s is %d bytes, limit %d", what, len(s), max)
		}
	}
	c := m.Commands[0]
	over("command name", c.Name, structuredCardCut)
	over("command description", c.Description, structuredDescCut)
	over("command hint", c.Hint, structuredCardCut)
	over("mode id", m.Modes.Available[0].ID, structuredCardCut)
	over("mode description", m.Modes.Available[0].Description, structuredDescCut)
	over("current model", m.Models.Current, structuredCardCut)
	o := m.ConfigOpts[0]
	over("option id", o.ID, structuredCardCut)
	over("option category", o.Category, 40)
	over("option current", o.Current, structuredCardCut)
	over("value description", o.Values[0].Description, structuredDescCut)
	over("value group", o.Values[0].Group, structuredCardCut)
	over("plan markdown", m.PlanMarkdown, structuredMarkdownCut)
	over("title", m.Title, structuredCardCut)
}

// The message the server writes for a pane whose agent listed every kind of
// state fits the schema, and so does one with text the agent made hostile.
func TestStructuredSessionMessageFitsTheSchema(t *testing.T) {
	schema, err := schemacheck.Load(structuredSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	ss := structuredSessionFixture("a\x1b[31mred\x1b[0m\nline\x00")
	ss.PlanMarkdown = "# plan\x07\n- [ ] a"
	raw, _ := json.Marshal(buildStructured("p1", structured.Snapshot{Phase: structured.PhaseReady, Session: ss}))
	checkStructured(t, "hostile session state", schema, raw)
	if strings.ContainsAny(string(raw), "\x1b\x00\x07") {
		t.Errorf("a control character is in the message: %q", raw)
	}
}

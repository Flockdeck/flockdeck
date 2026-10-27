package acp

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
)

// The updates below are the shapes the agents send, taken from the capability
// findings (docs/acp-capability-spike.md) and the ACP schema.

func TestDecodeAvailableCommands(t *testing.T) {
	u := decodeUpdate("s", json.RawMessage(`{"sessionUpdate":"available_commands_update","availableCommands":[
		{"name":"compact","description":"Summarise"},
		{"name":"review","description":"Review","input":{"hint":"branch"}},
		"not an object"]}`))
	if u.Kind != UpdateAvailableCommands || len(u.Commands) != 3 {
		t.Fatalf("update = %+v", u)
	}
	if c := u.Commands[1]; c.Name != "review" || c.Description != "Review" || c.Hint != "branch" {
		t.Errorf("command = %+v", c)
	}
	if u.Commands[2].Name != "" {
		t.Errorf("a command that is not an object has a name: %+v", u.Commands[2])
	}
	empty := decodeUpdate("s", json.RawMessage(`{"sessionUpdate":"available_commands_update","availableCommands":[]}`))
	if empty.Commands == nil || len(empty.Commands) != 0 {
		t.Errorf("an empty list must be non-nil and empty: %#v", empty.Commands)
	}
	if absent := decodeUpdate("s", json.RawMessage(`{"sessionUpdate":"available_commands_update"}`)); absent.Commands != nil {
		t.Errorf("an update with no list set one: %#v", absent.Commands)
	}
}

func TestDecodeModeAndInfo(t *testing.T) {
	if u := decodeUpdate("s", json.RawMessage(`{"sessionUpdate":"current_mode_update","currentModeId":"yolo"}`)); u.ModeID != "yolo" {
		t.Errorf("mode = %q", u.ModeID)
	}
	u := decodeUpdate("s", json.RawMessage(`{"sessionUpdate":"session_info_update","title":"Fix it","updatedAt":"2026-10-27T09:00:00Z"}`))
	if u.InfoTitle == nil || *u.InfoTitle != "Fix it" || u.InfoUpdatedAt == nil || *u.InfoUpdatedAt != "2026-10-27T09:00:00Z" {
		t.Errorf("info = %v %v", u.InfoTitle, u.InfoUpdatedAt)
	}
	cleared := decodeUpdate("s", json.RawMessage(`{"sessionUpdate":"session_info_update","title":null}`))
	if cleared.InfoTitle == nil || *cleared.InfoTitle != "" {
		t.Errorf("a null title must clear it: %v", cleared.InfoTitle)
	}
	if absent := decodeUpdate("s", json.RawMessage(`{"sessionUpdate":"session_info_update","updatedAt":"x"}`)); absent.InfoTitle != nil {
		t.Errorf("an update with no title set one: %v", *absent.InfoTitle)
	}
	if odd := decodeUpdate("s", json.RawMessage(`{"sessionUpdate":"session_info_update","title":7}`)); odd.InfoTitle != nil {
		t.Errorf("a title of the wrong type was taken: %v", *odd.InfoTitle)
	}
}

func TestDecodeConfigOptionUpdate(t *testing.T) {
	// opencode: flat values, a model and an effort.
	u := decodeUpdate("s", json.RawMessage(`{"sessionUpdate":"config_option_update","configOptions":[
		{"id":"model","name":"Model","category":"model","type":"select","currentValue":"anthropic/sonnet",
		 "options":[{"value":"anthropic/sonnet","name":"Sonnet"},{"value":"openai/gpt","name":"GPT","description":"d"}]},
		{"id":"effort","name":"Effort","category":"thought_level","type":"select","currentValue":"high",
		 "options":[{"value":"low","name":"Low"},{"value":"high","name":"High"}]},
		{"id":"fast","name":"Fast","type":"boolean","currentValue":true}]}`))
	if len(u.ConfigOptions) != 3 {
		t.Fatalf("options = %+v", u.ConfigOptions)
	}
	m := u.ConfigOptions[0]
	if m.ID != "model" || m.Category != "model" || m.Current != "anthropic/sonnet" || len(m.Values) != 2 || m.Values[1].Description != "d" {
		t.Errorf("model option = %+v", m)
	}
	if f := u.ConfigOptions[2]; f.Current != "true" || len(f.Values) != 0 {
		t.Errorf("boolean option = %+v", f)
	}

	// Grouped values keep the group.
	g := decodeUpdate("s", json.RawMessage(`{"sessionUpdate":"config_option_update","configOptions":[
		{"id":"mode","name":"Mode","type":"select","currentValue":"plan","options":[
		 {"group":"g1","name":"Agents","options":[{"value":"build","name":"Build"},{"value":"plan","name":"Plan"}]},
		 {"value":"solo","name":"Solo"}]}]}`))
	vals := g.ConfigOptions[0].Values
	if len(vals) != 3 || vals[0].Group != "Agents" || vals[1].Value != "plan" || vals[2].Value != "solo" || vals[2].Group != "" {
		t.Errorf("grouped values = %+v", vals)
	}

	// A numeric current value is kept as text; a value of an odd type is empty.
	n := decodeUpdate("s", json.RawMessage(`{"sessionUpdate":"config_option_update","configOptions":[{"id":"t","currentValue":0.5},{"id":"u","currentValue":{"a":1}}]}`))
	if n.ConfigOptions[0].Current != "0.5" || n.ConfigOptions[1].Current != "" {
		t.Errorf("current values = %q %q", n.ConfigOptions[0].Current, n.ConfigOptions[1].Current)
	}
	if bad := decodeUpdate("s", json.RawMessage(`{"sessionUpdate":"config_option_update","configOptions":"no"}`)); bad.ConfigOptions != nil {
		t.Errorf("options that were not a list were taken: %+v", bad.ConfigOptions)
	}
}

func TestDecodePlanUpdateAndCompaction(t *testing.T) {
	// Codex: the plan as a text block.
	if u := decodeUpdate("s", json.RawMessage(`{"sessionUpdate":"plan_update","content":{"type":"text","text":"1. a\n2. b"}}`)); u.PlanMarkdown != "1. a\n2. b" {
		t.Errorf("plan = %q", u.PlanMarkdown)
	}
	if u := decodeUpdate("s", json.RawMessage(`{"sessionUpdate":"plan_update","content":"as a string"}`)); u.PlanMarkdown != "as a string" {
		t.Errorf("plan = %q", u.PlanMarkdown)
	}
	if u := decodeUpdate("s", json.RawMessage(`{"sessionUpdate":"plan_update","markdown":"# plan"}`)); u.PlanMarkdown != "# plan" {
		t.Errorf("plan = %q", u.PlanMarkdown)
	}
	if u := decodeUpdate("s", json.RawMessage(`{"sessionUpdate":"plan_update","content":{"type":"image","uri":"x"}}`)); u.PlanMarkdown != "" {
		t.Errorf("an image became a plan: %q", u.PlanMarkdown)
	}
	c := decodeUpdate("s", json.RawMessage(`{"sessionUpdate":"compaction_update","status":"completed","message":"done"}`))
	if c.Compaction == nil || c.Compaction.Status != "completed" || c.Compaction.Message != "done" {
		t.Errorf("compaction = %+v", c.Compaction)
	}
	if c := decodeUpdate("s", json.RawMessage(`{"sessionUpdate":"compaction"}`)); c.Compaction == nil {
		t.Error("a compaction with no fields was dropped")
	}
}

func TestDecodeKeepsTheRawOfEveryNewKind(t *testing.T) {
	for _, k := range []string{UpdateAvailableCommands, UpdateCurrentMode, UpdateConfigOption, UpdateSessionInfo, UpdatePlanUpdate, UpdateCompaction} {
		raw := json.RawMessage(`{"sessionUpdate":"` + k + `","x":[1,{"y":2}],"configOptions":3,"availableCommands":"z"}`)
		u := decodeUpdate("s", raw)
		if u.Kind != k || string(u.Raw) != string(raw) {
			t.Errorf("%s: kind %q raw %s", k, u.Kind, u.Raw)
		}
	}
}

func TestSessionNewConfigOptionShape(t *testing.T) {
	c := startRaw(t, newRecorder(), nil, acptest.WithConfigOptions())
	if _, err := c.Initialize(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	s, err := c.NewSessionInfo(ctxT(t), acptest.SessionDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.SessionID != "sess-1" || s.Modes != nil || s.Models != nil || len(s.ConfigOptions) != 2 {
		t.Fatalf("session = %+v", s)
	}
	if m := s.ConfigOptions[0]; m.ID != "model" || m.Current != "big" || len(m.Values) != 2 {
		t.Errorf("model = %+v", m)
	}
	if m := s.ConfigOptions[1]; m.ID != "mode" || m.Current != "build" || len(m.Values) != 2 || m.Values[0].Group != "Agents" {
		t.Errorf("mode = %+v", m)
	}
}

func TestSessionNewModesShape(t *testing.T) {
	c := startRaw(t, newRecorder(), nil, acptest.WithModes())
	if _, err := c.Initialize(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	s, err := c.NewSessionInfo(ctxT(t), acptest.SessionDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.ConfigOptions != nil || s.Modes == nil || s.Models == nil {
		t.Fatalf("session = %+v", s)
	}
	if s.Modes.Current != "default" || len(s.Modes.Available) != 2 || s.Modes.Available[0].Description != "Asks before it edits" {
		t.Errorf("modes = %+v", s.Modes)
	}
	if s.Models.Current != "gem-pro" || len(s.Models.Available) != 2 || s.Models.Available[1].ID != "gem-flash" {
		t.Errorf("models = %+v", s.Models)
	}
}

func TestSessionNewWithNoBlocks(t *testing.T) {
	c := startRaw(t, newRecorder(), nil)
	if _, err := c.Initialize(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	s, err := c.NewSessionInfo(ctxT(t), acptest.SessionDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Modes != nil || s.Models != nil || s.ConfigOptions != nil {
		t.Errorf("blocks that were not sent: %+v", s)
	}
}

func TestDecodeSessionNewIsTolerant(t *testing.T) {
	s := decodeSessionNew(json.RawMessage(`{"sessionId":"x","modes":"no","models":[1],"configOptions":{"a":1}}`))
	if s.SessionID != "x" || s.Modes != nil || s.Models != nil || s.ConfigOptions != nil {
		t.Errorf("session = %+v", s)
	}
	s = decodeSessionNew(json.RawMessage(`{"sessionId":"x","modes":{"currentModeId":5,"availableModes":[{"id":"a"},7]}}`))
	if s.Modes == nil || len(s.Modes.Available) == 0 || s.Modes.Available[0].ID != "a" {
		t.Errorf("a modes block with odd fields lost its modes: %+v", s.Modes)
	}
}

func TestCapabilities(t *testing.T) {
	c := startRaw(t, newRecorder(), nil, acptest.WithModes())
	if got := c.Capabilities(); got.LoadSession || got.Prompt.Image {
		t.Errorf("capabilities before initialize: %+v", got)
	}
	r, err := c.Initialize(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	want := AgentCapabilities{LoadSession: true, Prompt: PromptCapabilities{Image: true, EmbeddedContext: true},
		MCP: MCPCapabilities{HTTP: true}, List: true, Fork: true}
	for name, got := range map[string]AgentCapabilities{"result": r.Capabilities, "client": c.Capabilities()} {
		raw := got.Raw
		got.Raw = nil
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s capabilities = %+v, want %+v", name, got, want)
		}
		if len(raw) == 0 {
			t.Errorf("%s capabilities lost the raw block", name)
		}
	}
	if string(r.AgentCapabilities) != string(r.Capabilities.Raw) {
		t.Error("the raw block differs between the result and the capabilities")
	}
}

func TestParseCapabilitiesIsTolerant(t *testing.T) {
	c := parseCapabilities(json.RawMessage(`{"loadSession":"yes","promptCapabilities":{"image":true,"audio":"x"},"sessionCapabilities":[1]}`))
	if !c.Prompt.Image || c.LoadSession || c.List {
		t.Errorf("capabilities = %+v", c)
	}
	if z := parseCapabilities(nil); z.LoadSession || z.Prompt.Image {
		t.Errorf("no block, but %+v", z)
	}
}

// The state scenario of the fake agent sends every kind; the client delivers
// each with its fields.
func TestStateUpdatesArriveOverTheWire(t *testing.T) {
	r := newRecorder()
	c, sid := start(t, r)
	prompt(t, c, sid, "state")
	byKind := map[string]Update{}
	for _, u := range r.all() {
		byKind[u.Kind] = u
	}
	if u := byKind[UpdateAvailableCommands]; len(u.Commands) != 2 || u.Commands[1].Hint != "branch or commit" {
		t.Errorf("commands = %+v", u.Commands)
	}
	if byKind[UpdateCurrentMode].ModeID != "yolo" {
		t.Errorf("mode = %+v", byKind[UpdateCurrentMode])
	}
	if u := byKind[UpdateConfigOption]; len(u.ConfigOptions) != 1 || u.ConfigOptions[0].Current != "small" {
		t.Errorf("config = %+v", u.ConfigOptions)
	}
	if u := byKind[UpdateSessionInfo]; u.InfoTitle == nil || *u.InfoTitle != "Fix the parser" {
		t.Errorf("info = %+v", u)
	}
	if byKind[UpdatePlanUpdate].PlanMarkdown != "1. Read the code\n2. Fix it" {
		t.Errorf("plan = %q", byKind[UpdatePlanUpdate].PlanMarkdown)
	}
	if u := byKind[UpdateUserMessageChunk]; u.Content == nil || u.Content.Text != "echo of the prompt" {
		t.Errorf("user message = %+v", u)
	}
	if u := byKind[UpdateCompaction]; u.Compaction == nil || u.Compaction.Message != "Compacted 80 messages." {
		t.Errorf("compaction = %+v", u.Compaction)
	}
	if u := byKind[UpdateUsage]; u.Used == nil || *u.Used != 90 || *u.Size != 1000 {
		t.Errorf("usage = %+v", u)
	}
}

// T219: the fake agent answers a request it does not know with -32601, as an
// agent that lacks the method does, instead of leaving the call to time out.
func TestTheFakeAgentAnswersAnUnknownMethod(t *testing.T) {
	c, _ := start(t, newRecorder())
	for _, method := range []string{"session/set_mode", "session/set_config_option", "session/list", "nope/nothing"} {
		var res json.RawMessage
		err := c.call(ctxT(t), method, map[string]any{"sessionId": "sess-1"}, &res)
		var e *RPCError
		if !errors.As(err, &e) || e.Code != -32601 {
			t.Errorf("%s: err = %v, want RPC error -32601", method, err)
		}
	}
	// The session still works after them.
	if stop := prompt(t, c, "sess-1", "plain"); stop != StopEndTurn {
		t.Errorf("stop = %q", stop)
	}
}

func TestNoticeUpdateKeepsOnlyTheCode(t *testing.T) {
	u := decodeUpdate("s", json.RawMessage(`{"sessionUpdate":"flockdeck_notice","code":"untested_version","message":"SECRET"}`))
	if u.Kind != UpdateNotice || u.NoticeCode != "untested_version" {
		t.Errorf("update = %+v", u)
	}
}

func TestSessionIDUpdateKeepsOnlyTheID(t *testing.T) {
	u := decodeUpdate("s", json.RawMessage(`{"sessionUpdate":"flockdeck_session","newSessionId":"11111111-2222-4333-8444-555555555555","message":"SECRET"}`))
	if u.Kind != UpdateSessionID || u.NewSessionID != "11111111-2222-4333-8444-555555555555" {
		t.Errorf("update = %+v", u)
	}
}

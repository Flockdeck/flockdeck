package bridge

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// What a card holds of a request, beyond its options.
const (
	// cardBodyCap bounds the text a request shows about its call (the plan of
	// ExitPlanMode), in bytes.
	cardBodyCap = 32 << 10
	// cardDiffCap bounds the diff of an edit on a card, in bytes.
	cardDiffCap = transcript.DiffCap
	// cutRoom is left under a cap for the note that says text was cut.
	cutRoom = 32

	// maxCardDiffs bounds how many edits of one request are compared: each is
	// a line-by-line comparison.
	maxCardDiffs = 8

	// cardCommandCap bounds Card.CommandText, in bytes.
	cardCommandCap = 4 << 10

	maxQuestions       = 4
	maxQuestionOptions = 8
	questionTextCap    = 2000 // bytes of a question or an option's description
	// MaxOtherBytes is the most a person's free-text answer keeps.
	MaxOtherBytes = 2000
)

// AllowOnce is the kind of the one option that answers a question card with
// answers. A card with no option of exactly this kind cannot be allowed.
const AllowOnce = "allow_once"

// QuestionKind is the Kind of a tool call that asks the person questions. Only
// a request of this kind is read for questions.
const QuestionKind = "question"

// Question is one question of a question card. Every string is plain text from
// the agent, cleaned and cut; none of it is markup or a Flockdeck control.
type Question struct {
	Header      string
	Question    string
	MultiSelect bool
	Options     []QuestionOption
}

// QuestionOption is one choice of a Question. A person's answer names it by
// its position, which is its place in the list the agent sent: options are only
// ever cut from the end.
type QuestionOption struct {
	Label       string
	Description string
}

// Answer is the person's answer to one question: the options chosen, by
// position, and text typed as "Other". Question is the position of the
// question on the card.
type Answer struct {
	Question int    `json:"question"`
	Selected []int  `json:"selected"`
	Other    string `json:"other,omitempty"`
}

// Reply is what a card is answered with: an option id as shown on the card,
// and, for a question card answered with an allowing option, the answers.
type Reply struct {
	OptionID string
	Answers  []Answer
}

// ErrBadAnswers is what CheckAnswers returns for answers that do not fit the
// card.
var ErrBadAnswers = errors.New("the answers do not fit the questions")

// parseQuestions reads the questions of a request of kind QuestionKind from its
// raw input: {"questions":[{"question","header","multiSelect","options":[{"label",
// "description"}]}]}. It keeps at most maxQuestions questions and
// maxQuestionOptions options each, the first ones. A question with no text or
// an option with no label makes the whole request malformed: the result is nil
// and the card can only be refused.
func parseQuestions(raw json.RawMessage) []Question {
	var in struct {
		Questions []struct {
			Question    string `json:"question"`
			Header      string `json:"header"`
			MultiSelect bool   `json:"multiSelect"`
			Options     []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
		} `json:"questions"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return nil
	}
	var out []Question
	for i, q := range in.Questions {
		if i == maxQuestions {
			break
		}
		cq := Question{
			Header:      line(q.Header, 60),
			Question:    strings.TrimSpace(clean(capBytes(q.Question, questionTextCap))),
			MultiSelect: q.MultiSelect,
		}
		if cq.Question == "" {
			return nil
		}
		for j, o := range q.Options {
			if j == maxQuestionOptions {
				break
			}
			label := line(o.Label, labelCap)
			if label == "" {
				return nil
			}
			cq.Options = append(cq.Options, QuestionOption{Label: label, Description: strings.TrimSpace(clean(capBytes(o.Description, questionTextCap)))})
		}
		out = append(out, cq)
	}
	return out
}

// CheckAnswers cleans the answers to the questions of a card and reports
// whether they fit: every question answered once, by its position, each with
// options that exist and are not repeated, one at most for a question that
// allows one, and something chosen or typed. The result is in the order of
// the questions, whatever order they came in.
func CheckAnswers(qs []Question, in []Answer) ([]Answer, error) {
	if len(qs) == 0 || len(in) != len(qs) {
		return nil, ErrBadAnswers
	}
	out := make([]Answer, len(qs))
	seen := make([]bool, len(qs))
	for _, a := range in {
		if a.Question < 0 || a.Question >= len(qs) || seen[a.Question] {
			return nil, ErrBadAnswers
		}
		seen[a.Question] = true
		q := qs[a.Question]
		got := map[int]bool{}
		sel := []int{}
		for _, s := range a.Selected {
			if s < 0 || s >= len(q.Options) || got[s] {
				return nil, ErrBadAnswers
			}
			got[s] = true
			sel = append(sel, s)
		}
		other := strings.TrimSpace(clean(capBytes(a.Other, MaxOtherBytes)))
		if len(sel) == 0 && other == "" {
			return nil, ErrBadAnswers
		}
		if !q.MultiSelect && len(sel)+btoi(other != "") > 1 {
			return nil, ErrBadAnswers
		}
		out[a.Question] = Answer{Question: a.Question, Selected: sel, Other: other}
	}
	return out, nil
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// cardContent fills the body and the diff of a card from the content of the
// tool call: text blocks become the body, diffs become one unified diff.
func cardContent(c *Card, content []acp.ToolContent) {
	var texts []string
	var diff string
	diffs := 0
	for _, t := range content {
		switch t.Type {
		case "content":
			if t.Content != nil && t.Content.Type == "text" && t.Content.Text != "" {
				texts = append(texts, t.Content.Text)
			}
		case "diff":
			if len(diff) >= cardDiffCap {
				continue
			}
			if diffs == maxCardDiffs {
				diff += "\n[more edits not shown]\n"
				diffs++
				continue
			}
			if diffs > maxCardDiffs {
				continue
			}
			diffs++
			path, newText := line(t.Path, 1024), clean(capBytes(t.NewText, cardDiffCap))
			if t.OldText == nil {
				diff += transcript.WriteDiff(path, newText)
			} else {
				diff += transcript.EditDiff(path, clean(capBytes(*t.OldText, cardDiffCap)), newText)
			}
		}
	}
	if len(texts) > 0 {
		all := strings.Join(texts, "\n")
		if len(all) > cardBodyCap {
			// The note is inside the cap, so a later cut to the same cap
			// does not take it off.
			all = capBytes(all, cardBodyCap-cutRoom) + "\n[text cut at 32 KiB]"
		}
		c.Body = strings.TrimSpace(clean(all))
	}
	if len(diff) > cardDiffCap {
		diff = capBytes(diff, cardDiffCap-cutRoom) + "\n[diff cut]\n"
	}
	c.Diff = diff
}

// answersMeta is what goes to the agent next to the outcome: the answers, in
// the order of the questions.
func answersMeta(a []Answer) json.RawMessage {
	b, err := json.Marshal(a)
	if err != nil {
		return nil
	}
	return b
}

// commandText is the command a request names, whole: cleaned, with its line
// breaks, and cut at cardCommandCap bytes. altered says it differs from what the
// agent sent, because cleaning took a character out (a control, a bidi mark, a
// byte that is not UTF-8) or because it was cut.
func commandText(raw string) (text string, altered bool) {
	cut := capBytes(raw, cardCommandCap)
	text = clean(cut)
	return text, len(cut) < len(raw) || text != cut
}

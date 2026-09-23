package jev

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Question is one thing to ask Jev about the state. It is one of Noul, Choice
// or Score -- the three types TypeSafe has, which cannot be added to from
// outside the package, since a fourth would be one the wire does not know.
//
// In every one of them Instructions is what is being asked, and TypeSafe takes
// it as a string, or an object or array that says it with structure; anything
// that marshals to JSON will do.
type Question interface {
	// wire is the question as TypeSafe reads it, or the reason it will not.
	wire() (wireQuestion, error)
	// kind is the type an answer to it must be.
	kind() string
}

// Noul asks for the probability, from 0 to 1, that a statement holds of the
// state: "the message conveys urgency". It is a probability, not a yes or a
// no, so a caller who wants a branch picks the threshold it will act on.
type Noul struct {
	Instructions any
	// True and False say what each side of the question means, for when the
	// instructions alone leave that open. Both are optional, and either is
	// left out of the request when empty.
	True, False any
}

func (q Noul) kind() string { return "noul" }

func (q Noul) wire() (wireQuestion, error) {
	if q.Instructions == nil {
		return wireQuestion{}, fmt.Errorf("has no instructions")
	}
	w := wireQuestion{Type: "noul", Instructions: q.Instructions}
	if q.True != nil || q.False != nil {
		c := map[string]any{}
		if q.True != nil {
			c["true"] = q.True
		}
		if q.False != nil {
			c["false"] = q.False
		}
		w.Criteria = c
	}
	return w, nil
}

// Choice asks which one of the named options fits best. Options maps each
// option's name to a description, which TypeSafe shows the model along with
// the name, so both should say something; nil describes an option by its name
// alone, for one that needs no more. At most 255 options.
//
// The name is what comes back as the choice, so it is the caller's own
// identifier for the branch it will take, not text for a person.
type Choice struct {
	Instructions any
	Options      map[string]any
}

func (q Choice) kind() string { return "choice" }

func (q Choice) wire() (wireQuestion, error) {
	if q.Instructions == nil {
		return wireQuestion{}, fmt.Errorf("has no instructions")
	}
	if len(q.Options) < 1 || len(q.Options) > maxChoices {
		return wireQuestion{}, fmt.Errorf("has %d options, want 1 to %d", len(q.Options), maxChoices)
	}
	for name := range q.Options {
		if name == "" {
			return wireQuestion{}, fmt.Errorf("has an option with no name")
		}
	}
	return wireQuestion{Type: "choice", Instructions: q.Instructions, Criteria: q.Options}, nil
}

// Score asks where the state falls on an ordered rubric. Levels describes
// each one from the lowest, and the answer is a number from 0 to
// len(Levels)-1 that need not be a whole one: TypeSafe weights each level by
// its probability, so a state between two reads as between them. Two to ten
// levels, each described by what a state at that level looks like rather than
// by how much of something it has.
type Score struct {
	Instructions any
	Levels       []any
}

func (q Score) kind() string { return "score" }

func (q Score) wire() (wireQuestion, error) {
	if q.Instructions == nil {
		return wireQuestion{}, fmt.Errorf("has no instructions")
	}
	if len(q.Levels) < minScoreLevels || len(q.Levels) > maxScoreLevels {
		return wireQuestion{}, fmt.Errorf("has %d levels, want %d to %d", len(q.Levels), minScoreLevels, maxScoreLevels)
	}
	return wireQuestion{Type: "score", Instructions: q.Instructions, Criteria: q.Levels}, nil
}

// request and wireQuestion are the request body, field for field.
type request struct {
	State     any                     `json:"state"`
	Model     string                  `json:"model"`
	Questions map[string]wireQuestion `json:"questions"`
}

type wireQuestion struct {
	Type         string `json:"type"`
	Instructions any    `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

// encodeQuestions checks every question and puts them in the shape the wire
// has. They are checked in key order, so that with two wrong the one named is
// always the same.
func encodeQuestions(qs map[string]Question) (map[string]wireQuestion, error) {
	if len(qs) == 0 {
		return nil, fmt.Errorf("%w: no questions to ask", ErrInvalidRequest)
	}
	keys := make([]string, 0, len(qs))
	for k := range qs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]wireQuestion, len(qs))
	for _, k := range keys {
		q := qs[k]
		if k == "" {
			return nil, fmt.Errorf("%w: a question has no key", ErrInvalidRequest)
		}
		if q == nil {
			return nil, fmt.Errorf("%w: question %q is nil", ErrInvalidRequest, k)
		}
		w, err := q.wire()
		if err != nil {
			return nil, fmt.Errorf("%w: question %q %v", ErrInvalidRequest, k, err)
		}
		out[k] = w
	}
	return out, nil
}

// Answer is what Jev said to one question: a NoulAnswer, ChoiceAnswer or
// ScoreAnswer, matching the question. Result.Noul, Choice and Score give it as
// its own type, and are how a caller who knows what it asked should read it.
type Answer interface {
	// Kind is the question type it answers: "noul", "choice" or "score".
	Kind() string
}

// NoulAnswer is the probability, 0 to 1, that the statement holds.
type NoulAnswer struct {
	Probability float64
}

func (NoulAnswer) Kind() string { return "noul" }

// ChoiceAnswer is the option picked, with how likely each was.
type ChoiceAnswer struct {
	// Choice is the most probable option's name.
	Choice        string
	Probabilities map[string]float64
	// Confidence, 0 to 1, is how concentrated the probabilities are. It is
	// not the probability of Choice: TypeSafe's own example has 0.85 on its
	// choice and a confidence of 0.78. It measures how sure the answer is, not
	// whether it is right, and a caller routing on it wants a threshold of its
	// own from looking at real answers.
	Confidence float64
}

func (ChoiceAnswer) Kind() string { return "choice" }

// ScoreAnswer is the position on the rubric.
type ScoreAnswer struct {
	// Score is 0 to len(Levels)-1, and fractional when the probability is
	// spread over levels: the mean of the level numbers weighted by it.
	Score float64
	// Legend is each level's description as the question gave it, keyed by
	// the level's number as a string ("0", "1", ...), which is how TypeSafe
	// sends it.
	Legend map[string]any
	// Probabilities is how likely each level was, keyed like Legend.
	Probabilities map[string]float64
	// Confidence is as it is for a ChoiceAnswer.
	Confidence float64
}

func (ScoreAnswer) Kind() string { return "score" }

// wireAnswer is any of the three answers, since which one it is is in its own
// type field. What each has of its own is a pointer or a map, so that an
// answer without it can be told from one with a zero.
type wireAnswer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul"`
	Choice        *string            `json:"choice"`
	Score         *float64           `json:"score"`
	Legend        map[string]any     `json:"legend"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    *float64           `json:"confidence"`
}

type response struct {
	Model   string                     `json:"model"`
	Answers map[string]json.RawMessage `json:"answers"`
	Usage   Usage                      `json:"usage"`
}

// decode reads TypeSafe's answer against the questions that were asked, and
// holds it to them: an answer for every question, of the question's own type,
// with what that type has. A caller then reads Result without checking, and
// one answer missing is the whole call being an error, not a nil to be met
// later where it is not clear what asked for it.
//
// Answers to questions that were not asked are dropped, not an error: they
// are nothing the caller can be misled by.
func decode(data []byte, asked map[string]Question) (*Result, error) {
	var resp response
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("%w: not JSON of the shape expected: %v", ErrBadResponse, err)
	}
	res := &Result{Model: resp.Model, Usage: resp.Usage, Answers: make(map[string]Answer, len(asked))}
	for key, q := range asked {
		raw, ok := resp.Answers[key]
		if !ok {
			return nil, fmt.Errorf("%w: no answer to %q", ErrBadResponse, key)
		}
		var w wireAnswer
		if err := json.Unmarshal(raw, &w); err != nil {
			return nil, fmt.Errorf("%w: the answer to %q: %v", ErrBadResponse, key, err)
		}
		if w.Type != q.kind() {
			return nil, fmt.Errorf("%w: %q was asked as a %s and answered as %q", ErrBadResponse, key, q.kind(), w.Type)
		}
		a, err := w.answer()
		if err != nil {
			return nil, fmt.Errorf("%w: the answer to %q %v", ErrBadResponse, key, err)
		}
		res.Answers[key] = a
	}
	return res, nil
}

func (w wireAnswer) answer() (Answer, error) {
	switch w.Type {
	case "noul":
		if w.Noul == nil {
			return nil, fmt.Errorf("has no noul")
		}
		return NoulAnswer{Probability: *w.Noul}, nil
	case "choice":
		if w.Choice == nil || w.Confidence == nil {
			return nil, fmt.Errorf("has no choice or confidence")
		}
		return ChoiceAnswer{Choice: *w.Choice, Probabilities: w.Probabilities, Confidence: *w.Confidence}, nil
	case "score":
		if w.Score == nil || w.Confidence == nil {
			return nil, fmt.Errorf("has no score or confidence")
		}
		return ScoreAnswer{Score: *w.Score, Legend: w.Legend, Probabilities: w.Probabilities, Confidence: *w.Confidence}, nil
	}
	return nil, fmt.Errorf("is of the unknown type %q", w.Type)
}

package claudeacp

import (
	"encoding/json"
	"strings"

	"github.com/jmwri/flockdeck/internal/acp/bridge"
)

// AskUserQuestion is answered through the client: the request goes out as a
// session/request_permission of kind "question" with the input as rawInput, and
// the person's answers come back in _meta.flockdeck.answers of the response, by
// position. The tool is allowed with updatedInput that is the input plus
// "answers", {question text: the labels chosen, joined by ", ", and any text
// typed as Other}, which is what claude reads. Anything that is not exactly what
// the tool takes is denied, and so is anything the client answers that does not
// fit the questions.

const (
	optAnswer = "answer"

	maxAskQuestions = 4
	maxAskOptions   = 8
	maxAskQuestion  = 2000 // bytes of a question or a description
	maxAskLabel     = 200
	maxAskOther     = 2000

	denyAskMalformed = "The question could not be shown. Ask the user in plain text instead."
	denyAskDeclined  = "The user declined to answer the questions."
	denyAskCancelled = "The question was cancelled."
)

type askQuestion struct {
	Question    string `json:"question"`
	Header      string `json:"header"`
	MultiSelect bool   `json:"multiSelect"`
	Options     []struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"options"`
	// shown is each option's label as the card shows it: cleaned and on one
	// line. It is what claude is told the person chose.
	shown []string
}

// parseAsk reads the input of AskUserQuestion. ok is false for anything that is
// not a list of one to four distinct questions with distinct options, within the
// caps: the request is then denied, not shown cut.
func parseAsk(input json.RawMessage) ([]askQuestion, bool) {
	var in struct {
		Questions []askQuestion `json:"questions"`
	}
	if json.Unmarshal(input, &in) != nil || len(in.Questions) == 0 || len(in.Questions) > maxAskQuestions {
		return nil, false
	}
	texts := map[string]bool{}
	for qi := range in.Questions {
		q := &in.Questions[qi]
		// The answers are keyed by the text of the question.
		if strings.TrimSpace(q.Question) == "" || len(q.Question) > maxAskQuestion || len(q.Header) > maxAskLabel || len(q.Options) > maxAskOptions || texts[q.Question] {
			return nil, false
		}
		texts[q.Question] = true
		labels := map[string]bool{}
		for _, o := range q.Options {
			// A label is what comes back, as the card shows it, so two alike
			// once cleaned, one that cleans to nothing, or one that holds the
			// separator could not be told apart in the answer.
			shown := bridge.Line(o.Label, maxAskLabel)
			if shown == "" || len(o.Label) > maxAskLabel || len(o.Description) > maxAskQuestion || labels[shown] || strings.Contains(shown, ", ") {
				return nil, false
			}
			labels[shown] = true
			q.shown = append(q.shown, shown)
		}
	}
	return in.Questions, true
}

// askAnswers builds the answers map of updatedInput from the client's answers.
// ok is false unless every question has exactly one answer, with options that
// exist and are not repeated, one at most for a question that takes one, and
// something chosen or typed.
func askAnswers(qs []askQuestion, raw json.RawMessage) (map[string]string, bool) {
	var in []struct {
		Question *int   `json:"question"`
		Selected []int  `json:"selected"`
		Other    string `json:"other"`
	}
	if json.Unmarshal(raw, &in) != nil || len(in) != len(qs) {
		return nil, false
	}
	out := make(map[string]string, len(qs))
	seen := make([]bool, len(qs))
	for _, a := range in {
		if a.Question == nil || *a.Question < 0 || *a.Question >= len(qs) || seen[*a.Question] {
			return nil, false
		}
		i := *a.Question
		seen[i] = true
		q := qs[i]
		other := strings.TrimSpace(a.Other)
		if len(other) > maxAskOther {
			return nil, false
		}
		var parts []string
		got := map[int]bool{}
		for _, s := range a.Selected {
			if s < 0 || s >= len(q.Options) || got[s] {
				return nil, false
			}
			got[s] = true
			parts = append(parts, q.shown[s])
		}
		if other != "" {
			parts = append(parts, other)
		}
		if len(parts) == 0 || (!q.MultiSelect && len(parts) > 1) {
			return nil, false
		}
		out[q.Question] = strings.Join(parts, ", ")
	}
	return out, true
}

func (ss *session) handleQuestion(requestID, toolUseID string, input json.RawMessage) {
	qs, ok := parseAsk(input)
	if !ok {
		ss.respond(requestID, map[string]any{"behavior": "deny", "message": denyAskMalformed})
		return
	}
	toolID := toolUseID
	if toolID == "" {
		toolID = requestID
	}
	ss.announce(toolID, "AskUserQuestion", input, "")
	call := map[string]any{"toolCallId": toolID, "title": "Question from Claude", "kind": "question", "status": "pending", "rawInput": input}
	options := []any{
		map[string]any{"optionId": optAnswer, "name": "Send answers", "kind": "allow_once"},
		map[string]any{"optionId": optReject, "name": "Skip", "kind": "reject_once"},
	}
	res, asked := ss.askPermission(requestID, call, options)
	if !asked {
		return
	}
	switch {
	case !res.selected:
		ss.respond(requestID, map[string]any{"behavior": "deny", "message": denyAskCancelled})
	case res.optionID == optAnswer:
		answers, ok := askAnswers(qs, res.answers)
		var m map[string]json.RawMessage
		if !ok || json.Unmarshal(input, &m) != nil {
			ss.respond(requestID, map[string]any{"behavior": "deny", "message": denyAskCancelled})
			return
		}
		b, _ := json.Marshal(answers)
		m["answers"] = b
		updated, _ := json.Marshal(m)
		ss.allow(requestID, toolID, updated)
	default:
		ss.respond(requestID, map[string]any{"behavior": "deny", "message": denyAskDeclined})
	}
}
